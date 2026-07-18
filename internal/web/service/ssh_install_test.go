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
	// The REAL installer line is ANSI-colored: green start code, reset "\x1b[0m"
	// trailing the URL. The reset code must not end up in the base path (the
	// bug that produced a 404 on the converted node).
	stdout := "Migration done!\n\x1b[0;32mAccess URL:  http://38.207.132.48:1322/uJlwUiLpIMe41EwJv7/\x1b[0m\nx-ui v3.5.0 installation finished\n"
	env := parseAccessURL(stdout)
	if env == nil {
		t.Fatal("parseAccessURL returned nil for a valid access url line")
	}
	if env.scheme != "http" || env.port != 1322 {
		t.Fatalf("scheme/port = %q/%d, want http/1322", env.scheme, env.port)
	}
	if env.basePath != "uJlwUiLpIMe41EwJv7" {
		t.Fatalf("basePath = %q, want uJlwUiLpIMe41EwJv7 (no ANSI, no slashes)", env.basePath)
	}
	if strings.Contains(normalizeBasePath(env.basePath), "\x1b") {
		t.Fatalf("normalized base path still contains an ANSI escape: %q", normalizeBasePath(env.basePath))
	}
}

func TestParseApiTokenStripsANSI(t *testing.T) {
	// Guard the token path against the same ANSI contamination.
	out := "\x1b[0;32mapiToken: tok_abc123\x1b[0m\n"
	if got := parseApiToken(out); got != "tok_abc123" {
		t.Fatalf("parseApiToken with ANSI = %q, want tok_abc123", got)
	}
}

func TestParseAccessURLHttps(t *testing.T) {
	env := parseAccessURL("Access URL: https://example.com:2053/panel/\n")
	if env == nil || env.scheme != "https" || env.port != 2053 || env.basePath != "panel" {
		t.Fatalf("https parse = %+v, want https/2053/panel", env)
	}
}

func TestParseAccessURLStripsExtraSlashes(t *testing.T) {
	// If the installer prints a trailing "//", the base path must still come out
	// as a bare segment so normalizeBasePath produces a single-slashed path and
	// the panel URL doesn't 404 on a double slash.
	cases := []struct{ url, want string }{
		{"Access URL: http://h:3518/abc/\n", "abc"},
		{"Access URL: http://h:3518/abc//\n", "abc"},
		{"Access URL: http://h:3518/abc\n", "abc"},
	}
	for _, c := range cases {
		env := parseAccessURL(c.url)
		if env == nil {
			t.Fatalf("parseAccessURL(%q) = nil", c.url)
		}
		if env.basePath != c.want {
			t.Fatalf("parseAccessURL(%q) basePath = %q, want %q", c.url, env.basePath, c.want)
		}
		if got := normalizeBasePath(env.basePath); got != "/abc/" {
			t.Fatalf("normalized basePath for %q = %q, want /abc/", c.url, got)
		}
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

func installTestServer(t *testing.T, svc *ManagedServerService, name string) *model.ManagedServer {
	t.Helper()
	srv := &model.ManagedServer{
		Name:        name,
		Address:     "203.0.113.40",
		SshUser:     "root",
		SshAuthType: "password",
		SshPassword: "pw",
	}
	if err := svc.Create(srv); err != nil {
		t.Fatalf("create server: %v", err)
	}
	return srv
}

func TestDeriveNode(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}
	srv := installTestServer(t, svc, "to-derive")

	env := &installEnv{port: 2096, basePath: "abcd", scheme: "https", token: "tok_x"}
	nodeId, err := svc.deriveNode(srv, env)
	if err != nil {
		t.Fatalf("deriveNode: %v", err)
	}
	if nodeId == 0 {
		t.Fatalf("deriveNode returned node id 0")
	}

	node, err := (&NodeService{}).GetById(nodeId)
	if err != nil {
		t.Fatalf("get derived node: %v", err)
	}
	if node.Name != "to-derive" {
		t.Fatalf("derived node name = %q, want the server's name", node.Name)
	}
	if node.Address != srv.Address {
		t.Fatalf("derived node address = %q, want %q", node.Address, srv.Address)
	}
	if node.Port != 2096 || node.ApiToken != "tok_x" || node.Scheme != "https" {
		t.Fatalf("api fields not filled: port=%d token=%q scheme=%q", node.Port, node.ApiToken, node.Scheme)
	}
	if node.BasePath != "/abcd/" {
		t.Fatalf("BasePath = %q, want /abcd/ (single-slashed)", node.BasePath)
	}
	if node.TlsVerifyMode != "verify" {
		t.Fatalf("TlsVerifyMode = %q, want verify for an https install", node.TlsVerifyMode)
	}

	// The server keeps its SSH identity and now points at the derived node.
	after, err := svc.GetById(srv.Id)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if after.NodeId != nodeId {
		t.Fatalf("server NodeId = %d, want link to derived node %d", after.NodeId, nodeId)
	}
	if after.SshPassword == "" {
		t.Fatalf("SSH password was cleared on derive, want it kept")
	}
}

func TestDeriveNodeHttpSkipsTlsVerify(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}
	srv := installTestServer(t, svc, "http-derive")
	// A skipped-SSL install serves plain HTTP; the node must not carry a
	// contradictory "http + verify" config.
	env := &installEnv{port: 1322, basePath: "p", scheme: "http", token: "t"}
	nodeId, err := svc.deriveNode(srv, env)
	if err != nil {
		t.Fatalf("deriveNode: %v", err)
	}
	node, _ := (&NodeService{}).GetById(nodeId)
	if node.Scheme != "http" || node.TlsVerifyMode != "skip" {
		t.Fatalf("http install => scheme=%q tls=%q, want http/skip", node.Scheme, node.TlsVerifyMode)
	}
}

func TestDeriveNodeNameCollisionFallsBack(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	nodeSvc := &NodeService{}
	taken := &model.Node{Name: "shared-name", Address: "node.example.com", Port: 2053, ApiToken: "tok"}
	if err := nodeSvc.Create(taken); err != nil {
		t.Fatalf("create existing node: %v", err)
	}
	svc := &ManagedServerService{}
	srv := installTestServer(t, svc, "shared-name")

	env := &installEnv{port: 2096, basePath: "p", scheme: "https", token: "tok_y"}
	nodeId, err := svc.deriveNode(srv, env)
	if err != nil {
		t.Fatalf("deriveNode with name collision: %v", err)
	}
	node, _ := nodeSvc.GetById(nodeId)
	if node.Name != "shared-name-panel" {
		t.Fatalf("derived node name = %q, want shared-name-panel on collision", node.Name)
	}
}

func TestInstallPanelRejectsLinkedServer(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}
	srv := installTestServer(t, svc, "already-linked")
	if err := database.GetDB().Model(&model.ManagedServer{}).Where("id = ?", srv.Id).
		Update("node_id", 42).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}
	_, err := svc.InstallPanel(t.Context(), srv.Id, "", "admin")
	if err == nil || !strings.Contains(err.Error(), "already has a linked panel node") {
		t.Fatalf("InstallPanel on linked server error = %v, want already-linked rejection", err)
	}
}
