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
