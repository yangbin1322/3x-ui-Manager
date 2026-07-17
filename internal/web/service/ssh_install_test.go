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

func TestParseInstallResult(t *testing.T) {
	content := `XUI_USERNAME=admin7x
XUI_PASSWORD=s3cretpw
XUI_PANEL_PORT=2096
XUI_WEB_BASE_PATH=abcd
XUI_ACCESS_URL=https://1.2.3.4:2096/abcd/
XUI_API_TOKEN=tok_abc123
XUI_DB_TYPE=sqlite
`
	env, err := parseInstallResult(content)
	if err != nil {
		t.Fatalf("parseInstallResult: %v", err)
	}
	if env.port != 2096 {
		t.Fatalf("port = %d, want 2096", env.port)
	}
	if env.token != "tok_abc123" {
		t.Fatalf("token = %q, want tok_abc123", env.token)
	}
	if env.basePath != "abcd" {
		t.Fatalf("basePath = %q, want abcd", env.basePath)
	}
	if env.scheme != "https" {
		t.Fatalf("scheme = %q, want https", env.scheme)
	}
}

func TestParseInstallResultQuotedAndHttp(t *testing.T) {
	content := "XUI_PANEL_PORT='8080'\nXUI_API_TOKEN=\"quoted_tok\"\nXUI_ACCESS_URL=http://host:8080/p/\n"
	env, err := parseInstallResult(content)
	if err != nil {
		t.Fatalf("parseInstallResult: %v", err)
	}
	if env.port != 8080 || env.token != "quoted_tok" {
		t.Fatalf("quoted values not unquoted: port=%d token=%q", env.port, env.token)
	}
	if env.scheme != "http" {
		t.Fatalf("scheme = %q, want http from the access url", env.scheme)
	}
}

func TestParseInstallResultRejectsIncomplete(t *testing.T) {
	// Missing token.
	if _, err := parseInstallResult("XUI_PANEL_PORT=2053\n"); err == nil {
		t.Fatal("parseInstallResult accepted a result with no api token")
	}
	// Missing port.
	if _, err := parseInstallResult("XUI_API_TOKEN=tok\n"); err == nil {
		t.Fatal("parseInstallResult accepted a result with no panel port")
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
