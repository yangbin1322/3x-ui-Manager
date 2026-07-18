package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"

	"gorm.io/gorm"
)

const (
	// installTimeout is wider than the command ceiling: downloading and starting
	// 3x-ui can take a few minutes on a slow box, and a install cut short at the
	// 5m command limit would leave a half-installed panel.
	installTimeout = 10 * time.Minute
)

// InstallResult reports the outcome of an auto-install to the panel.
type InstallResult struct {
	Success   bool   `json:"success" example:"true"`
	Converted bool   `json:"converted" example:"true"`
	Message   string `json:"message,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	AccessUrl string `json:"accessUrl,omitempty" example:"https://1.2.3.4:2053/abc/"`
}

// installEnv holds the fields parsed out of install-result.env that the panel
// needs to adopt the freshly installed node over its API.
type installEnv struct {
	port     int
	basePath string
	scheme   string
	token    string
	url      string
}

// buildInstallCommand assembles the non-interactive install invocation. The
// script is fetched over the network and executed — the same trust model as a
// human running the official one-liner. XUI_NONINTERACTIVE=1 makes it run
// unattended (Phase 4's EOF stdin already makes stdin a non-TTY, but the flag is
// explicit and future-proof).
func buildInstallCommand(version string) string {
	const scriptURL = "https://raw.githubusercontent.com/mhsanaei/3x-ui/master/install.sh"
	inner := fmt.Sprintf("XUI_NONINTERACTIVE=1 bash <(curl -Ls %s)", scriptURL)
	version = strings.TrimSpace(version)
	if version != "" {
		inner += " " + shellQuote(version)
	}
	// Process substitution <(...) is a bash feature; the remote login shell may
	// be sh/dash, so run the whole thing explicitly under bash -c.
	return "bash -c " + shellQuote(inner)
}

// shellQuote wraps a value in single quotes so a pinned version string cannot
// break out of the command. Single quotes inside are handled by the standard
// '\” idiom.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// InstallPanel installs 3x-ui on an ssh-mode node and, on success, converts the
// node to api mode in place using the credentials the installer writes. The SSH
// credentials are kept so the box stays reachable over SSH for later automation.
func (s *NodeService) InstallPanel(ctx context.Context, nodeId int, version string, username string) (*InstallResult, error) {
	n, err := s.GetById(nodeId)
	if err != nil || n == nil {
		return nil, common.NewError("node not found")
	}
	if n.Mode != "ssh" {
		return nil, common.NewError("panel install is only available for ssh-mode nodes")
	}

	runCtx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	cmd := buildInstallCommand(version)
	res := s.execOnNode(runCtx, n, cmd, installTimeout)
	s.writeAudit("", n, "[install 3x-ui]", username, res)

	out := &InstallResult{Stdout: res.Stdout}
	if res.Status != execStatusSuccess {
		out.Message = firstNonEmpty(res.Error, "install failed")
		return out, nil
	}
	out.Success = true

	// Read the panel's credentials and convert the node. A failure here does not
	// undo the install (the panel is up); it just means the operator must add the
	// api node by hand, so it is reported, not fatal.
	env, err := s.readPanelCredentials(runCtx, n, res.Stdout)
	if err != nil {
		out.Message = "installed, but could not read panel credentials: " + err.Error()
		return out, nil
	}
	out.AccessUrl = env.url
	if err := s.convertToApiNode(n.Id, env); err != nil {
		out.Message = "installed, but automatic conversion failed: " + err.Error()
		return out, nil
	}
	out.Converted = true
	return out, nil
}

// accessURLPattern extracts the panel URL the installer prints ("Access URL:
// scheme://host:port/basePath/"). This is the most reliable source of the
// port/path/scheme because the installer always prints it, unlike the optional
// install-result.env file whose write is gated on internal script branches.
var accessURLPattern = regexp.MustCompile(`Access URL:\s*(https?)://[^:/\s]+:(\d+)/([^\s]*)`)

// ansiPattern matches ANSI SGR color escapes (e.g. "\x1b[0;32m", "\x1b[0m").
// The installer colorizes the Access URL line, so a reset code "\x1b[0m" trails
// the URL; without stripping it, the [^\s]* base-path capture swallows the escape
// and the panel URL ends up containing it, 404ing.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// getApiTokenCommand reads (minting if needed) the panel's API token. It calls
// the x-ui BINARY at its install path, not the global `x-ui` wrapper: the global
// command is the management shell script (x-ui.sh), which has no `setting`
// subcommand and would silently do nothing. The binary must run from its own
// directory so it finds the panel database, matching how install.sh calls it.
const getApiTokenCommand = "cd /usr/local/x-ui && ./x-ui setting -getApiToken true"

// readPanelCredentials assembles what an api node needs after an install. The
// port/path/scheme come from the installer's own "Access URL" line (always
// printed); the API token comes from the x-ui binary's -getApiToken, the same
// command the installer uses — it mints one if none exists. This does not rely
// on /etc/x-ui/install-result.env, which is only written on some install paths.
func (s *NodeService) readPanelCredentials(ctx context.Context, n *model.Node, installStdout string) (*installEnv, error) {
	env := parseAccessURL(installStdout)
	if env == nil {
		return nil, fmt.Errorf("could not find the panel access URL in the install output")
	}
	tokenRes := s.execOnNode(ctx, n, getApiTokenCommand, sshCommandTimeout)
	if tokenRes.Status != execStatusSuccess {
		return nil, fmt.Errorf("could not read the API token from the panel")
	}
	token := parseApiToken(tokenRes.Stdout)
	if token == "" {
		return nil, fmt.Errorf("the panel did not return an API token")
	}
	env.token = token
	return env, nil
}

// parseAccessURL pulls scheme/port/basePath out of the installer's Access URL
// line. Returns nil if the line is absent.
func parseAccessURL(stdout string) *installEnv {
	// Strip ANSI color codes first: the installer colorizes the line, so the URL
	// is wrapped in escapes ("\x1b[0;32m…\x1b[0m") that would otherwise be
	// captured into the base path (no whitespace separates them).
	m := accessURLPattern.FindStringSubmatch(stripANSI(stdout))
	if m == nil {
		return nil
	}
	port, err := strconv.Atoi(m[2])
	if err != nil || port == 0 {
		return nil
	}
	return &installEnv{
		scheme: m[1],
		port:   port,
		// Strip all leading/trailing slashes to a bare segment; normalizeBasePath
		// re-adds exactly one on each side.
		basePath: strings.Trim(m[3], "/"),
		url:      m[0][len("Access URL:"):],
	}
}

// parseApiToken pulls the token out of `x-ui setting -getApiToken true`, whose
// output line is "apiToken: <token>". ANSI codes are stripped first in case the
// binary colorizes its output, so an escape can't end up in the token.
func parseApiToken(stdout string) string {
	for _, line := range strings.Split(stripANSI(stdout), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "apiToken:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// convertToApiNode flips the node from ssh to api mode, filling the api fields
// from the install result. The SSH credentials are left in place so the box
// stays SSH-reachable for later automation; only the access mode changes.
func (s *NodeService) convertToApiNode(id int, env *installEnv) error {
	// An install that skipped SSL serves plain HTTP, so pin the verify mode to
	// match the scheme — otherwise the node carries "http + verify", a
	// contradictory config the SSH-mode default left behind.
	tlsVerifyMode := "verify"
	if env.scheme == "http" {
		tlsVerifyMode = "skip"
	}
	updates := map[string]any{
		"mode":            "api",
		"scheme":          env.scheme,
		"port":            env.port,
		"base_path":       normalizeBasePath(env.basePath),
		"api_token":       env.token,
		"tls_verify_mode": tlsVerifyMode,
		"status":          "unknown",
		"last_error":      "",
	}
	db := database.GetDB()
	if err := db.Transaction(func(tx *gorm.DB) error {
		return tx.Model(model.Node{}).Where("id = ?", id).Updates(updates).Error
	}); err != nil {
		return err
	}
	if mgr := runtime.GetManager(); mgr != nil {
		mgr.InvalidateNode(id)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
