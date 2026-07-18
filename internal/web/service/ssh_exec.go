package service

import (
	"context"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"

	"github.com/google/uuid"
)

// ExecResult is the outcome of running a command on one managed server,
// returned to the caller and mirrored into the audit log. NodeId/NodeName carry
// the managed server's id and name; the wire field names predate the
// Node/ManagedServer split.
type ExecResult struct {
	NodeId     int    `json:"nodeId" example:"3"`
	NodeName   string `json:"nodeName" example:"hk-1"`
	Status     string `json:"status" example:"success"`
	ExitCode   int    `json:"exitCode" example:"0"`
	Stdout     string `json:"stdout"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"durationMs" example:"142"`
}

// Exec status values.
const (
	execStatusSuccess     = "success"
	execStatusFailed      = "failed"
	execStatusUnreachable = "unreachable"
	execStatusTimeout     = "timeout"
)

// clampExecTimeout keeps a caller-supplied timeout within [1s, execMaxTimeout],
// defaulting when unset. The hard ceiling means a command that hangs cannot hold
// its SSH connection (and, for batches, a concurrency slot) open indefinitely.
func clampExecTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return execDefaultTimeout
	}
	if d > execMaxTimeout {
		return execMaxTimeout
	}
	if d < time.Second {
		return time.Second
	}
	return d
}

// truncateOutput caps stored output so one large-output command cannot bloat the
// audit table. A truncation marker is appended so the record is honest about it.
func truncateOutput(s string) string {
	if len(s) <= execOutputCap {
		return s
	}
	return s[:execOutputCap] + "\n... [output truncated]"
}

// ExecCommand runs cmd on a single managed server, records the outcome in the
// audit log, and returns it. username is the panel user who initiated it, kept
// for the audit trail. It reuses the existing SSH dial/run primitives; the only
// new behavior here is timeout clamping, output capping, and the audit write.
func (s *ManagedServerService) ExecCommand(ctx context.Context, serverId int, cmd string, timeout time.Duration, username string) (*ExecResult, error) {
	srv, err := s.GetById(serverId)
	if err != nil || srv == nil {
		return nil, common.NewError("server not found")
	}
	res := s.execOnServer(ctx, srv, cmd, clampExecTimeout(timeout))
	s.writeAudit("", srv, cmd, username, res)
	return res, nil
}

// BatchExecResult is the outcome of running one command across several managed
// servers. BatchId ties the per-server audit rows together for the history view.
type BatchExecResult struct {
	BatchId string       `json:"batchId" example:"a1b2c3d4"`
	Results []ExecResult `json:"results"`
}

