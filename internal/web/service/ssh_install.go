package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"

	"gorm.io/gorm"
)

const (
	// installTimeout is wider than the command ceiling: downloading and starting
	// 3x-ui can take a few minutes on a slow box, and a install cut short at the
	// 5m command limit would leave a half-installed panel.
	installTimeout = 10 * time.Minute
)

// InstallResult reports the outcome of an auto-install to the panel. Derived
// reports that a panel Node was created from the server and linked to it;
// NodeId is that node's id.
type InstallResult struct {
	Success   bool   `json:"success" example:"true"`
	Derived   bool   `json:"derived" example:"true"`
	NodeId    int    `json:"nodeId,omitempty" example:"7"`
	Message   string `json:"message,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	AccessUrl string `json:"accessUrl,omitempty" example:"https://1.2.3.4:2053/abc/"`
}

// UninstallResult reports the outcome of removing a panel from a managed server.
type UninstallResult struct {
	Success bool   `json:"success" example:"true"`
	Message string `json:"message,omitempty"`
	Stdout  string `json:"stdout,omitempty"`
}

// installEnv holds the fields parsed out of the installer's output that the
// panel needs to adopt the freshly installed node over its API.
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
// unattended (the EOF stdin already makes stdin a non-TTY, but the flag is
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

// InstallPanel installs 3x-ui on a managed server and, on success, derives a
// NEW panel Node from the credentials the installer prints, linking it via the
// server's NodeId. The server row — and with it the SSH access — is left
// untouched: SSH management and panel management are two parallel handles on
// the same box, not successive states of one row.
func (s *ManagedServerService) InstallPanel(ctx context.Context, serverId int, version string, username string) (*InstallResult, error) {
	srv, err := s.GetById(serverId)
	if err != nil || srv == nil {
		return nil, common.NewError("server not found")
	}
	if srv.NodeId != 0 {
		return nil, common.NewError("this server already has a linked panel node")
	}
	// Another row for the same box may have already installed a panel. Don't
	// install again — just adopt the existing node onto this row and its
	// siblings, so the shared machine keeps one node.
	if nodeId := s.linkedNodeForHost(srv); nodeId != 0 {
		if err := s.linkHostToNode(database.GetDB(), srv, nodeId); err != nil {
			return nil, common.NewError(err.Error())
		}
		return &InstallResult{Success: true, Derived: true, NodeId: nodeId}, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	cmd := buildInstallCommand(version)
	res := s.execOnServer(runCtx, srv, cmd, installTimeout)
	s.writeAudit("", srv, "[install 3x-ui]", username, res)

	out := &InstallResult{Stdout: res.Stdout}
	if res.Status != execStatusSuccess {
		out.Message = firstNonEmpty(res.Error, "install failed")
		return out, nil
	}
	out.Success = true

	// Read the panel's credentials and derive the node. A failure here does not
	// undo the install (the panel is up); it just means the operator must add the
	// api node by hand, so it is reported, not fatal.
	env, err := s.readPanelCredentials(runCtx, srv, res.Stdout)
	if err != nil {
		out.Message = "installed, but could not read panel credentials: " + err.Error()
		return out, nil
	}
	out.AccessUrl = env.url
	nodeId, err := s.deriveNode(srv, env)
	if err != nil {
		out.Message = "installed, but creating the panel node failed: " + err.Error()
		return out, nil
	}
	out.Derived = true
	out.NodeId = nodeId
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
func (s *ManagedServerService) readPanelCredentials(ctx context.Context, srv *model.ManagedServer, installStdout string) (*installEnv, error) {
	env := parseAccessURL(installStdout)
	if env == nil {
		return nil, fmt.Errorf("could not find the panel access URL in the install output")
	}
	tokenRes := s.execOnServer(ctx, srv, getApiTokenCommand, sshCommandTimeout)
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

// deriveNode creates the panel Node for a freshly installed server and links it
// back via the server's NodeId, in one transaction so a failed link does not
// leave an orphan node the operator did not ask for. The node reuses the
// server's name when free, otherwise "<name>-panel".
func (s *ManagedServerService) deriveNode(srv *model.ManagedServer, env *installEnv) (int, error) {
	// An install that skipped SSL serves plain HTTP, so pin the verify mode to
	// match the scheme — otherwise the node carries "http + verify", a
	// contradictory config.
	tlsVerifyMode := "verify"
	if env.scheme == "http" {
		tlsVerifyMode = "skip"
	}
	node := &model.Node{
		Name:                srv.Name,
		Remark:              srv.Remark,
		Scheme:              env.scheme,
		Address:             srv.Address,
		Port:                env.port,
		BasePath:            normalizeBasePath(env.basePath),
		ApiToken:            env.token,
		Enable:              true,
		AllowPrivateAddress: srv.AllowPrivateAddress,
		TlsVerifyMode:       tlsVerifyMode,
	}
	nodeService := NodeService{}
	if err := nodeService.normalize(node); err != nil {
		return 0, err
	}
	db := database.GetDB()
	err := db.Transaction(func(tx *gorm.DB) error {
		var clash int64
		if err := tx.Model(model.Node{}).Where("name = ?", node.Name).Count(&clash).Error; err != nil {
			return err
		}
		if clash > 0 {
			node.Name = node.Name + "-panel"
		}
		if err := tx.Create(node).Error; err != nil {
			return err
		}
		// Link this row and every sibling row for the same box, so all the
		// differently-named records for one machine share the derived node.
		return s.linkHostToNode(tx, srv, node.Id)
	})
	if err != nil {
		return 0, err
	}
	return node.Id, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// uninstallCommand runs the official uninstaller non-interactively, then reports
// success by whether the binary is actually gone rather than by the script's own
// exit code. The global `x-ui` management script prompts once
// ("Are you sure… [Default n]") via a `read`, so a "y" is piped in. It must be
// the global x-ui.sh wrapper, NOT /usr/local/x-ui/x-ui: the uninstall deletes
// that binary, so referencing it afterwards runs a missing file and exits 127
// (the false failure the operator hit). The uninstall can also exit non-zero
// from its trailing daemon-reload/menu even on success, so the trailing
// `test ! -e …` makes the command's exit status mean "the panel binary is gone".
const uninstallCommand = "printf 'y\\n' | x-ui uninstall; test ! -e /usr/local/x-ui/x-ui"

// showSettingCommand prints the current panel settings (port, webBasePath, …)
// via the x-ui binary at its install path, the same way install.sh reads an
// existing install's config.
const showSettingCommand = "cd /usr/local/x-ui && ./x-ui setting -show true"

// readInstalledPanelCredentials assembles what an api node needs to adopt a
// panel that is ALREADY installed (the import path), where there is no installer
// "Access URL" line to parse. Port and base path come from `x-ui setting -show`;
// the API token from -getApiToken (minted if absent). The scheme defaults to
// http — a fresh install serves plain HTTP unless the operator set up TLS — and
// is only https when the settings report a web certificate. The operator can
// still correct the derived node from the Panel Nodes tab.
func (s *ManagedServerService) readInstalledPanelCredentials(ctx context.Context, srv *model.ManagedServer) (*installEnv, error) {
	showRes := s.execOnServer(ctx, srv, showSettingCommand, sshCommandTimeout)
	if showRes.Status != execStatusSuccess {
		return nil, fmt.Errorf("could not read the panel settings over SSH")
	}
	port, basePath, hasCert := parseShowSetting(showRes.Stdout)
	if port == 0 {
		return nil, fmt.Errorf("could not determine the panel port from its settings")
	}
	tokenRes := s.execOnServer(ctx, srv, getApiTokenCommand, sshCommandTimeout)
	if tokenRes.Status != execStatusSuccess {
		return nil, fmt.Errorf("could not read the API token from the panel")
	}
	token := parseApiToken(tokenRes.Stdout)
	if token == "" {
		return nil, fmt.Errorf("the panel did not return an API token")
	}
	scheme := "http"
	if hasCert {
		scheme = "https"
	}
	return &installEnv{scheme: scheme, port: port, basePath: basePath, token: token}, nil
}

// parseShowSetting pulls the port, webBasePath, and whether a web TLS
// certificate is configured out of `x-ui setting -show`, whose output has
// "port: <n>", "webBasePath: <path>" and "webCertFile: <path>" lines. ANSI
// codes are stripped in case the binary colorizes. A missing base path yields
// "" (root); a non-empty webCertFile means the panel serves https.
func parseShowSetting(out string) (int, string, bool) {
	var port int
	var basePath string
	var hasCert bool
	for _, line := range strings.Split(stripANSI(out), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "port:"); ok {
			if p, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				port = p
			}
		}
		if rest, ok := strings.CutPrefix(line, "webCertFile:"); ok {
			if strings.TrimSpace(rest) != "" {
				hasCert = true
			}
		}
		if rest, ok := strings.CutPrefix(line, "webBasePath:"); ok {
			basePath = strings.Trim(strings.TrimSpace(rest), "/")
		}
	}
	return port, basePath, hasCert
}

// ImportPanel adopts a panel that is already installed on a managed server:
// it reads the running panel's credentials over SSH and derives a linked Node
// without reinstalling anything. Used when the SSH heartbeat found a panel the
// operator never installed through this UI.
func (s *ManagedServerService) ImportPanel(ctx context.Context, serverId int, username string) (*InstallResult, error) {
	srv, err := s.GetById(serverId)
	if err != nil || srv == nil {
		return nil, common.NewError("server not found")
	}
	if srv.NodeId != 0 {
		return nil, common.NewError("this server already has a linked panel node")
	}
	// A sibling row for the same box may already be linked to a node; adopt it
	// onto this row instead of creating a second node for one machine.
	if nodeId := s.linkedNodeForHost(srv); nodeId != 0 {
		if err := s.linkHostToNode(database.GetDB(), srv, nodeId); err != nil {
			return nil, common.NewError(err.Error())
		}
		return &InstallResult{Success: true, Derived: true, NodeId: nodeId}, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	env, err := s.readInstalledPanelCredentials(runCtx, srv)
	if err != nil {
		return &InstallResult{Message: err.Error()}, nil
	}
	nodeId, err := s.deriveNode(srv, env)
	if err != nil {
		return &InstallResult{Message: "could not create the panel node: " + err.Error()}, nil
	}
	s.writeAudit("", srv, "[import panel]", username, &ExecResult{ServerId: srv.Id, ServerName: srv.Name, Status: execStatusSuccess})
	return &InstallResult{Success: true, Derived: true, NodeId: nodeId}, nil
}

// UninstallPanel runs the official uninstaller on a managed server. If the
// server has a derived node, that node is deleted and the link cleared so the
// panel and its record disappear together. The SSH access (the ManagedServer
// row) is kept, so the box can be reinstalled later.
func (s *ManagedServerService) UninstallPanel(ctx context.Context, serverId int, username string) (*UninstallResult, error) {
	srv, err := s.GetById(serverId)
	if err != nil || srv == nil {
		return nil, common.NewError("server not found")
	}

	runCtx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	res := s.execOnServer(runCtx, srv, uninstallCommand, installTimeout)
	s.writeAudit("", srv, "[uninstall 3x-ui]", username, res)
	out := &UninstallResult{Stdout: res.Stdout}
	if res.Status != execStatusSuccess {
		out.Message = firstNonEmpty(res.Error, "uninstall failed")
		return out, nil
	}
	out.Success = true

	// The panel is gone from the box, so tear down the shared node and reset the
	// panel state on THIS row and every sibling row for the same machine — they
	// all pointed at the same panel. A failure here is reported but not fatal.
	db := database.GetDB()
	if srv.NodeId != 0 {
		if err := (&NodeService{}).Delete(srv.NodeId); err != nil {
			out.Message = "uninstalled, but removing the linked node failed: " + err.Error()
		}
	}
	reset := map[string]any{"panel_installed": false, "panel_version": "", "node_id": 0}
	if err := db.Model(&model.ManagedServer{}).Where("id = ?", srv.Id).Updates(reset).Error; err != nil {
		out.Message = firstNonEmpty(out.Message, "uninstalled, but clearing the panel state failed: "+err.Error())
	}
	if err := sameHostFilter(db, srv).Model(&model.ManagedServer{}).Updates(reset).Error; err != nil {
		out.Message = firstNonEmpty(out.Message, "uninstalled, but clearing sibling servers failed: "+err.Error())
	}
	return out, nil
}

// BatchInstallResult / BatchUninstallResult carry one per-server outcome each so
// the UI can show which servers succeeded and which failed in a bulk action.
type BatchServerResult struct {
	ServerId   int    `json:"serverId" example:"3"`
	ServerName string `json:"serverName" example:"hk-1"`
	Success    bool   `json:"success" example:"true"`
	Message    string `json:"message,omitempty"`
}

type BatchInstallResponse struct {
	Results []BatchServerResult `json:"results"`
}

// InstallPanelBatch installs 3x-ui on each server concurrently, bounded by
// execConcurrency (each install is a heavy, side-effecting action). Results come
// back in the requested order. A server that is missing or already linked
// becomes a failed result rather than aborting the batch.
func (s *ManagedServerService) InstallPanelBatch(ctx context.Context, serverIds []int, version string, username string) *BatchInstallResponse {
	results := make([]BatchServerResult, len(serverIds))
	sem := make(chan struct{}, execConcurrency)
	var wg sync.WaitGroup
	for i, id := range serverIds {
		wg.Add(1)
		sem <- struct{}{}
		go func(i, id int) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := s.InstallPanel(ctx, id, version, username)
			results[i] = installOutcome(id, res, err)
		}(i, id)
	}
	wg.Wait()
	return &BatchInstallResponse{Results: results}
}

// UninstallPanelBatch is the uninstall counterpart of InstallPanelBatch.
func (s *ManagedServerService) UninstallPanelBatch(ctx context.Context, serverIds []int, username string) *BatchInstallResponse {
	results := make([]BatchServerResult, len(serverIds))
	sem := make(chan struct{}, execConcurrency)
	var wg sync.WaitGroup
	for i, id := range serverIds {
		wg.Add(1)
		sem <- struct{}{}
		go func(i, id int) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := s.UninstallPanel(ctx, id, username)
			results[i] = uninstallOutcome(id, res, err)
		}(i, id)
	}
	wg.Wait()
	return &BatchInstallResponse{Results: results}
}

func installOutcome(id int, res *InstallResult, err error) BatchServerResult {
	out := BatchServerResult{ServerId: id}
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.Success = res.Success && res.Derived
	out.Message = res.Message
	return out
}

func uninstallOutcome(id int, res *UninstallResult, err error) BatchServerResult {
	out := BatchServerResult{ServerId: id}
	if err != nil {
		out.Message = err.Error()
		return out
	}
	out.Success = res.Success
	out.Message = res.Message
	return out
}

// panelReleasesURL lists the 3x-ui releases the install script can install.
const panelReleasesURL = "https://api.github.com/repos/MHSanaei/3x-ui/releases"

// panelVersionsTTL bounds how often the version picker hits GitHub.
const panelVersionsTTL = 15 * time.Minute

var (
	panelVersionsMu     sync.Mutex
	panelVersionsCache  []string
	panelVersionsCached time.Time
)

// PanelVersions returns installable 3x-ui release tags, newest first, cached for
// panelVersionsTTL. On a fetch failure a non-empty stale cache is served rather
// than erroring, so the picker keeps working through a transient GitHub blip;
// only a cold-cache failure propagates. The caller always adds a "latest"
// default and free-text entry, so an empty list is still usable.
func (s *ManagedServerService) PanelVersions() ([]string, error) {
	panelVersionsMu.Lock()
	cached, at := panelVersionsCache, panelVersionsCached
	panelVersionsMu.Unlock()
	if cached != nil && time.Since(at) <= panelVersionsTTL {
		return cached, nil
	}

	versions, err := fetchPanelVersions()
	if err != nil {
		if cached != nil {
			logger.Warning("PanelVersions: serving stale list:", err)
			return cached, nil
		}
		return nil, err
	}
	panelVersionsMu.Lock()
	panelVersionsCache, panelVersionsCached = versions, time.Now()
	panelVersionsMu.Unlock()
	return versions, nil
}

func fetchPanelVersions() ([]string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, panelReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&SettingService{}).NewProxiedHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}
	// The installer refuses anything below v2.3.5, so filter to installable tags
	// of the vMAJOR.MINOR.PATCH shape and drop pre-releases/dev tags.
	versions := make([]string, 0, len(releases))
	for _, r := range releases {
		tag := strings.TrimPrefix(r.TagName, "v")
		parts := strings.Split(tag, ".")
		if len(parts) != 3 {
			continue
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			continue
		}
		versions = append(versions, r.TagName)
	}
	return versions, nil
}
