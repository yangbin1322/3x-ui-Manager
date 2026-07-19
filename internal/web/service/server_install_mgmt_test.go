package service

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestDeleteNodeClearsServerLink(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	serverSvc := &ManagedServerService{}
	srv := installTestServer(t, serverSvc, "linked-server")

	env := &installEnv{port: 2096, basePath: "p", scheme: "https", token: "tok"}
	nodeId, err := serverSvc.deriveNode(srv, env)
	if err != nil {
		t.Fatalf("deriveNode: %v", err)
	}

	if err := (&NodeService{}).Delete(nodeId); err != nil {
		t.Fatalf("delete node: %v", err)
	}

	after, err := serverSvc.GetById(srv.Id)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if after.NodeId != 0 {
		t.Fatalf("server NodeId = %d after deleting the node, want 0 (link cleared)", after.NodeId)
	}
}

func TestCreateBatch(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}

	servers := []*model.ManagedServer{
		// Empty name -> defaults to the address.
		{Address: "203.0.113.5", SshUser: "root", SshAuthType: "password", SshPassword: "pw"},
		// Named row.
		{Name: "hk-2", Address: "203.0.113.6", SshUser: "root", SshAuthType: "password", SshPassword: "pw"},
		// Invalid: no credential -> fails without blocking the others.
		{Name: "bad", Address: "203.0.113.7", SshUser: "root", SshAuthType: "password"},
	}
	// verify=false: these are placeholder addresses with no reachable SSH host,
	// so this exercises validation + creation, not connectivity.
	resp := svc.CreateBatch(t.Context(), servers, false)
	if len(resp.Results) != 3 {
		t.Fatalf("got %d results, want 3", len(resp.Results))
	}
	if !resp.Results[0].Success || resp.Results[0].Name != "203.0.113.5" {
		t.Fatalf("row 0 = %+v, want success with name defaulted to address", resp.Results[0])
	}
	if !resp.Results[1].Success || resp.Results[1].Name != "hk-2" {
		t.Fatalf("row 1 = %+v, want success named hk-2", resp.Results[1])
	}
	if resp.Results[2].Success || !strings.Contains(resp.Results[2].Message, "ssh password is required") {
		t.Fatalf("row 2 = %+v, want failure for the missing credential", resp.Results[2])
	}

	all, err := svc.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("stored %d servers, want 2 (the invalid row was skipped)", len(all))
	}
}

func TestSharedNodeAcrossSameHostRows(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}

	// Two rows for the same box (same address+port+user), different names.
	a := &model.ManagedServer{Name: "box-a", Address: "203.0.113.9", SshPort: 22, SshUser: "root", SshAuthType: "password", SshPassword: "pw"}
	b := &model.ManagedServer{Name: "box-b", Address: "203.0.113.9", SshPort: 22, SshUser: "root", SshAuthType: "password", SshPassword: "pw"}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := svc.Create(b); err != nil {
		t.Fatalf("create b: %v", err)
	}

	// Deriving a node for row a must link BOTH rows to that node.
	env := &installEnv{port: 2096, basePath: "p", scheme: "https", token: "tok"}
	nodeId, err := svc.deriveNode(a, env)
	if err != nil {
		t.Fatalf("deriveNode: %v", err)
	}
	ga, _ := svc.GetById(a.Id)
	gb, _ := svc.GetById(b.Id)
	if ga.NodeId != nodeId || gb.NodeId != nodeId {
		t.Fatalf("both rows should share node %d, got a=%d b=%d", nodeId, ga.NodeId, gb.NodeId)
	}

	// Deleting one row must NOT delete the shared node (the other still uses it).
	if err := svc.Delete(a.Id); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	if _, err := (&NodeService{}).GetById(nodeId); err != nil {
		t.Fatalf("shared node was deleted while a sibling still references it: %v", err)
	}

	// Deleting the last referencing row must delete the node.
	if err := svc.Delete(b.Id); err != nil {
		t.Fatalf("delete b: %v", err)
	}
	if _, err := (&NodeService{}).GetById(nodeId); err == nil {
		t.Fatalf("node should be gone after the last referencing server was deleted")
	}
}

