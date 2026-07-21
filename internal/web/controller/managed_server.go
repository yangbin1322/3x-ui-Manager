package controller

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"

	"github.com/gin-gonic/gin"
)

// execRequestBudget bounds the whole exec HTTP request. It sits above the
// command timeout ceiling (5m) to leave room for the SSH dial and audit write,
// so the request context isn't what cuts a still-running command short.
const execRequestBudget = 6 * time.Minute

// installRequestBudget sits above the service's 10m install timeout to leave
// room for reading the install result and deriving the panel node afterward.
const installRequestBudget = 12 * time.Minute

// uploadRequestBudget bounds the whole upload HTTP request; it sits above the
// per-server upload timeout ceiling (30m) so the request context isn't what
// cuts a still-running transfer short.
const uploadRequestBudget = 32 * time.Minute

// uploadMaxFileSize caps the total size of one upload (across all files) at 1
// GiB. Files are buffered in memory to be fanned out to every target server, so
// an unbounded upload would be a trivial memory-exhaustion vector.
const uploadMaxFileSize = 1 << 30

// copyRequestBudget bounds the whole server-to-server copy request. It must
// cover staging the source tree onto the panel host plus the per-target push
// ceiling (60m), so it sits above both.
const copyRequestBudget = 65 * time.Minute

type ManagedServerController struct {
	serverService service.ManagedServerService
}

func NewManagedServerController(g *gin.RouterGroup) *ManagedServerController {
	a := &ManagedServerController{}
	a.initRouter(g)
	return a
}

func (a *ManagedServerController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.list)
	g.GET("/get/:id", a.get)

	g.POST("/add", a.add)
	g.POST("/addBatch", a.addBatch)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/delBatch", a.delBatch)
	g.POST("/setEnable/:id", a.setEnable)

	g.POST("/test", a.test)
	g.POST("/exec", a.exec)
	g.POST("/upload", a.upload)
	g.POST("/copyPath", a.copyPath)
	g.POST("/install", a.install)
	g.POST("/installBatch", a.installBatch)
	g.POST("/import", a.importPanel)
	g.POST("/uninstall", a.uninstall)
	g.POST("/uninstallBatch", a.uninstallBatch)
	g.GET("/panelVersions", a.panelVersions)
	g.GET("/execHistory", a.execHistory)
	g.POST("/execHistory/prune", a.pruneExecHistory)
}

func (a *ManagedServerController) list(c *gin.Context) {
	servers, err := a.serverService.GetAll()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.list"), err)
		return
	}
	jsonObj(c, servers, nil)
}

func (a *ManagedServerController) get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	srv, err := a.serverService.GetById(id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.obtain"), err)
		return
	}
	jsonObj(c, srv, nil)
}

func (a *ManagedServerController) add(c *gin.Context) {
	srv, ok := middleware.BindAndValidate[model.ManagedServer](c)
	if !ok {
		return
	}
	if err := a.serverService.Create(srv); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.add"), err)
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.nodes.toasts.add"), srv, nil)
}

// bulkAddRow is the JSON shape of one server in a batch add. It cannot bind
// straight onto model.ManagedServer: the credential fields there are json:"-"
// (write-only, so JSON is never deserialized into them), which would silently
// drop every password/key and fail validation with "ssh password is required".
// So the credentials are named explicitly here and copied into the model.
type bulkAddRow struct {
	Name             string `json:"name"`
	Remark           string `json:"remark"`
	Address          string `json:"address"`
	SshPort          int    `json:"sshPort"`
	SshUser          string `json:"sshUser"`
	SshAuthType      string `json:"sshAuthType"`
	SshPassword      string `json:"sshPassword"`
	SshPrivateKey    string `json:"sshPrivateKey"`
	SshKeyPassphrase string `json:"sshKeyPassphrase"`
	SshHostKeyMode   string `json:"sshHostKeyMode"`
	SshHostKeySha256 string `json:"sshHostKeySha256"`
}

