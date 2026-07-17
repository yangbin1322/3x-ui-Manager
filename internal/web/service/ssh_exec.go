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

// ExecResult is the outcome of running a command on one node, returned to the
// caller and mirrored into the audit log.
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

// ExecCommand runs cmd on a single ssh-mode node, records the outcome in the
// audit log, and returns it. username is the panel user who initiated it, kept
// for the audit trail. It reuses the existing SSH dial/run primitives; the only
// new behavior here is timeout clamping, output capping, and the audit write.
func (s *NodeService) ExecCommand(ctx context.Context, nodeId int, cmd string, timeout time.Duration, username string) (*ExecResult, error) {
	n, err := s.GetById(nodeId)
	if err != nil || n == nil {
		return nil, common.NewError("node not found")
	}
	if n.Mode != "ssh" {
		return nil, common.NewError("command execution is only available for ssh-mode nodes")
	}
	res := s.execOnNode(ctx, n, cmd, clampExecTimeout(timeout))
	s.writeAudit("", n, cmd, username, res)
	return res, nil
}

// BatchExecResult is the outcome of running one command across several nodes.
// BatchId ties the per-node audit rows together for the history view.
type BatchExecResult struct {
	BatchId string       `json:"batchId" example:"a1b2c3d4"`
	Results []ExecResult `json:"results"`
}

// ExecCommandBatch runs cmd on each of nodeIds concurrently, bounded by
// execConcurrency, and records every execution under one shared batch id.
// Results are returned in the input order regardless of completion order so the
// caller can line them up with the nodes it asked about. A node that is missing
// or not ssh-mode becomes a failed result rather than aborting the batch, and is
// not audited because nothing ran on it.
func (s *NodeService) ExecCommandBatch(ctx context.Context, nodeIds []int, cmd string, timeout time.Duration, username string) *BatchExecResult {
	batchId := uuid.NewString()
	clamped := clampExecTimeout(timeout)
	results := make([]ExecResult, len(nodeIds))

	sem := make(chan struct{}, execConcurrency)
	var wg sync.WaitGroup
	for i, id := range nodeIds {
		n, err := s.GetById(id)
		if err != nil || n == nil {
			results[i] = ExecResult{NodeId: id, Status: execStatusFailed, ExitCode: -1, Error: "node not found"}
			continue
		}
		if n.Mode != "ssh" {
			results[i] = ExecResult{NodeId: id, NodeName: n.Name, Status: execStatusFailed, ExitCode: -1, Error: "not an ssh-mode node"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, n *model.Node) {
			defer wg.Done()
			defer func() { <-sem }()
			res := s.execOnNode(ctx, n, cmd, clamped)
			s.writeAudit(batchId, n, cmd, username, res)
			results[i] = *res
		}(i, n)
	}
	wg.Wait()

	return &BatchExecResult{BatchId: batchId, Results: results}
}

// execOnNode performs one execution with its own bounded context and maps the
// transport outcome onto an ExecResult + status. It never returns an error: a
// failure is a recorded result, not an exception, so a batch caller keeps going.
func (s *NodeService) execOnNode(ctx context.Context, n *model.Node, cmd string, timeout time.Duration) *ExecResult {
	out := &ExecResult{NodeId: n.Id, NodeName: n.Name}
	started := time.Now()
	defer func() { out.DurationMs = int(time.Since(started).Milliseconds()) }()
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ssh := SSHService{}
	dial, err := ssh.Dial(runCtx, n)
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
func (s *NodeService) writeAudit(batchId string, n *model.Node, cmd, username string, res *ExecResult) {
	rec := &model.CommandExecution{
		BatchId:    batchId,
		NodeId:     n.Id,
		NodeName:   n.Name,
		Username:   username,
		Command:    cmd,
		Stdout:     res.Stdout,
		Error:      res.Error,
		ExitCode:   res.ExitCode,
		Status:     res.Status,
		DurationMs: res.DurationMs,
	}
	if err := database.GetDB().Create(rec).Error; err != nil {
		logger.Warning("command audit write failed for node", n.Id, ":", err)
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
func (s *NodeService) ExecHistory(p ExecHistoryParams) (*ExecHistoryResponse, error) {
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
func (s *NodeService) PruneExecHistory(olderThanDays int) (int64, error) {
	if olderThanDays <= 0 {
		return 0, common.NewError("olderThanDays must be positive")
	}
	cutoff := time.Now().AddDate(0, 0, -olderThanDays).UnixMilli()
	res := database.GetDB().Where("created_at < ?", cutoff).Delete(&model.CommandExecution{})
	return res.RowsAffected, res.Error
}