func TestInstallReusesSameHostNode(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}

	a := &model.ManagedServer{Name: "reuse-a", Address: "203.0.113.11", SshPort: 22, SshUser: "root", SshAuthType: "password", SshPassword: "pw"}
	b := &model.ManagedServer{Name: "reuse-b", Address: "203.0.113.11", SshPort: 22, SshUser: "root", SshAuthType: "password", SshPassword: "pw"}
	if err := svc.Create(a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := svc.Create(b); err != nil {
		t.Fatalf("create b: %v", err)
	}
	// Row a already has a derived node.
	env := &installEnv{port: 2096, basePath: "p", scheme: "https", token: "tok"}
	nodeId, err := svc.deriveNode(a, env)
	if err != nil {
		t.Fatalf("deriveNode: %v", err)
	}
	// Clear b's link to simulate an unlinked sibling asking to install.
	if err := database.GetDB().Model(&model.ManagedServer{}).Where("id = ?", b.Id).Update("node_id", 0).Error; err != nil {
		t.Fatalf("unlink b: %v", err)
	}
	// Installing on b must reuse a's node, not create a second one.
	res, err := svc.InstallPanel(t.Context(), b.Id, "", "admin")
	if err != nil {
		t.Fatalf("install b: %v", err)
	}
	if res.NodeId != nodeId {
		t.Fatalf("install on sibling created node %d, want reuse of %d", res.NodeId, nodeId)
	}
	var nodeCount int64
	database.GetDB().Model(&model.Node{}).Count(&nodeCount)
	if nodeCount != 1 {
		t.Fatalf("node count = %d, want 1 (reused, not a second node)", nodeCount)
	}
}

func TestParsePanelVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"typical", "x-ui version v2.6.0\n", "v2.6.0"},
		{"bare version", "v2.5.5", "v2.5.5"},
		{"ansi wrapped", "\x1b[0;32mx-ui version v2.6.0\x1b[0m\n", "v2.6.0"},
		{"empty", "", ""},
		{"command-not-found noise is not a version", "bash: x-ui: command not found", ""},
		{"menu banner is not a version", "x-ui control menu", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePanelVersion(tt.in); got != tt.want {
				t.Fatalf("parsePanelVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseShowSetting(t *testing.T) {
	// A plain HTTP install: cert file empty, so hasCert is false.
	out := "current panel settings:\nport: 2053\nwebBasePath: /abcd/\nwebCertFile: \nusername: admin\n"
	port, basePath, hasCert := parseShowSetting(out)
	if port != 2053 {
		t.Fatalf("port = %d, want 2053", port)
	}
	if basePath != "abcd" {
		t.Fatalf("basePath = %q, want abcd (slashes trimmed)", basePath)
	}
	if hasCert {
		t.Fatalf("hasCert = true for an empty webCertFile, want false (http)")
	}

	// A TLS install: a non-empty cert file means https.
	_, _, tls := parseShowSetting("port: 2053\nwebCertFile: /root/cert/fullchain.pem\n")
	if !tls {
		t.Fatalf("hasCert = false for a configured webCertFile, want true (https)")
	}

	port2, base2, cert2 := parseShowSetting("no settings here\n")
	if port2 != 0 || base2 != "" || cert2 {
		t.Fatalf("absent settings = (%d, %q, %v), want (0, \"\", false)", port2, base2, cert2)
	}
}

func TestImportPanelRejectsLinkedServer(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}
	srv := installTestServer(t, svc, "already-linked")
	if err := database.GetDB().Model(&model.ManagedServer{}).Where("id = ?", srv.Id).
		Update("node_id", 42).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}
	_, err := svc.ImportPanel(t.Context(), srv.Id, "admin")
	if err == nil || !strings.Contains(err.Error(), "already has a linked panel node") {
		t.Fatalf("ImportPanel on linked server error = %v, want already-linked rejection", err)
	}
}

func TestUpdateSSHHeartbeatPanelStateGatedOnKnown(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &ManagedServerService{}
	srv := installTestServer(t, svc, "hb-server")

	// A reachable probe that saw a panel records it.
	if err := svc.UpdateSSHHeartbeat(srv.Id, SSHHeartbeatPatch{
		Status: "reachable", PanelKnown: true, PanelInstalled: true, PanelVersion: "v2.6.0",
	}); err != nil {
		t.Fatalf("update reachable: %v", err)
	}
	got, _ := svc.GetById(srv.Id)
	if !got.PanelInstalled || got.PanelVersion != "v2.6.0" {
		t.Fatalf("after reachable probe = (%v, %q), want (true, v2.6.0)", got.PanelInstalled, got.PanelVersion)
	}

	// An unreachable probe (PanelKnown false) must NOT clear the known state.
	if err := svc.UpdateSSHHeartbeat(srv.Id, SSHHeartbeatPatch{Status: "unreachable"}); err != nil {
		t.Fatalf("update unreachable: %v", err)
	}
	got, _ = svc.GetById(srv.Id)
	if !got.PanelInstalled || got.PanelVersion != "v2.6.0" {
		t.Fatalf("unreachable probe cleared panel state: (%v, %q), want it preserved", got.PanelInstalled, got.PanelVersion)
	}
}
