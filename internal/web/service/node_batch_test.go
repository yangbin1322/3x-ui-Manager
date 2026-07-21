package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func nodeWithInbound(t *testing.T, svc *InboundService, nodeSvc *NodeService, name, tag string, port int, email string) (*model.Node, *model.Inbound) {
	t.Helper()
	n := &model.Node{Name: name, Address: name + ".example.com", Port: 2053, ApiToken: "t-" + name}
	if err := nodeSvc.Create(n); err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	settings := `{"clients":[` +
		`{"id":"33333333-3333-3333-3333-333333333333","email":"` + email + `","subId":"s-` + email + `","enable":true}` +
		`],"decryption":"none","encryption":"none"}`
	ib := makeImportInbound(tag, port, settings, []xray.ClientTraffic{{Email: email}})
	nid := n.Id
	ib.NodeID = &nid
	created, _, err := svc.AddInbound(ib)
	if err != nil {
		t.Fatalf("add inbound on %s: %v", name, err)
	}
	return n, created
}

func countInboundsOnNode(t *testing.T, nodeId int) int64 {
	t.Helper()
	var c int64
	if err := database.GetDB().Model(&model.Inbound{}).Where("node_id = ?", nodeId).Count(&c).Error; err != nil {
		t.Fatalf("count inbounds: %v", err)
	}
	return c
}

func TestRemoveNodeInbounds(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &InboundService{}
	nodeSvc := &NodeService{}

	n, _ := nodeWithInbound(t, svc, nodeSvc, "hk", "in-7101", 7101, "u1")

	resp, err := nodeSvc.RemoveNodeInbounds(svc, []int{n.Id})
	if err != nil {
		t.Fatalf("remove inbounds: %v", err)
	}
	if !resp.Results[0].OK || resp.Results[0].Inbounds != 1 {
		t.Fatalf("result = %+v, want OK with 1 inbound removed", resp.Results[0])
	}
	if got := countInboundsOnNode(t, n.Id); got != 0 {
		t.Fatalf("node still has %d inbounds, want 0", got)
	}
}

func TestRemoveNodeClients_KeepsInbound(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &InboundService{}
	nodeSvc := &NodeService{}

	n, ib := nodeWithInbound(t, svc, nodeSvc, "sg", "in-7201", 7201, "u2")

	resp, err := nodeSvc.RemoveNodeClients(svc, &ClientService{}, []int{n.Id})
	if err != nil {
		t.Fatalf("remove clients: %v", err)
	}
	if !resp.Results[0].OK || resp.Results[0].Clients != 1 {
		t.Fatalf("result = %+v, want OK with 1 client detached", resp.Results[0])
	}
	if got := countInboundsOnNode(t, n.Id); got != 1 {
		t.Fatalf("node has %d inbounds, want 1 (inbound kept)", got)
	}
	reload, err := svc.GetInbound(ib.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	clients, err := svc.GetClients(reload)
	if err != nil {
		t.Fatalf("get clients: %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("inbound has %d clients, want 0 (detached)", len(clients))
	}
}

func TestDeleteNodes_ForceCascades(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	setupConflictDB(t)
	svc := &InboundService{}
	nodeSvc := &NodeService{}

	n, _ := nodeWithInbound(t, svc, nodeSvc, "de", "in-7301", 7301, "u3")

	resp, err := nodeSvc.DeleteNodes(svc, []int{n.Id}, false)
	if err != nil {
		t.Fatalf("delete (no force): %v", err)
	}
	if resp.Results[0].OK || resp.Results[0].Error == "" {
		t.Fatalf("expected refusal with inbounds attached, got %+v", resp.Results[0])
	}
	if got := countNodes(t, n.Id); got != 1 {
		t.Fatalf("node was deleted despite refusal")
	}

	resp2, err := nodeSvc.DeleteNodes(svc, []int{n.Id}, true)
	if err != nil {
		t.Fatalf("delete (force): %v", err)
	}
	if !resp2.Results[0].OK || resp2.Results[0].Inbounds != 1 {
		t.Fatalf("force result = %+v, want OK with 1 inbound removed", resp2.Results[0])
	}
	if got := countNodes(t, n.Id); got != 0 {
		t.Fatalf("node still exists after force delete")
	}
}

func countNodes(t *testing.T, id int) int64 {
	t.Helper()
	var c int64
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", id).Count(&c).Error; err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	return c
}
