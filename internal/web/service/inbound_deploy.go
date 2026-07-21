package service

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// DeployResult is one node's outcome from copying an inbound to several nodes.
type DeployResult struct {
	NodeId   int    `json:"nodeId" example:"3"`
	NodeName string `json:"nodeName" example:"hk-1"`
	Tag      string `json:"tag,omitempty" example:"in-443-tcp-hk1"`
	Success  bool   `json:"success" example:"true"`
	Message  string `json:"message,omitempty"`
	Attached int    `json:"attached,omitempty" example:"2"`
}

type DeployResponse struct {
	Results []DeployResult `json:"results"`
}

// DeployClientMode selects what happens to clients on each node copy.
//   - none: copy config only, no clients (the default and original behavior).
//   - copy: attach the source inbound's own clients to every copy, sharing the
//     same client identity (email/UUID/traffic account) across nodes.
//   - bind: attach a caller-chosen set of existing clients (by email) to every copy.
type DeployClientMode string

const (
	DeployClientsNone DeployClientMode = "none"
	DeployClientsCopy DeployClientMode = "copy"
	DeployClientsBind DeployClientMode = "bind"
)

// DeployOptions carries the client-handling choice for DeployInboundToNodes.
// ClientEmails is only read when ClientMode is bind.
type DeployOptions struct {
	ClientMode   DeployClientMode `json:"clientMode" example:"none"`
	ClientEmails []string         `json:"clientEmails,omitempty"`
}

// tagSuffixSanitizer keeps a node name usable as a tag suffix: xray tags are
// bare identifiers, so anything but alphanumerics / dash / underscore is
// collapsed to a dash.
var tagSuffixSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// clearInboundClients empties the clients array in the inbound settings, so a
// copied inbound carries the transport/TLS/port config but no clients — client
// emails are globally unique and cannot be duplicated across nodes. Returns the
// settings unchanged if it has no clients key.
func clearInboundClients(settings string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil || parsed == nil {
		return settings
	}
	if _, ok := parsed["clients"]; !ok {
		return settings
	}
	parsed["clients"] = []any{}
	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return settings
	}
	return string(out)
}

// DeployInboundToNodes copies one inbound's configuration onto each of the given
// nodes: for every node it clones the inbound (new tag "<tag>-<nodeName>",
// remark "<remark>-<nodeName>", the node's id, and an emptied client list) and
// creates it there via AddInbound. Depending on opts.ClientMode it then attaches
// clients to each copy — none (config only), copy (the source inbound's own
// clients, shared identity), or bind (a caller-chosen set of existing clients).
// Each node is independent — one failure does not abort the rest — and the
// response reports per-node success/failure. The node that already hosts the
// source inbound is skipped (a copy there would collide with itself).
func (s *InboundService) DeployInboundToNodes(inboundId int, nodeIds []int, opts DeployOptions) (*DeployResponse, error) {
	src, err := s.GetInbound(inboundId)
	if err != nil || src == nil {
		return nil, common.NewError("inbound not found")
	}

	var attachEmails []string
	switch opts.ClientMode {
	case DeployClientsCopy:
		clients, cerr := s.GetClients(src)
		if cerr != nil {
			return nil, cerr
		}
		for _, c := range clients {
			if c.Email != "" {
				attachEmails = append(attachEmails, c.Email)
			}
		}
	case DeployClientsBind:
		seen := make(map[string]struct{}, len(opts.ClientEmails))
		for _, e := range opts.ClientEmails {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if _, dup := seen[strings.ToLower(e)]; dup {
				continue
			}
			seen[strings.ToLower(e)] = struct{}{}
			attachEmails = append(attachEmails, e)
		}
	}

	clientSvc := ClientService{}
	nodeSvc := NodeService{}
	results := make([]DeployResult, 0, len(nodeIds))
	for _, nodeId := range nodeIds {
		res := DeployResult{NodeId: nodeId}
		node, err := nodeSvc.GetById(nodeId)
		if err != nil || node == nil {
			res.Message = "node not found"
			results = append(results, res)
			continue
		}
		res.NodeName = node.Name
		if src.NodeID != nil && *src.NodeID == nodeId {
			res.Message = "source inbound already lives on this node"
			results = append(results, res)
			continue
		}

		clone := *src
		clone.Id = 0
		clone.ClientStats = nil
		nid := nodeId
		clone.NodeID = &nid
		clone.OriginNodeGuid = ""
		clone.Settings = clearInboundClients(src.Settings)
		suffix := tagSuffixSanitizer.ReplaceAllString(strings.TrimSpace(node.Name), "-")
		if suffix == "" {
			suffix = "node" + strconv.Itoa(nodeId)
		}
		clone.Tag = src.Tag + "-" + suffix
		if trimmed := strings.TrimSpace(src.Remark); trimmed != "" {
			clone.Remark = trimmed + "-" + suffix
		} else {
			clone.Remark = suffix
		}

		created, _, addErr := s.AddInbound(&clone)
		if addErr != nil {
			res.Message = addErr.Error()
			results = append(results, res)
			continue
		}
		res.Success = true
		res.Tag = created.Tag

		if len(attachEmails) > 0 {
			attachRes, _, aerr := clientSvc.BulkAttach(s, attachEmails, []int{created.Id})
			if aerr != nil {
				res.Message = "clients: " + aerr.Error()
			} else {
				res.Attached = len(attachRes.Attached)
				if len(attachRes.Errors) > 0 {
					res.Message = "clients: " + strings.Join(attachRes.Errors, "; ")
				}
			}
		}
		results = append(results, res)
	}
	return &DeployResponse{Results: results}, nil
}
