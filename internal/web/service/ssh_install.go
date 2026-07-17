package service

import (
	"context"
	"fmt"
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

	// installResultPath is where install.sh's write_install_result persists the
	// panel credentials (root-only, mode 600), including the API token an
	// api-mode node needs.
	installResultPath = "/etc/x-ui/install-result.env"
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

	// Read the credentials the installer wrote and convert the node. A failure
	// here does not undo the install (the panel is up); it just means the
	// operator must add the api node by hand, so it is reported, not fatal.
	env, err := s.readInstallResult(runCtx, n)
	if err != nil {
		out.Message = "installed, but could not read install result: " + err.Error()
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

// readInstallResult cats the installer's credentials file and parses it.
func (s *NodeService) readInstallResult(ctx context.Context, n *model.Node) (*installEnv, error) {
	res := s.execOnNode(ctx, n, "cat "+installResultPath, sshCommandTimeout)
	if res.Status != execStatusSuccess {
		return nil, fmt.Errorf("%s not found", installResultPath)
	}
	return parseInstallResult(res.Stdout)
}

// parseInstallResult reads the KEY=value lines the installer writes. Values are
// shell-quoted with printf %q; for the alphanumeric randoms the installer emits
// this is a no-op, but surrounding single/double quotes are stripped defensively.
func parseInstallResult(content string) (*installEnv, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		fields[key] = unquoteEnv(strings.TrimSpace(value))
	}
	env := &installEnv{
		basePath: fields["XUI_WEB_BASE_PATH"],
		token:    fields["XUI_API_TOKEN"],
		url:      fields["XUI_ACCESS_URL"],
	}
	if p, err := strconv.Atoi(fields["XUI_PANEL_PORT"]); err == nil {
		env.port = p
	}
	env.scheme = "https"
	if strings.HasPrefix(env.url, "http://") {
		env.scheme = "http"
	}
	if env.port == 0 || env.token == "" {
		return nil, fmt.Errorf("install result missing panel port or api token")
	}
	return env, nil
}

func unquoteEnv(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// convertToApiNode flips the node from ssh to api mode, filling the api fields
// from the install result. The SSH credentials are left in place so the box
// stays SSH-reachable for later automation; only the access mode changes.
func (s *NodeService) convertToApiNode(id int, env *installEnv) error {
	updates := map[string]any{
		"mode":       "api",
		"scheme":     env.scheme,
		"port":       env.port,
		"base_path":  normalizeBasePath(env.basePath),
		"api_token":  env.token,
		"status":     "unknown",
		"last_error": "",
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
