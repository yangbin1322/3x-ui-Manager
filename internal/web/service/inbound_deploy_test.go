package service

import (
	"encoding/json"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestDeployInboundToNodes(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &InboundService{}

	// Source inbound with a client on the panel's own xray (no node).
	settings := `{"clients":[` +
		`{"id":"11111111-1111-1111-1111-111111111111","email":"alice","subId":"s-alice","enable":true}` +
		`],"decryption":"none","encryption":"none"}`
	src := makeImportInbound("in-9201-tcp", 9201, settings, []xray.ClientTraffic{
		{Email: "alice", Up: 1, Down: 2, Total: 1000},
	})
	created, _, err := svc.AddInbound(src)
	if err != nil {
		t.Fatalf("add source inbound: %v", err)
	}

	// Two api nodes to deploy onto.
	nodeSvc := &NodeService{}
	hk := &model.Node{Name: "hk 1", Address: "node1.example.com", Port: 2053, ApiToken: "t1"}
	sg := &model.Node{Name: "sg-1", Address: "node2.example.com", Port: 2053, ApiToken: "t2"}
	if err := nodeSvc.Create(hk); err != nil {
		t.Fatalf("create hk: %v", err)
	}
	if err := nodeSvc.Create(sg); err != nil {
		t.Fatalf("create sg: %v", err)
	}

	resp, err := svc.DeployInboundToNodes(created.Id, []int{hk.Id, sg.Id})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	for _, r := range resp.Results {
		if !r.Success {
			t.Fatalf("node %d deploy failed: %s", r.NodeId, r.Message)
		}
	}
	// The "hk 1" name is sanitized to "hk-1" in the tag suffix.
	if resp.Results[0].Tag != "in-9201-tcp-hk-1" {
		t.Fatalf("tag = %q, want in-9201-tcp-hk-1 (source tag + sanitized node name)", resp.Results[0].Tag)
	}

	// Each copy exists on its node with an empty client list; the source is
	// untouched and keeps its client.
	var copies []model.Inbound
	if err := database.GetDB().Where("node_id IS NOT NULL").Find(&copies).Error; err != nil {
		t.Fatalf("load copies: %v", err)
	}
	if len(copies) != 2 {
		t.Fatalf("got %d node copies, want 2", len(copies))
	}
	for _, cp := range copies {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(cp.Settings), &parsed); err != nil {
			t.Fatalf("parse copy settings: %v", err)
		}
		clients, _ := parsed["clients"].([]any)
		if len(clients) != 0 {
			t.Fatalf("copy %q has %d clients, want 0 (clients must not be copied)", cp.Tag, len(clients))
		}
	}

	// Redeploying to a node that already has the copy fails on that node (tag
	// collision) but does not error the whole call.
	resp2, err := svc.DeployInboundToNodes(created.Id, []int{hk.Id})
	if err != nil {
		t.Fatalf("redeploy: %v", err)
	}
	if resp2.Results[0].Success {
		t.Fatalf("redeploy to a node that already has the copy should fail (tag collision)")
	}
}
