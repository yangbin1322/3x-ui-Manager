package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	// copyDefaultTimeout / copyMaxTimeout bound one source→target copy. A tree
	// copy can move many files, so the ceiling is generous but finite.
	copyDefaultTimeout = 5 * time.Minute
	copyMaxTimeout     = 60 * time.Minute

	// copyConcurrency caps how many targets a copy fans out to at once, matching
	// the exec/upload batch model.
	copyConcurrency = 8

	// copyMaxTreeBytes caps the total size of a copied tree so a mistaken copy of
	// a huge directory cannot exhaust the panel host's temp disk. Individual
	// files above this are rejected too.
	copyMaxTreeBytes = 2 << 30
)

// CopyResult is the outcome of copying the source path to one target server.
type CopyResult struct {
	ServerId   int    `json:"serverId" example:"5"`
	ServerName string `json:"serverName" example:"hk-2"`
	Status     string `json:"status" example:"success"`
	Path       string `json:"path" example:"/opt/app"`
	Files      int    `json:"files" example:"12"`
	Bytes      int64  `json:"bytes" example:"1048576"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"durationMs" example:"3200"`
}

// copy status values reuse the exec vocabulary.
const (
	copyStatusSuccess     = execStatusSuccess
	copyStatusFailed      = execStatusFailed
	copyStatusUnreachable = execStatusUnreachable
	copyStatusTimeout     = execStatusTimeout
)

func clampCopyTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return copyDefaultTimeout
	}
	if d > copyMaxTimeout {
		return copyMaxTimeout
	}
	if d < time.Second {
		return time.Second
	}
	return d
}

// stagedEntry is one file pulled from the source, buffered on the panel host's
// temp disk, ready to be pushed to every target. rel is its path relative to the
// source root (empty for a single-file source).
type stagedEntry struct {
	rel      string
	tempPath string
	size     int64
	mode     os.FileMode
}

// copyStage is the source tree materialized on the panel host: a list of files
// (as temp paths) plus the directory skeleton, so targets can be rebuilt without
// re-reading the source per target. isDir reports whether the source root was a
// directory (destination is then a directory to receive the tree) or a file.
type copyStage struct {
	isDir   bool
	dirs    []string
	entries []stagedEntry
	bytes   int64
}

func (st *copyStage) cleanup() {
	for _, e := range st.entries {
		_ = os.Remove(e.tempPath)
	}
}

// stageSource connects to the source server and pulls the path into a temp
// staging area on the panel host. A single file yields one entry with an empty
// rel; a directory yields the full tree (dirs + files) with rels relative to the
// source root. The whole tree is size-capped to protect the panel's temp disk.
func (s *ManagedServerService) stageSource(ctx context.Context, src *model.ManagedServer, srcPath string) (*copyStage, error) {
	ssh := SSHService{}
	dial, err := ssh.Dial(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("source unreachable: %w", err)
	}
	defer dial.Client.Close()

	client, err := sftp.NewClient(dial.Client)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	info, err := client.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("source path not found: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "xui-copy-")
	if err != nil {
		return nil, err
	}
	stage := &copyStage{isDir: info.IsDir()}

	if !info.IsDir() {
		entry, err := stageOneFile(ctx, client, srcPath, "", tempDir, stage)
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, err
		}
		stage.entries = append(stage.entries, entry)
		return stage, nil
	}

	walker := client.Walk(srcPath)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			stage.cleanup()
			_ = os.RemoveAll(tempDir)
			return nil, err
		}
		full := walker.Path()
		rel := relativeTo(srcPath, full)
		st := walker.Stat()
		if st.IsDir() {
			if rel != "" {
				stage.dirs = append(stage.dirs, rel)
			}
			continue
		}
		if !st.Mode().IsRegular() {
			continue
		}
		entry, err := stageOneFile(ctx, client, full, rel, tempDir, stage)
		if err != nil {
			stage.cleanup()
			_ = os.RemoveAll(tempDir)
			return nil, err
		}
		stage.entries = append(stage.entries, entry)
	}
	return stage, nil
}

// stageOneFile pulls one remote file into the temp dir, enforcing the tree size
// cap as it goes.
func stageOneFile(ctx context.Context, client *sftp.Client, remotePath, rel, tempDir string, stage *copyStage) (stagedEntry, error) {
	rf, err := client.Open(remotePath)
	if err != nil {
		return stagedEntry{}, err
	}
	defer rf.Close()

	tempFile, err := os.CreateTemp(tempDir, "f-")
	if err != nil {
		return stagedEntry{}, err
	}
	remaining := copyMaxTreeBytes - stage.bytes
	n, err := io.Copy(tempFile, io.LimitReader(rf, remaining+1))
	closeErr := tempFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tempFile.Name())
		return stagedEntry{}, err
	}
	if n > remaining {
		_ = os.Remove(tempFile.Name())
		return stagedEntry{}, fmt.Errorf("source exceeds the %d MiB copy limit", copyMaxTreeBytes>>20)
	}
	stage.bytes += n

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(tempFile.Name()); statErr == nil {
		mode = info.Mode()
	}
	return stagedEntry{rel: rel, tempPath: tempFile.Name(), size: n, mode: mode}, nil
}

