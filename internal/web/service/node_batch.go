package service

import (
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// NodeBatchResult is one node's outcome from a batch node operation (remove
// inbounds / remove clients / delete). Counts report how much was affected on
// that node; Error is set when the node itself could not be processed.
type NodeBatchResult struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Inbounds int    `json:"inbounds,omitempty"`
	Clients  int    `json:"clients,omitempty"`
	Error    string `json:"error,omitempty"`
}

// NodeBatchResponse aggregates per-node results plus whether xray needs a
// restart afterward (a client/inbound change on the panel's own node).
type NodeBatchResponse struct {
	Results     []NodeBatchResult `json:"results"`
	NeedRestart bool              `json:"needRestart"`
}

// inboundIdsForNode returns the ids of every inbound attached to the node.
func (s *NodeService) inboundIdsForNode(nodeId int) ([]int, error) {
	var ids []int
	if err := database.GetDB().Model(&model.Inbound{}).
		Where("node_id = ?", nodeId).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// clientEmailsForInbounds returns the distinct client emails attached to any of
// the given inbounds, so a node-scoped client removal targets exactly those.
func clientEmailsForInbounds(inboundSvc *InboundService, inboundIds []int) ([]string, error) {
	seen := map[string]struct{}{}
	var emails []string
	for _, id := range inboundIds {
		ib, err := inboundSvc.GetInbound(id)
		if err != nil {
			return nil, err
		}
		clients, err := inboundSvc.GetClients(ib)
		if err != nil {
			return nil, err
		}
		for _, c := range clients {
			if c.Email == "" {
				continue
			}
			if _, dup := seen[c.Email]; dup {
				continue
			}
			seen[c.Email] = struct{}{}
			emails = append(emails, c.Email)
		}
	}
	return emails, nil
}

// RemoveNodeInbounds deletes every inbound (and its clients) on each given node,
// reusing InboundService.DelInbounds. A node with no inbounds is a no-op success.
func (s *NodeService) RemoveNodeInbounds(inboundSvc *InboundService, nodeIds []int) (*NodeBatchResponse, error) {
	resp := &NodeBatchResponse{Results: make([]NodeBatchResult, 0, len(nodeIds))}
	for _, nodeId := range nodeIds {
		res := NodeBatchResult{Id: nodeId}
		node, err := s.GetById(nodeId)
		if err != nil || node == nil {
			res.Error = "node not found"
			resp.Results = append(resp.Results, res)
			continue
		}
		res.Name = node.Name
		inboundIds, err := s.inboundIdsForNode(nodeId)
		if err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		if len(inboundIds) == 0 {
			res.OK = true
			resp.Results = append(resp.Results, res)
			continue
		}
		result, needRestart, err := inboundSvc.DelInbounds(inboundIds)
		if err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		if needRestart {
			resp.NeedRestart = true
		}
		res.OK = true
		res.Inbounds = result.Deleted
		resp.Results = append(resp.Results, res)
	}
	return resp, nil
}

// RemoveNodeClients detaches every client from each given node's inbounds,
// keeping the inbounds themselves. It reuses ClientService.BulkDetach, so a
// client shared with another inbound stays on that other inbound.
func (s *NodeService) RemoveNodeClients(inboundSvc *InboundService, clientSvc *ClientService, nodeIds []int) (*NodeBatchResponse, error) {
	resp := &NodeBatchResponse{Results: make([]NodeBatchResult, 0, len(nodeIds))}
	for _, nodeId := range nodeIds {
		res := NodeBatchResult{Id: nodeId}
		node, err := s.GetById(nodeId)
		if err != nil || node == nil {
			res.Error = "node not found"
			resp.Results = append(resp.Results, res)
			continue
		}
		res.Name = node.Name
		inboundIds, err := s.inboundIdsForNode(nodeId)
		if err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		if len(inboundIds) == 0 {
			res.OK = true
			resp.Results = append(resp.Results, res)
			continue
		}
		emails, err := clientEmailsForInbounds(inboundSvc, inboundIds)
		if err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		if len(emails) == 0 {
			res.OK = true
			resp.Results = append(resp.Results, res)
			continue
		}
		detach, needRestart, err := clientSvc.BulkDetach(inboundSvc, emails, inboundIds)
		if err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		if needRestart {
			resp.NeedRestart = true
		}
		res.OK = true
		res.Clients = len(detach.Detached)
		resp.Results = append(resp.Results, res)
	}
	return resp, nil
}

// DeleteNodes deletes each given node. When force is false a node that still has
// inbounds is skipped and reported (matching single-delete's refusal). When
// force is true the node's inbounds (and their clients) are removed first, then
// the node row is deleted.
func (s *NodeService) DeleteNodes(inboundSvc *InboundService, nodeIds []int, force bool) (*NodeBatchResponse, error) {
	resp := &NodeBatchResponse{Results: make([]NodeBatchResult, 0, len(nodeIds))}
	for _, nodeId := range nodeIds {
		res := NodeBatchResult{Id: nodeId}
		node, err := s.GetById(nodeId)
		if err != nil || node == nil {
			res.Error = "node not found"
			resp.Results = append(resp.Results, res)
			continue
		}
		res.Name = node.Name

		inboundIds, err := s.inboundIdsForNode(nodeId)
		if err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		if len(inboundIds) > 0 {
			if !force {
				res.Error = "node still has inbounds; remove them first or confirm cascade delete"
				resp.Results = append(resp.Results, res)
				continue
			}
			result, needRestart, delErr := inboundSvc.DelInbounds(inboundIds)
			if delErr != nil {
				res.Error = delErr.Error()
				resp.Results = append(resp.Results, res)
				continue
			}
			if needRestart {
				resp.NeedRestart = true
			}
			res.Inbounds = result.Deleted
		}

		if err := s.Delete(nodeId); err != nil {
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		res.OK = true
		resp.Results = append(resp.Results, res)
	}
	return resp, nil
}
