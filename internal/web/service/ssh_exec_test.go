package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func execTestServer(t *testing.T, svc *ManagedServerService, host string, port int) *model.ManagedServer {
	t.Helper()
	return execTestServerNamed(t, svc, "exec-server", host, port)
}

func execTestServerNamed(t *testing.T, svc *ManagedServerService, name, host string, port int) *model.ManagedServer {
	t.Helper()
	srv := &model.ManagedServer{
		Name:                name,
		Address:             host,
		SshPort:             port,
		SshUser:             "root",
		SshAuthType:         "password",
		SshPassword:         "s3cret",
		SshHostKeyMode:      "trust",
		AllowPrivateAddress: true,
	}
	if err := svc.Create(srv); err != nil {
		t.Fatalf("create server %q: %v", name, err)
	}
	return srv
}

func lastAudit(t *testing.T, serverId int) *model.CommandExecution {
	t.Helper()
	rec := &model.CommandExecution{}
	if err := database.GetDB().Where("node_id = ?", serverId).
		Order("id desc").First(rec).Error; err != nil {
		t.Fatalf("load audit row: %v", err)
	}
	return rec
}

func TestExecCommandSuccessWritesAudit(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	host, port, _ := startTestSSHServer(t, "root", "s3cret")
	svc := &ManagedServerService{}
	srv := execTestServer(t, svc, host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := svc.ExecCommand(ctx, srv.Id, "echo hello", 0, "admin")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if res.Status != execStatusSuccess || res.ExitCode != 0 {
		t.Fatalf("result = %+v, want success/0", res)
	}
	if !strings.Contains(res.Stdout, "echo hello") {
		t.Fatalf("stdout = %q, want it to echo the command", res.Stdout)
	}

	rec := lastAudit(t, srv.Id)
	if rec.Username != "admin" || rec.Command != "echo hello" || rec.Status != execStatusSuccess {
		t.Fatalf("audit row = %+v, want admin/echo hello/success", rec)
	}
	if rec.ServerName != "exec-server" {
		t.Fatalf("audit NodeName = %q, want snapshot exec-server", rec.ServerName)
	}
}

func TestExecCommandNonZeroExit(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	host, port, _ := startTestSSHServer(t, "root", "s3cret")
	svc := &ManagedServerService{}
	srv := execTestServer(t, svc, host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := svc.ExecCommand(ctx, srv.Id, "fail:now", 0, "admin")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if res.Status != execStatusFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exitCode = %d, want 7 from the remote command", res.ExitCode)
	}
	if lastAudit(t, srv.Id).ExitCode != 7 {
		t.Fatalf("audit did not record the non-zero exit code")
	}
}

func TestExecCommandTruncatesOutput(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	host, port, _ := startTestSSHServer(t, "root", "s3cret")
	svc := &ManagedServerService{}
	srv := execTestServer(t, svc, host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := svc.ExecCommand(ctx, srv.Id, "big:dump", 0, "admin")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if len(res.Stdout) > execOutputCap+64 {
		t.Fatalf("stdout length = %d, want it capped near %d", len(res.Stdout), execOutputCap)
	}
	if !strings.HasSuffix(res.Stdout, "[output truncated]") {
		t.Fatalf("capped output missing truncation marker: %q", res.Stdout[len(res.Stdout)-40:])
	}
}

func TestExecCommandServerNotFound(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := svc.ExecCommand(ctx, 999999, "whoami", 0, "admin")
	if err == nil || !strings.Contains(err.Error(), "server not found") {
		t.Fatalf("ExecCommand on missing server error = %v, want server not found", err)
	}
}

func TestExecCommandStdinEOFDoesNotHang(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	host, port, _ := startTestSSHServer(t, "root", "s3cret")
	svc := &ManagedServerService{}
	srv := execTestServer(t, svc, host, port)

	// A command that waits on stdin must hit EOF and return quickly, not block
	// until the timeout ceiling. Give it a generous timeout but assert it
	// finishes well under that, proving the EOF stdin — not the timeout — ended it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	res, err := svc.ExecCommand(ctx, srv.Id, "readstdin:apt", 25*time.Second, "admin")
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("command waiting on stdin took %v, want it to hit EOF and return quickly", elapsed)
	}
	if res.Status != execStatusSuccess {
		t.Fatalf("status = %q, want success (EOF stdin, not a timeout)", res.Status)
	}
	if !strings.Contains(res.Stdout, "read 0 bytes") {
		t.Fatalf("stdout = %q, want it to report an empty (EOF) stdin", res.Stdout)
	}
}

func TestClampExecTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero defaults", 0, execDefaultTimeout},
		{"negative defaults", -5 * time.Second, execDefaultTimeout},
		{"over ceiling clamps", 30 * time.Minute, execMaxTimeout},
		{"sub-second floors", 200 * time.Millisecond, time.Second},
		{"in range kept", 90 * time.Second, 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampExecTimeout(tt.in); got != tt.want {
				t.Fatalf("clampExecTimeout(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestExecCommandBatchAllSucceed(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	host1, port1, _ := startTestSSHServer(t, "root", "s3cret")
	host2, port2, _ := startTestSSHServer(t, "root", "s3cret")
	svc := &ManagedServerService{}
	s1 := execTestServerNamed(t, svc, "server-a", host1, port1)
	s2 := execTestServerNamed(t, svc, "server-b", host2, port2)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	batch := svc.ExecCommandBatch(ctx, []int{s1.Id, s2.Id}, "echo hi", 0, "admin")

	if batch.BatchId == "" {
		t.Fatal("BatchId is empty, want a shared id")
	}
	if len(batch.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(batch.Results))
	}
	if batch.Results[0].ServerId != s1.Id || batch.Results[1].ServerId != s2.Id {
		t.Fatalf("results out of order: %d, %d", batch.Results[0].ServerId, batch.Results[1].ServerId)
	}
	for _, r := range batch.Results {
		if r.Status != execStatusSuccess {
			t.Fatalf("server %d status = %q, want success", r.ServerId, r.Status)
		}
	}

	var count int64
	database.GetDB().Model(&model.CommandExecution{}).
		Where("batch_id = ?", batch.BatchId).Count(&count)
	if count != 2 {
		t.Fatalf("audit rows for batch = %d, want 2", count)
	}
}

func TestExecCommandBatchMixedOutcomes(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	host, port, _ := startTestSSHServer(t, "root", "s3cret")
	svc := &ManagedServerService{}
	good := execTestServerNamed(t, svc, "reachable", host, port)
	dead := execTestServerNamed(t, svc, "dead", "127.0.0.1", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	batch := svc.ExecCommandBatch(ctx, []int{good.Id, dead.Id, 999999}, "echo hi", 5*time.Second, "admin")

	if len(batch.Results) != 3 {
		t.Fatalf("got %d results, want 3", len(batch.Results))
	}
	if batch.Results[0].Status != execStatusSuccess {
		t.Fatalf("reachable server status = %q, want success", batch.Results[0].Status)
	}
	if batch.Results[1].Status != execStatusUnreachable {
		t.Fatalf("dead server status = %q, want unreachable", batch.Results[1].Status)
	}
	if batch.Results[2].Status != execStatusFailed || batch.Results[2].Error != "server not found" {
		t.Fatalf("missing server result = %+v, want failed/server not found", batch.Results[2])
	}

	var count int64
	database.GetDB().Model(&model.CommandExecution{}).
		Where("batch_id = ?", batch.BatchId).Count(&count)
	if count != 2 {
		t.Fatalf("audit rows for batch = %d, want 2 (missing server not audited)", count)
	}
}