// pushStageToTarget rebuilds the staged tree on one target server under dest.
// For a single-file source, dest follows the same rule as upload (trailing "/"
// = directory drop under the original name). For a directory source, dest is the
// destination directory and the tree is recreated beneath it.
func (s *ManagedServerService) pushStageToTarget(ctx context.Context, target *model.ManagedServer, stage *copyStage, srcPath, dest string, timeout time.Duration) *CopyResult {
	out := &CopyResult{ServerId: target.Id, ServerName: target.Name}
	started := time.Now()
	defer func() { out.DurationMs = int(time.Since(started).Milliseconds()) }()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ssh := SSHService{}
	dial, err := ssh.Dial(runCtx, target)
	if err != nil {
		out.Status = copyStatusUnreachable
		out.Error = err.Error()
		return out
	}
	defer dial.Client.Close()

	done := make(chan error, 1)
	go func() { done <- s.writeStage(dial.Client, stage, srcPath, dest, out) }()

	select {
	case <-runCtx.Done():
		if runCtx.Err() == context.DeadlineExceeded {
			out.Status = copyStatusTimeout
			out.Error = "copy timed out"
		} else {
			out.Status = copyStatusFailed
			out.Error = runCtx.Err().Error()
		}
		return out
	case err := <-done:
		if err != nil {
			out.Status = copyStatusFailed
			out.Error = err.Error()
			return out
		}
		out.Status = copyStatusSuccess
		return out
	}
}

func (s *ManagedServerService) writeStage(sshClient *ssh.Client, stage *copyStage, srcPath, dest string, out *CopyResult) error {
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		return err
	}
	defer client.Close()

	if !stage.isDir {
		remotePath, err := resolveRemotePath(dest, path.Base(srcPath))
		if err != nil {
			return err
		}
		if err := writeOneFile(client, stage.entries[0].tempPath, remotePath); err != nil {
			return err
		}
		out.Path = remotePath
		out.Files = 1
		out.Bytes = stage.entries[0].size
		return nil
	}

	root := path.Clean(dest)
	out.Path = root
	if err := client.MkdirAll(root); err != nil {
		return err
	}
	for _, d := range stage.dirs {
		if err := client.MkdirAll(path.Join(root, d)); err != nil {
			return err
		}
	}
	for _, e := range stage.entries {
		target := path.Join(root, e.rel)
		if dir := path.Dir(target); dir != "" && dir != "." && dir != "/" {
			_ = client.MkdirAll(dir)
		}
		if err := writeOneFile(client, e.tempPath, target); err != nil {
			return err
		}
		out.Files++
		out.Bytes += e.size
	}
	return nil
}

func writeOneFile(client *sftp.Client, localPath, remotePath string) error {
	lf, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer lf.Close()
	if dir := path.Dir(remotePath); dir != "" && dir != "." && dir != "/" {
		_ = client.MkdirAll(dir)
	}
	rf, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	_, err = io.Copy(rf, lf)
	closeErr := rf.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

// relativeTo returns full relative to root using slash paths. It assumes full is
// root or under it (SFTP Walk guarantees this), returning "" for the root itself.
func relativeTo(root, full string) string {
	root = path.Clean(root)
	full = path.Clean(full)
	if full == root {
		return ""
	}
	rel := full[len(root):]
	for len(rel) > 0 && rel[0] == '/' {
		rel = rel[1:]
	}
	return rel
}

// CopyPathBatch stages the source path once from src and pushes it to every
// target concurrently. Staging happens under the whole request; each target push
// gets its own clamped timeout. The stage is cleaned up when done.
func (s *ManagedServerService) CopyPathBatch(ctx context.Context, srcId int, srcPath string, targetIds []int, dest string, timeout time.Duration) (*BatchCopyResult, error) {
	src, err := s.GetById(srcId)
	if err != nil || src == nil {
		return nil, fmt.Errorf("source server not found")
	}
	if srcPath == "" {
		return nil, fmt.Errorf("source path is required")
	}
	if dest == "" {
		return nil, fmt.Errorf("destination path is required")
	}

	stage, err := s.stageSource(ctx, src, srcPath)
	if err != nil {
		return nil, err
	}
	defer stage.cleanup()

	clamped := clampCopyTimeout(timeout)
	results := make([]CopyResult, len(targetIds))
	sem := make(chan struct{}, copyConcurrency)
	var wg sync.WaitGroup
	for i, id := range targetIds {
		target, err := s.GetById(id)
		if err != nil || target == nil {
			results[i] = CopyResult{ServerId: id, Status: copyStatusFailed, Error: "server not found"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, target *model.ManagedServer) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = *s.pushStageToTarget(ctx, target, stage, srcPath, dest, clamped)
		}(i, target)
	}
	wg.Wait()

	return &BatchCopyResult{Results: results}, nil
}

// BatchCopyResult is the outcome of copying one source path to several targets.
type BatchCopyResult struct {
	Results []CopyResult `json:"results"`
}