// addBatch registers several managed servers in one request. Each row is
// validated and created independently; the response reports per-row outcomes in
// the input order so the operator can fix only the rows that failed.
func (a *ManagedServerController) addBatch(c *gin.Context) {
	var req struct {
		Servers []bulkAddRow `json:"servers"`
		// Verify defaults to true (matching the UI default): only rows whose SSH
		// test connection succeeds are created. Send verify=false to import
		// without a connection test.
		Verify *bool `json:"verify"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.add"), err)
		return
	}
	if len(req.Servers) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.add"), fmt.Errorf("at least one server is required"))
		return
	}
	verify := req.Verify == nil || *req.Verify
	servers := make([]*model.ManagedServer, len(req.Servers))
	for i, r := range req.Servers {
		servers[i] = &model.ManagedServer{
			Name:             r.Name,
			Remark:           r.Remark,
			Address:          r.Address,
			Enable:           true,
			SshPort:          r.SshPort,
			SshUser:          r.SshUser,
			SshAuthType:      r.SshAuthType,
			SshPassword:      r.SshPassword,
			SshPrivateKey:    r.SshPrivateKey,
			SshKeyPassphrase: r.SshKeyPassphrase,
			SshHostKeyMode:   r.SshHostKeyMode,
			SshHostKeySha256: r.SshHostKeySha256,
		}
	}
	// A verified add opens an SSH connection per row (bounded internally), so it
	// needs a wider budget than a plain DB insert.
	ctx, cancel := context.WithTimeout(c.Request.Context(), execRequestBudget)
	defer cancel()
	result := a.serverService.CreateBatch(ctx, servers, verify)
	jsonObj(c, result, nil)
}

func (a *ManagedServerController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	srv, ok := middleware.BindAndValidate[model.ManagedServer](c)
	if !ok {
		return
	}
	if err := a.serverService.Update(id, srv); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.update"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.update"), nil)
}

func (a *ManagedServerController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	if err := a.serverService.Delete(id); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.delete"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.delete"), nil)
}

// delBatch removes several managed servers at once. Each is deleted
// independently; the number actually removed is returned.
func (a *ManagedServerController) delBatch(c *gin.Context) {
	var req struct {
		ServerIds []int `json:"serverIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.delete"), err)
		return
	}
	if len(req.ServerIds) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.delete"), fmt.Errorf("at least one server is required"))
		return
	}
	removed := a.serverService.DeleteBatch(req.ServerIds)
	jsonObj(c, gin.H{"removed": removed}, nil)
}

func (a *ManagedServerController) setEnable(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	body := struct {
		Enable bool `json:"enable" form:"enable"`
	}{}
	if err := c.ShouldBind(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.update"), err)
		return
	}
	if err := a.serverService.SetEnable(id, body.Enable); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.update"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.update"), nil)
}

// test verifies a managed server's SSH credentials before it is saved and
// reports the host key so an operator can adopt it under trust-on-first-use.
// When an existing server is edited without re-entering its secret (they are
// write-only over the API), the stored ciphertext is carried forward so the
// test reflects what would actually be saved.
func (a *ManagedServerController) test(c *gin.Context) {
	srv := &model.ManagedServer{}
	if err := c.ShouldBind(srv); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.test"), err)
		return
	}
	if srv.SshPassword == "" || srv.SshPrivateKey == "" || srv.SshKeyPassphrase == "" {
		if id, err := strconv.Atoi(c.Query("id")); err == nil {
			if old, err := a.serverService.GetById(id); err == nil && old != nil {
				if srv.SshPassword == "" {
					srv.SshPassword = old.SshPassword
				}
				if srv.SshPrivateKey == "" {
					srv.SshPrivateKey = old.SshPrivateKey
				}
				if srv.SshKeyPassphrase == "" {
					srv.SshKeyPassphrase = old.SshKeyPassphrase
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	result := (&service.SSHService{}).TestConnection(ctx, srv)
	jsonObj(c, result, nil)
}

// exec runs one shell command on one or more managed servers and records each
// execution in the audit log. It inherits the /panel/api group's admin auth and
// CSRF middleware, so only an authenticated panel admin can reach it. The
// initiating username is taken from the session, never the request body, so the
// audit trail cannot be spoofed.
func (a *ManagedServerController) exec(c *gin.Context) {
	var req struct {
		ServerIds  []int  `json:"serverIds"`
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), err)
		return
	}
	// Running one server is just a batch of one, so the response shape stays
	// uniform for every caller.
	ids := req.ServerIds
	if len(ids) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), fmt.Errorf("at least one server is required"))
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), fmt.Errorf("command is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), execRequestBudget)
	defer cancel()
	result := a.serverService.ExecCommandBatch(ctx, ids, req.Command, time.Duration(req.TimeoutSec)*time.Second, username)
	jsonObj(c, result, nil)
}

