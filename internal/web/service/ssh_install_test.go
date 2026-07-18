package service

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestBuildInstallCommand(t *testing.T) {
	// No version pins to latest; the command must run under bash (process
	// substitution) and be non-interactive.
	latest := buildInstallCommand("")
	if !strings.HasPrefix(latest, "bash -c ") {
		t.Fatalf("command must run under bash -c, got %q", latest)
	}
	if !strings.Contains(latest, "XUI_NONINTERACTIVE=1") {
		t.Fatalf("command must be non-interactive, got %q", latest)
	}
	if !strings.Contains(latest, "install.sh") {
		t.Fatalf("command must fetch install.sh, got %q", latest)
	}

	// A pinned version must be shell-quoted so it cannot break out of the command.
	pinned := buildInstallCommand("v3.4.0; rm -rf /")
	if !strings.Contains(pinned, `'v3.4.0; rm -rf /'`) {
		t.Fatalf("pinned version not shell-quoted safely: %q", pinned)
	}
}

func TestParseAccessURL(t *testing.T) {
	// A realistic installer tail, including the ANSI-colored Access URL line.
	stdout := "Migration done!\nAccess URL:  http://38.207.132.48:1322/uJlwUiLpIMe41EwJv7/\nx-ui v3.5.0 installation finished\n"
	env := parseAccessURL(stdout)
	if env == nil {
		t.Fatal("parseAccessURL returned nil for a valid access url line")
	}
	if env.scheme != "http" || env.port != 1322 {
		t.Fatalf("scheme/port = %q/%d, want http/1322", env.scheme, env.port)
	}
	if env.basePath != "uJlwUiLpIMe41EwJv7" {
		t.Fatalf("basePath = %q, want uJlwUiLpIMe41EwJv7 (trailing slash trimmed)", env.basePath)
	}
}

func TestParseAccessURLHttps(t *testing.T) {
	env := parseAccessURL("Access URL: https://example.com:2053/panel/\n")
	if env == nil || env.scheme != "https" || env.port != 2053 || env.basePath != "panel" {
		t.Fatalf("https parse = %+v, want https/2053/panel", env)
	}
}

func TestParseAccessURLAbsent(t *testing.T) {
	if env := parseAccessURL("no url here\n"); env != nil {
		t.Fatalf("parseAccessURL = %+v, want nil when no access url present", env)
	}
}

func TestGetApiTokenCommandUsesBinaryPath(t *testing.T) {
	// Must call the x-ui BINARY at its install dir, not the global `x-ui`
	// wrapper (which is the management script with no `setting` subcommand), and
	// must cd there so the binary finds the panel database.
	if !strings.Contains(getApiTokenCommand, "/usr/local/x-ui") {
		t.Fatalf("token command must use the binary install path, got %q", getApiTokenCommand)
	}
	if !strings.Contains(getApiTokenCommand, "-getApiToken true") {
		t.Fatalf("token command must call -getApiToken, got %q", getApiTokenCommand)
	}
	if !strings.HasPrefix(getApiTokenCommand, "cd ") {
		t.Fatalf("token command must cd into the install dir first, got %q", getApiTokenCommand)
	}
}

func TestParseApiToken(t *testing.T) {
	out := "There are 0 API token(s).\napiToken: tok_abc123\n"
	if got := parseApiToken(out); got != "tok_abc123" {
		t.Fatalf("parseApiToken = %q, want tok_abc123", got)
	}
	if got := parseApiToken("no token line\n"); got != "" {
		t.Fatalf("parseApiToken = %q, want empty when absent", got)
	}
}

func TestConvertToApiNode(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &NodeService{}

	n := &model.Node{
		Mode:        "ssh",
		Name:        "to-convert",
		Address:     "203.0.113.40",
		SshUser:     "root",
		SshAuthType: "password",
		SshPassword: "pw",
		Status:      "reachable",
	}
	if err := svc.Create(n); err != nil {
		t.Fatalf("create: %v", err)
	}

	env := &installEnv{port: 2096, basePath: "abcd", scheme: "https", token: "tok_x"}
	if err := svc.convertToApiNode(n.Id, env); err != nil {
		t.Fatalf("convertToApiNode: %v", err)
	}

	got, err := svc.GetById(n.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Mode != "api" {
		t.Fatalf("Mode = %q, want api", got.Mode)
	}
	if got.Port != 2096 || got.ApiToken != "tok_x" || got.Scheme != "https" {
		t.Fatalf("api fields not filled: port=%d token=%q scheme=%q", got.Port, got.ApiToken, got.Scheme)
	}
	// SSH credentials must be kept so the box stays SSH-reachable.
	if got.SshPassword == "" {
		t.Fatalf("SSH password was cleared on conversion, want it kept")
	}
	// Status reset so the heartbeat re-evaluates it as an api node.
	if got.Status != "unknown" {
		t.Fatalf("Status = %q, want unknown after conversion", got.Status)
	}
}

func TestInstallPanelRejectsApiNode(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &NodeService{}
	apiNode := &model.Node{Name: "already-api", Address: "node.example.com", Port: 2053, ApiToken: "tok"}
	if err := svc.Create(apiNode); err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = database.GetDB()
	_, err := svc.InstallPanel(t.Context(), apiNode.Id, "", "admin")
	if err == nil || !strings.Contains(err.Error(), "only available for ssh-mode") {
		t.Fatalf("InstallPanel on api node error = %v, want ssh-mode-only", err)
	}
}
