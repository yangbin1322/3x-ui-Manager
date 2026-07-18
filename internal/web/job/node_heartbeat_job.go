package job

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/websocket"
)

const (
	nodeHeartbeatConcurrency    = 32
	nodeHeartbeatRequestTimeout = 4 * time.Second
	// An SSH handshake is slower than an HTTP status call and runs a small
	// command to read the remote OS, so it gets a wider budget than the panel
	// probe rather than reporting a healthy-but-slow server as unreachable.
	serverSSHProbeTimeout = 15 * time.Second
)

type NodeHeartbeatJob struct {
	nodeService          service.NodeService
	managedServerService service.ManagedServerService
	running              sync.Mutex
}

func NewNodeHeartbeatJob() *NodeHeartbeatJob {
	return &NodeHeartbeatJob{}
}

func (j *NodeHeartbeatJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()

	nodes, err := j.nodeService.GetAll()
	if err != nil {
		logger.Warning("node heartbeat: load nodes failed:", err)
		return
	}
	servers, err := j.managedServerService.GetAll()
	if err != nil {
		logger.Warning("node heartbeat: load managed servers failed:", err)
	}
	if len(nodes) == 0 && len(servers) == 0 {
		return
	}

	sem := make(chan struct{}, nodeHeartbeatConcurrency)
	var wg sync.WaitGroup
	for _, n := range nodes {
		if !n.Enable {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		n := n
		common.GoRecover("node-heartbeat:"+n.Name, func() {
			defer wg.Done()
			defer func() { <-sem }()
			j.probeOne(n)
		})
	}
	// Managed servers have no panel to poll: their reachability is measured
	// over SSH, sharing the same fan-out budget as the node probes.
	for _, srv := range servers {
		if !srv.Enable {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		srv := srv
		common.GoRecover("server-heartbeat:"+srv.Name, func() {
			defer wg.Done()
			defer func() { <-sem }()
			j.probeOneServer(srv)
		})
	}
	wg.Wait()

	if !websocket.HasClients() {
		return
	}
	updated, err := j.nodeService.GetNodeTree()
	if err != nil {
		logger.Warning("node heartbeat: load nodes for broadcast failed:", err)
		return
	}
	websocket.BroadcastNodes(updated)
}

func (j *NodeHeartbeatJob) probeOne(n *model.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), nodeHeartbeatRequestTimeout)
	defer cancel()
	prevStatus := n.Status
	patch, err := j.nodeService.Probe(ctx, n)
	if err != nil {
		patch.Status = "offline"
	} else {
		patch.Status = "online"
	}
	if updErr := j.nodeService.UpdateHeartbeat(n.Id, patch); updErr != nil {
		logger.Warning("node heartbeat: update node", n.Id, "failed:", updErr)
	}
	publishNodeTransition(n, prevStatus, patch)
	// Learn the nodes this node manages so the panel can surface them as
	// transitive sub-nodes (#4983). Fresh context — the probe budget above may
	// be spent. Drop them when the node is unreachable.
	if patch.Status == "online" {
		dctx, dcancel := context.WithTimeout(context.Background(), nodeHeartbeatRequestTimeout)
		j.nodeService.RefreshDescendants(dctx, n)
		dcancel()
	} else {
		j.nodeService.ClearDescendants(n.Id)
	}
}

// probeOneServer measures a managed server's SSH reachability. It does not
// emit node.up / node.down: those events carry panel and Xray health that an
// SSH probe cannot observe, and their consumers act on panel-level state.
func (j *NodeHeartbeatJob) probeOneServer(srv *model.ManagedServer) {
	ctx, cancel := context.WithTimeout(context.Background(), serverSSHProbeTimeout)
	defer cancel()
	patch := j.managedServerService.ProbeSSH(ctx, srv)
	if err := j.managedServerService.UpdateSSHHeartbeat(srv.Id, patch); err != nil {
		logger.Warning("node heartbeat: update managed server", srv.Id, "failed:", err)
	}
}

// publishNodeTransition emits node.down / node.up only on a genuine state change.
// An "unknown"/empty previous status (fresh start) is treated as not-online, so a
// node coming up for the first time fires node.up but never a spurious node.down.
func publishNodeTransition(n *model.Node, prevStatus string, patch service.HeartbeatPatch) {
	if EventBus == nil {
		return
	}
	var eventType eventbus.EventType
	switch {
	case prevStatus == "online" && patch.Status == "offline":
		eventType = eventbus.EventNodeDown
	case prevStatus != "online" && patch.Status == "online":
		eventType = eventbus.EventNodeUp
	default:
		return
	}
	source := n.Name
	if source == "" {
		source = "node-" + strconv.Itoa(n.Id)
	}
	EventBus.Publish(eventbus.Event{
		Type:   eventType,
		Source: source,
		Data: &eventbus.NodeHealthData{
			NodeId:    n.Id,
			LatencyMs: patch.LatencyMs,
			CpuPct:    patch.CpuPct,
			MemPct:    patch.MemPct,
			XrayState: patch.XrayState,
			XrayError: patch.XrayError,
		},
	})
}