// upload writes one or more files (multipart form field "file", repeated) to a
// destination path on every server in "serverIds" (comma-separated). Optional
// repeated "path" fields carry each file's relative path from a directory
// upload, in the same order as the files. With a single file and no relative
// path, "dest" ending in "/" drops it into that directory under its original
// name, otherwise it is the full target path; with multiple files or any
// relative path, "dest" is a destination directory the tree is recreated under.
// Every file is buffered once and fanned out concurrently.
func (a *ManagedServerController) upload(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), err)
		return
	}
	files := form.File["file"]
	if len(files) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), fmt.Errorf("a file is required"))
		return
	}
	ids := parseIntCSV(c.PostForm("serverIds"))
	if len(ids) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), fmt.Errorf("at least one server is required"))
		return
	}
	dest := strings.TrimSpace(c.PostForm("dest"))
	if dest == "" {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), fmt.Errorf("destination path is required"))
		return
	}
	timeoutSec, _ := strconv.Atoi(c.PostForm("timeoutSec"))
	// Relative paths are sent in the same order as the files; an entry may be
	// blank for a plainly-picked file, so index alignment is what matters.
	rels := form.Value["path"]

	var total int64
	entries := make([]service.UploadEntry, 0, len(files))
	for i, fh := range files {
		total += fh.Size
		if total > uploadMaxFileSize {
			jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), fmt.Errorf("upload exceeds the %d MiB limit", uploadMaxFileSize>>20))
			return
		}
		f, err := fh.Open()
		if err != nil {
			jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), err)
			return
		}
		content, err := io.ReadAll(io.LimitReader(f, uploadMaxFileSize))
		_ = f.Close()
		if err != nil {
			jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.upload"), err)
			return
		}
		rel := ""
		if i < len(rels) {
			rel = rels[i]
		}
		entries = append(entries, service.UploadEntry{Name: fh.Filename, Rel: rel, Content: content})
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), uploadRequestBudget)
	defer cancel()
	result := a.serverService.UploadFilesBatch(ctx, ids, dest, entries, time.Duration(timeoutSec)*time.Second)
	jsonObj(c, result, nil)
}

