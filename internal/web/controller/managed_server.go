package controller

import (
	"context"
	"fmt"
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
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/setEnable/:id", a.setEnable)

	g.POST("/test", a.test)
	g.POST("/exec", a.exec)
	g.POST("/install", a.install)
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
		NodeIds    []int  `json:"nodeIds"`
		NodeId     int    `json:"nodeId"`
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.exec"), err)
		return
	}
	// serverIds is the canonical field; nodeIds / nodeId are accepted as the
	// pre-split spellings so existing callers keep working. Running one server
	// is just a batch of one, so the response shape stays uniform.
	ids := req.ServerIds
	if len(ids) == 0 {
		ids = req.NodeIds
	}
	if len(ids) == 0 && req.NodeId != 0 {
		ids = []int{req.NodeId}
	}
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

// install installs 3x-ui on a managed server and, on success, derives a new
// panel Node linked to it. It is a long-running synchronous call — the install
// can take minutes — so it carries a wider request budget than exec.
func (a *ManagedServerController) install(c *gin.Context) {
	var req struct {
		ServerId int    `json:"serverId"`
		NodeId   int    `json:"nodeId"`
		Version  string `json:"version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	id := req.ServerId
	if id == 0 {
		id = req.NodeId
	}
	if id == 0 {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), fmt.Errorf("a server is required"))
		return
	}
	username := ""
	if u := session.GetLoginUser(c); u != nil {
		username = u.Username
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), installRequestBudget)
	defer cancel()
	result, err := a.serverService.InstallPanel(ctx, id, req.Version, username)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.nodes.toasts.install"), err)
		return
	}
	jsonObj(c, result, nil)
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
