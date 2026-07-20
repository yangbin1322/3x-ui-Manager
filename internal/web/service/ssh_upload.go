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
	"golang.org/x/crypto/ssh"
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

// UploadResult is the outcome of writing the upload (one or more files) to one
// managed server.
type UploadResult struct {
	ServerId   int    `json:"serverId" example:"3"`
	ServerName string `json:"serverName" example:"hk-1"`
	Status     string `json:"status" example:"success"`
	Path       string `json:"path" example:"/root/app.conf"`
	Files      int    `json:"files" example:"1"`
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

// UploadEntry is one file to write. Name is the base file name; Rel is its path
// relative to the chosen upload root (set only when a directory was uploaded),
// used to recreate the tree under dest. Content is the whole file, buffered so
// it can be fanned out to every target.
type UploadEntry struct {
	Name    string
	Rel     string
	Content []byte
}

// uploadToServer opens one SFTP session and writes every entry. With a single
// entry that has no Rel it keeps the original single-file semantics (dest ending
// in "/" is a directory drop, otherwise the full file path). With multiple
// entries or any Rel, dest is treated as a destination directory and each entry
// is written at dest/<rel-or-name>, recreating the uploaded tree. It never
// returns an error: a failure becomes a recorded result so a batch keeps going.
func (s *ManagedServerService) uploadToServer(ctx context.Context, srv *model.ManagedServer, dest string, entries []UploadEntry, timeout time.Duration) *UploadResult {
	out := &UploadResult{ServerId: srv.Id, ServerName: srv.Name}
	started := time.Now()
	defer func() { out.DurationMs = int(time.Since(started).Milliseconds()) }()

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

	done := make(chan error, 1)
	go func() { done <- writeUploadEntries(dial.Client, dest, entries, out) }()

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
	case err := <-done:
		if err != nil {
			out.Status = uploadStatusFailed
			out.Error = err.Error()
			return out
		}
		out.Status = uploadStatusSuccess
		return out
	}
}

// treeUpload reports whether entries should be written as a tree under dest (as
// opposed to the single-file drop): more than one file, or any file carrying a
// relative path from a directory upload.
func treeUpload(entries []UploadEntry) bool {
	if len(entries) > 1 {
		return true
	}
	return len(entries) == 1 && strings.TrimSpace(entries[0].Rel) != ""
}

// safeRel cleans a relative path from the browser so it cannot escape dest. It
// forward-slashes, strips any leading "/" or drive, and drops ".." segments.
func safeRel(rel, name string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	cleaned := path.Clean("/" + rel)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		cleaned = path.Base(path.Clean("/" + strings.ReplaceAll(name, "\\", "/")))
	}
	return cleaned
}

func writeUploadEntries(sshClient *ssh.Client, dest string, entries []UploadEntry, out *UploadResult) error {
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		return err
	}
	defer client.Close()

	if !treeUpload(entries) {
		remotePath, err := resolveRemotePath(dest, entries[0].Name)
		if err != nil {
			return err
		}
		n, err := sftpWriteFile(client, remotePath, entries[0].Content)
		if err != nil {
			return err
		}
		out.Path = remotePath
		out.Files = 1
		out.Bytes = n
		return nil
	}

	root := path.Clean(dest)
	out.Path = root
	if err := client.MkdirAll(root); err != nil {
		return err
	}
	for _, e := range entries {
		rel := safeRel(e.Rel, e.Name)
		target := path.Join(root, rel)
		if dir := path.Dir(target); dir != "" && dir != "." && dir != "/" {
			_ = client.MkdirAll(dir)
		}
		n, err := sftpWriteFile(client, target, e.Content)
		if err != nil {
			return err
		}
		out.Files++
		out.Bytes += n
	}
	return nil
}

// sftpWriteFile creates remotePath (making its parent dir) and writes content,
// returning the byte count.
func sftpWriteFile(client *sftp.Client, remotePath string, content []byte) (int64, error) {
	if dir := path.Dir(remotePath); dir != "" && dir != "." && dir != "/" {
		_ = client.MkdirAll(dir)
	}
	f, err := client.Create(remotePath)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, strings.NewReader(string(content)))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return n, err
}

// BatchUploadResult is the outcome of writing the upload across several servers.
type BatchUploadResult struct {
	Results []UploadResult `json:"results"`
}

// UploadFilesBatch writes every entry to dest on each of serverIds, bounded by
// uploadConcurrency. Content is already buffered, so each target goroutine reads
// its own copy. Results come back in input order. A missing server becomes a
// failed result rather than aborting the batch.
func (s *ManagedServerService) UploadFilesBatch(ctx context.Context, serverIds []int, dest string, entries []UploadEntry, timeout time.Duration) *BatchUploadResult {
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
			results[i] = *s.uploadToServer(ctx, srv, dest, entries, clamped)
		}(i, srv)
	}
	wg.Wait()

	return &BatchUploadResult{Results: results}
}