// copyPath copies a file or directory from one managed server to one or more
// others. The source path is staged onto the panel host once (over SFTP), then
// pushed to every target, so the source and targets need no connectivity to each
// other. A trailing "/" on dest follows the same rule as upload for a single
// file; for a directory source, dest is the destination directory the tree is
// recreated under.
func (a *ManagedServerController) copyPath(c *gin.Context) {
	var req struct {
		SourceId   int    `json:"sourceId"`
		SourcePath string `json:"sourcePath"`
		TargetIds  []int  `json:"targetIds"`
		Dest       string `json:"dest"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.copy"), err)
		return
	}
	if req.SourceId == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.copy"), fmt.Errorf("a source server is required"))
		return
	}
	if strings.TrimSpace(req.SourcePath) == "" {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.copy"), fmt.Errorf("source path is required"))
		return
	}
	if len(req.TargetIds) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.copy"), fmt.Errorf("at least one target server is required"))
		return
	}
	if strings.TrimSpace(req.Dest) == "" {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.copy"), fmt.Errorf("destination path is required"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), copyRequestBudget)
	defer cancel()
	result, err := a.serverService.CopyPathBatch(ctx, req.SourceId, strings.TrimSpace(req.SourcePath), req.TargetIds, strings.TrimSpace(req.Dest), time.Duration(req.TimeoutSec)*time.Second)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.copy"), err)
		return
	}
	jsonObj(c, result, nil)
}

// parseIntCSV parses a comma-separated list of ints, skipping blanks and
// non-numeric tokens. Used for form fields that carry a server-id list.
func parseIntCSV(s string) []int {
	out := make([]int, 0)
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if n, err := strconv.Atoi(tok); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// install installs 3x-ui on a managed server and, on success, derives a new
// panel Node linked to it. It is a long-running synchronous call — the install
// can take minutes — so it carries a wider request budget than exec.
func (a *ManagedServerController) install(c *gin.Context) {
	var req struct {
		ServerId int                   `json:"serverId"`
		Version  string                `json:"version"`
		Config   service.InstallConfig `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	if req.ServerId == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), fmt.Errorf("a server is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), installRequestBudget)
	defer cancel()
	result, err := a.serverService.InstallPanel(ctx, req.ServerId, req.Version, req.Config, username)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	a.serverService.ProbeNowForHost(req.ServerId)
	jsonObj(c, result, nil)
}

// installBatch installs 3x-ui on several managed servers at once. The version is
// applied to every server; servers run concurrently, bounded internally.
func (a *ManagedServerController) installBatch(c *gin.Context) {
	var req struct {
		ServerIds []int                 `json:"serverIds"`
		Version   string                `json:"version"`
		Config    service.InstallConfig `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	if len(req.ServerIds) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), fmt.Errorf("at least one server is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), installRequestBudget)
	defer cancel()
	result := a.serverService.InstallPanelBatch(ctx, req.ServerIds, req.Version, req.Config, username)
	for _, id := range req.ServerIds {
		a.serverService.ProbeNowForHost(id)
	}
	jsonObj(c, result, nil)
}

// importPanel adopts a panel that is already installed on a server, deriving a
// linked node from its running credentials without reinstalling.
func (a *ManagedServerController) importPanel(c *gin.Context) {
	var req struct {
		ServerId int `json:"serverId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	if req.ServerId == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), fmt.Errorf("a server is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), installRequestBudget)
	defer cancel()
	result, err := a.serverService.ImportPanel(ctx, req.ServerId, username)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	a.serverService.ProbeNowForHost(req.ServerId)
	jsonObj(c, result, nil)
}

// uninstall removes 3x-ui from a server and tears down its derived node.
func (a *ManagedServerController) uninstall(c *gin.Context) {
	var req struct {
		ServerId int `json:"serverId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.uninstall"), err)
		return
	}
	if req.ServerId == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.uninstall"), fmt.Errorf("a server is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), installRequestBudget)
	defer cancel()
	result, err := a.serverService.UninstallPanel(ctx, req.ServerId, username)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.uninstall"), err)
		return
	}
	a.serverService.ProbeNowForHost(req.ServerId)
	jsonObj(c, result, nil)
}

// uninstallBatch removes 3x-ui from several servers at once.
func (a *ManagedServerController) uninstallBatch(c *gin.Context) {
	var req struct {
		ServerIds []int `json:"serverIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.uninstall"), err)
		return
	}
	if len(req.ServerIds) == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.uninstall"), fmt.Errorf("at least one server is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), installRequestBudget)
	defer cancel()
	result := a.serverService.UninstallPanelBatch(ctx, req.ServerIds, username)
	for _, id := range req.ServerIds {
		a.serverService.ProbeNowForHost(id)
	}
	jsonObj(c, result, nil)
}

// panelVersions lists installable 3x-ui release tags for the install version
// picker (cached; newest first).
func (a *ManagedServerController) panelVersions(c *gin.Context) {
	versions, err := a.serverService.PanelVersions()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	jsonObj(c, versions, nil)
}

// execHistory returns a filtered, paginated page of the command audit log. It is
// read-only; the audit trail has no per-row delete.
func (a *ManagedServerController) execHistory(c *gin.Context) {
	var params service.ExecHistoryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), err)
		return
	}
	resp, err := a.serverService.ExecHistory(params)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), err)
		return
	}
	jsonObj(c, resp, nil)
}

// pruneExecHistory removes audit rows older than olderThanDays. This is the only
// deletion path for the audit log — retention management, not selective erasure.
func (a *ManagedServerController) pruneExecHistory(c *gin.Context) {
	var req struct {
		OlderThanDays int `json:"olderThanDays"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), err)
		return
	}
	removed, err := a.serverService.PruneExecHistory(req.OlderThanDays)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), err)
		return
	}
	jsonObj(c, gin.H{"removed": removed}, nil)
}
