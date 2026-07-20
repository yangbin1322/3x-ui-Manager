package service

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"github.com/pkg/sftp"
)

const (
	// uploadDefaultTimeout / uploadMaxTimeout bound a single server's upload. A
	// file transfer legitimately takes longer than a shell command, so the
	// ceiling is higher than execMaxTimeout, but still finite so a stalled
	// transfer cannot hold its connection (and a batch slot) open forever.
	uploadDefaultTimeout = 2 * time.Minute
	uploadMaxTimeout     = 30 * time.Minute

	// uploadConcurrency caps how many servers a batch upload writes to at once.
	// Matches execConcurrency: an upload is a side-effecting action, so the
	// fan-out is kept small to limit the blast radius of a mistake.
	uploadConcurrency = 8
)

// UploadResult is the outcome of writing one file to one managed server.
type UploadResult struct {
	ServerId   int    `json:"serverId" example:"3"`
	ServerName string `json:"serverName" example:"hk-1"`
	Status     string `json:"status" example:"success"`
	Path       string `json:"path" example:"/root/app.conf"`
	Bytes      int64  `json:"bytes" example:"20480"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"durationMs" example:"842"`
}

// Upload status values reuse the exec vocabulary so the UI can render both the
// same way.
const (
	uploadStatusSuccess     = execStatusSuccess
	uploadStatusFailed      = execStatusFailed
	uploadStatusUnreachable = execStatusUnreachable
	uploadStatusTimeout     = execStatusTimeout
)

func clampUploadTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return uploadDefaultTimeout
	}
	if d > uploadMaxTimeout {
		return uploadMaxTimeout
	}
	if d < time.Second {
		return time.Second
	}
	return d
}

// resolveRemotePath turns the operator-supplied destination into a concrete
// remote file path. A destination ending in "/" (or empty) is treated as a
// directory the file is dropped into under its original name; otherwise it is
// the full target path. The original name is base-cleaned so a crafted
// "../../etc/passwd" filename cannot escape the chosen directory.
func resolveRemotePath(dest, fileName string) (string, error) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", fmt.Errorf("destination path is required")
	}
	if strings.HasSuffix(dest, "/") {
		base := path.Base(path.Clean("/" + strings.ReplaceAll(fileName, "\\", "/")))
		if base == "/" || base == "." || base == "" {
			return "", fmt.Errorf("file name is required when destination is a directory")
		}
		return path.Join(dest, base), nil
	}
	return dest, nil
}

// uploadToServer opens one SFTP session, ensures the parent directory exists,
// and streams content to the resolved path. It never returns an error: a
// failure becomes a recorded result so a batch keeps going.
func (s *ManagedServerService) uploadToServer(ctx context.Context, srv *model.ManagedServer, dest, fileName string, content io.Reader, timeout time.Duration) *UploadResult {
	out := &UploadResult{ServerId: srv.Id, ServerName: srv.Name}
	started := time.Now()
	defer func() { out.DurationMs = int(time.Since(started).Milliseconds()) }()

	remotePath, err := resolveRemotePath(dest, fileName)
	if err != nil {
		out.Status = uploadStatusFailed
		out.Error = err.Error()
		return out
	}
	out.Path = remotePath

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ssh := SSHService{}
	dial, err := ssh.Dial(runCtx, srv)
	if err != nil {
		out.Status = uploadStatusUnreachable
		out.Error = err.Error()
		return out
	}
	defer dial.Client.Close()

	type result struct {
		bytes int64
		err   error
	}
	done := make(chan result, 1)
	go func() {
		client, cErr := sftp.NewClient(dial.Client)
		if cErr != nil {
			done <- result{err: cErr}
			return
		}
		defer client.Close()

		if dir := path.Dir(remotePath); dir != "" && dir != "." && dir != "/" {
			_ = client.MkdirAll(dir)
		}
		f, cErr := client.Create(remotePath)
		if cErr != nil {
			done <- result{err: cErr}
			return
		}
		n, cErr := io.Copy(f, content)
		closeErr := f.Close()
		if cErr == nil {
			cErr = closeErr
		}
		done <- result{bytes: n, err: cErr}
	}()

	select {
	case <-runCtx.Done():
		if runCtx.Err() == context.DeadlineExceeded {
			out.Status = uploadStatusTimeout
			out.Error = "upload timed out"
		} else {
			out.Status = uploadStatusFailed
			out.Error = runCtx.Err().Error()
		}
		return out
	case r := <-done:
		if r.err != nil {
			out.Status = uploadStatusFailed
			out.Error = r.err.Error()
			return out
		}
		out.Status = uploadStatusSuccess
		out.Bytes = r.bytes
		return out
	}
}

// BatchUploadResult is the outcome of writing one file across several servers.
type BatchUploadResult struct {
	Results []UploadResult `json:"results"`
}

// UploadFileBatch writes the same file to dest on each of serverIds, bounded by
// uploadConcurrency. Because the content is a single reader that cannot be read
// concurrently, it is buffered once by the caller and each goroutine gets its
// own reader over that buffer. Results come back in input order. A missing
// server becomes a failed result rather than aborting the batch.
func (s *ManagedServerService) UploadFileBatch(ctx context.Context, serverIds []int, dest, fileName string, content []byte, timeout time.Duration) *BatchUploadResult {
	clamped := clampUploadTimeout(timeout)
	results := make([]UploadResult, len(serverIds))

	sem := make(chan struct{}, uploadConcurrency)
	var wg sync.WaitGroup
	for i, id := range serverIds {
		srv, err := s.GetById(id)
		if err != nil || srv == nil {
			results[i] = UploadResult{ServerId: id, Status: uploadStatusFailed, Error: "server not found"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, srv *model.ManagedServer) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = *s.uploadToServer(ctx, srv, dest, fileName, strings.NewReader(string(content)), clamped)
		}(i, srv)
	}
	wg.Wait()

	return &BatchUploadResult{Results: results}
}