// ExecCommandBatch runs cmd on each of serverIds concurrently, bounded by
// execConcurrency, and records every execution under one shared batch id.
// Results are returned in the input order regardless of completion order so the
// caller can line them up with the servers it asked about. A server that is
// missing becomes a failed result rather than aborting the batch, and is not
// audited because nothing ran on it.
func (s *ManagedServerService) ExecCommandBatch(ctx context.Context, serverIds []int, cmd string, timeout time.Duration, username string) *BatchExecResult {
	batchId := uuid.NewString()
	clamped := clampExecTimeout(timeout)
	results := make([]ExecResult, len(serverIds))

	sem := make(chan struct{}, execConcurrency)
	var wg sync.WaitGroup
	for i, id := range serverIds {
		srv, err := s.GetById(id)
		if err != nil || srv == nil {
			results[i] = ExecResult{NodeId: id, Status: execStatusFailed, ExitCode: -1, Error: "server not found"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, srv *model.ManagedServer) {
			defer wg.Done()
			defer func() { <-sem }()
			res := s.execOnServer(ctx, srv, cmd, clamped)
			s.writeAudit(batchId, srv, cmd, username, res)
			results[i] = *res
		}(i, srv)
	}
	wg.Wait()

	return &BatchExecResult{BatchId: batchId, Results: results}
}

// execOnServer performs one execution with its own bounded context and maps the
// transport outcome onto an ExecResult + status. It never returns an error: a
// failure is a recorded result, not an exception, so a batch caller keeps going.
func (s *ManagedServerService) execOnServer(ctx context.Context, srv *model.ManagedServer, cmd string, timeout time.Duration) *ExecResult {
	out := &ExecResult{NodeId: srv.Id, NodeName: srv.Name}
	started := time.Now()
	defer func() { out.DurationMs = int(time.Since(started).Milliseconds()) }()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ssh := SSHService{}
	dial, err := ssh.Dial(runCtx, srv)
	if err != nil {
		out.Status = execStatusUnreachable
		out.ExitCode = -1
		out.Error = err.Error()
		return out
	}
	defer dial.Client.Close()

	stdout, runErr := runOnClient(runCtx, dial.Client, cmd)
	out.Stdout = truncateOutput(stdout)
	if runErr == nil {
		out.Status = execStatusSuccess
		out.ExitCode = 0
		return out
	}
	if runCtx.Err() == context.DeadlineExceeded {
		out.Status = execStatusTimeout
		out.ExitCode = -1
		out.Error = "command timed out"
		return out
	}
	// A non-zero exit is a "ran, but failed" result: surface the exit code when
	// the SSH layer reports one, otherwise record it as a generic failure.
	out.Status = execStatusFailed
	out.ExitCode = exitCodeOf(runErr)
	out.Error = runErr.Error()
	return out
}

// writeAudit persists one execution record. Audit failures are logged but never
// block the caller: the command already ran, and losing the response is worse
// than a missing audit row, which is surfaced in logs for follow-up.
func (s *ManagedServerService) writeAudit(batchId string, srv *model.ManagedServer, cmd, username string, res *ExecResult) {
	rec := &model.CommandExecution{
		BatchId:    batchId,
		NodeId:     srv.Id,
		NodeName:   srv.Name,
		Username:   username,
		Command:    cmd,
		Stdout:     res.Stdout,
		Error:      res.Error,
		ExitCode:   res.ExitCode,
		Status:     res.Status,
		DurationMs: res.DurationMs,
	}
	if err := database.GetDB().Create(rec).Error; err != nil {
		logger.Warning("command audit write failed for server", srv.Id, ":", err)
	}
}

// ExecHistoryParams are the filters for querying the command audit log. Zero
// values mean "no filter". Page is 1-based.
type ExecHistoryParams struct {
	Page     int    `form:"page"`
	PageSize int    `form:"pageSize"`
	NodeId   int    `form:"nodeId"`
	Username string `form:"username"`
	Status   string `form:"status"`
}

// ExecHistoryResponse is one page of audit rows, newest first, plus the total
// matching the filter so the UI can paginate.
type ExecHistoryResponse struct {
	Items    []model.CommandExecution `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page" example:"1"`
	PageSize int                      `json:"pageSize" example:"20"`
}

const (
	execHistoryDefaultPageSize = 20
	execHistoryMaxPageSize     = 200
)

// ExecHistory returns a filtered, paginated page of the command audit log,
// ordered newest first. The audit log has no delete endpoint by design; this is
// read-only and cannot mutate the trail.
func (s *ManagedServerService) ExecHistory(p ExecHistoryParams) (*ExecHistoryResponse, error) {
	page := p.Page
	if page < 1 {
		page = 1
	}
	size := p.PageSize
	if size <= 0 {
		size = execHistoryDefaultPageSize
	}
	if size > execHistoryMaxPageSize {
		size = execHistoryMaxPageSize
	}

	q := database.GetDB().Model(&model.CommandExecution{})
	if p.NodeId > 0 {
		q = q.Where("node_id = ?", p.NodeId)
	}
	if p.Username != "" {
		q = q.Where("username = ?", p.Username)
	}
	if p.Status != "" {
		q = q.Where("status = ?", p.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []model.CommandExecution
	if err := q.Order("id desc").
		Limit(size).Offset((page - 1) * size).
		Find(&items).Error; err != nil {
		return nil, err
	}
	return &ExecHistoryResponse{Items: items, Total: total, Page: page, PageSize: size}, nil
}

// PruneExecHistory deletes audit rows older than the given number of days. It is
// the only way to remove audit records — there is no per-row delete — so the
// trail can be retention-managed without being selectively erased. A
// non-positive olderThanDays is rejected to avoid wiping the whole log by
// accident.
func (s *ManagedServerService) PruneExecHistory(olderThanDays int) (int64, error) {
	if olderThanDays <= 0 {
		return 0, common.NewError("olderThanDays must be positive")
	}
	cutoff := time.Now().AddDate(0, 0, -olderThanDays).UnixMilli()
	res := database.GetDB().Where("created_at < ?", cutoff).Delete(&model.CommandExecution{})
	return res.RowsAffected, res.Error
}
