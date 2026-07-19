package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"

	"gorm.io/gorm"
)

// ManagedServerService manages servers reached over SSH. It is the write path
// for their credentials (encrypted at rest) and the read path the heartbeat and
// remote-command features build on. Panel nodes are NodeService's job; the two
// never share rows.
type ManagedServerService struct{}

func (s *ManagedServerService) GetAll() ([]*model.ManagedServer, error) {
	db := database.GetDB()
	var servers []*model.ManagedServer
	err := db.Model(model.ManagedServer{}).Order("id asc").Find(&servers).Error
	for _, srv := range servers {
		srv.SshPasswordSet = srv.SshPassword != ""
		srv.SshPrivateKeySet = srv.SshPrivateKey != ""
	}
	return servers, err
}

func (s *ManagedServerService) GetById(id int) (*model.ManagedServer, error) {
	db := database.GetDB()
	srv := &model.ManagedServer{}
	if err := db.Model(model.ManagedServer{}).Where("id = ?", id).First(srv).Error; err != nil {
		return nil, err
	}
	srv.SshPasswordSet = srv.SshPassword != ""
	srv.SshPrivateKeySet = srv.SshPrivateKey != ""
	return srv, nil
}

// normalize validates a managed server and encrypts its credentials. It is
// called on every write path, so a row never reaches the database with a
// plaintext secret.
func (s *ManagedServerService) normalize(srv *model.ManagedServer) error {
	srv.Name = strings.TrimSpace(srv.Name)
	if srv.Name == "" {
		return common.NewError("server name is required")
	}
	addr, err := netsafe.NormalizeHost(srv.Address)
	if err != nil {
		return common.NewError(err.Error())
	}
	srv.Address = addr
	srv.SshUser = strings.TrimSpace(srv.SshUser)
	if srv.SshUser == "" {
		return common.NewError("ssh username is required")
	}
	if srv.SshPort <= 0 {
		srv.SshPort = 22
	}
	if srv.SshPort > 65535 {
		return common.NewError("ssh port must be 1-65535")
	}
	if srv.SshAuthType != "key" {
		srv.SshAuthType = "password"
	}
	if srv.SshHostKeyMode != "pin" && srv.SshHostKeyMode != "skip" {
		srv.SshHostKeyMode = "trust"
	}
	srv.SshHostKeySha256 = strings.TrimSpace(srv.SshHostKeySha256)
	if srv.SshHostKeyMode == "pin" && srv.SshHostKeySha256 == "" {
		return common.NewError("host key pinning requires a fingerprint; test the connection first to learn it")
	}

	switch srv.SshAuthType {
	case "key":
		if strings.TrimSpace(srv.SshPrivateKey) == "" {
			return common.NewError("ssh private key is required")
		}
	default:
		if srv.SshPassword == "" {
			return common.NewError("ssh password is required")
		}
	}

	encrypted, err := crypto.EncryptSecret(srv.SshPassword)
	if err != nil {
		return common.NewError(err.Error())
	}
	srv.SshPassword = encrypted
	if encrypted, err = crypto.EncryptSecret(srv.SshPrivateKey); err != nil {
		return common.NewError(err.Error())
	}
	srv.SshPrivateKey = encrypted
	if encrypted, err = crypto.EncryptSecret(srv.SshKeyPassphrase); err != nil {
		return common.NewError(err.Error())
	}
	srv.SshKeyPassphrase = encrypted
	return nil
}

func (s *ManagedServerService) Create(srv *model.ManagedServer) error {
	if err := s.normalize(srv); err != nil {
		return err
	}
	if err := database.GetDB().Create(srv).Error; err != nil {
		return err
	}
	s.ProbeNowAsync(srv.Id)
	return nil
}

// ProbeNowAsync kicks off a single SSH heartbeat for one server in the
// background so a just-added / just-installed server shows its reachability and
// panel state right away instead of waiting for the next heartbeat tick. It is
// fire-and-forget: failures are recorded by UpdateSSHHeartbeat like any probe,
// and the caller's response is not blocked on the SSH round-trip.
func (s *ManagedServerService) ProbeNowAsync(id int) {
	go func() {
		srv, err := s.GetById(id)
		if err != nil || srv == nil || !srv.Enable {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), bulkVerifyTimeout)
		defer cancel()
		patch := s.ProbeSSH(ctx, srv)
		if err := s.UpdateSSHHeartbeat(id, patch); err != nil {
			logger.Warning("managed server immediate probe: update", id, "failed:", err)
		}
	}()
}

// ProbeNowForHost probes every server row for the same box as the given id, used
// after an install/uninstall that changes the panel state of a shared machine.
func (s *ManagedServerService) ProbeNowForHost(id int) {
	srv, err := s.GetById(id)
	if err != nil || srv == nil {
		return
	}
	s.ProbeNowAsync(id)
	var siblings []*model.ManagedServer
	if err := sameHostFilter(database.GetDB(), srv).Find(&siblings).Error; err != nil {
		return
	}
	for _, sib := range siblings {
		s.ProbeNowAsync(sib.Id)
	}
}

// BulkAddResult is one row's outcome from a batch add, carrying the row index so
// the UI can point the operator at the exact line that failed. Name echoes what
// was created (after the address fallback) for the success rows.
type BulkAddResult struct {
	Index   int    `json:"index" example:"0"`
	Name    string `json:"name,omitempty" example:"203.0.113.5"`
	Success bool   `json:"success" example:"true"`
	Message string `json:"message,omitempty"`
}

type BulkAddResponse struct {
	Results []BulkAddResult `json:"results"`
}

// bulkVerifyTimeout bounds each row's SSH test during a verified batch add, so
// one unreachable host can't stall the whole import.
const bulkVerifyTimeout = 15 * time.Second

// CreateBatch adds several managed servers in one call. Each row is validated
// and created independently, so one bad row does not block the others; the
// response reports per-row success/failure in the input order. A row with an
// empty name defaults to its address, matching the single-add convention.
//
// When verify is true, each row is first SSH-tested (concurrently, bounded by
// execConcurrency) and only rows that connect are created — a bad password or
// unreachable host is rejected with its error instead of being stored, matching
// what the single add enforces. When verify is false the rows are created
// without a connection test and reachability is left to the heartbeat.
func (s *ManagedServerService) CreateBatch(ctx context.Context, servers []*model.ManagedServer, verify bool) *BulkAddResponse {
	results := make([]BulkAddResult, len(servers))

	// verifyErr[i] is set to a non-nil failure message when row i fails its SSH
	// test; it stays "" for a row that connected or when verify is off.
	verifyErr := make([]string, len(servers))
	if verify {
		sshService := SSHService{}
		sem := make(chan struct{}, execConcurrency)
		var wg sync.WaitGroup
		for i, srv := range servers {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, srv *model.ManagedServer) {
				defer wg.Done()
				defer func() { <-sem }()
				// Test on a copy with a defaulted host-key mode so a blank mode
				// (trust) does not reject the connection before create normalizes it.
				probe := *srv
				if probe.SshHostKeyMode == "" {
					probe.SshHostKeyMode = "trust"
				}
				testCtx, cancel := context.WithTimeout(ctx, bulkVerifyTimeout)
				defer cancel()
				res := sshService.TestConnection(testCtx, &probe)
				if !res.Success {
					verifyErr[i] = res.Message
				}
			}(i, srv)
		}
		wg.Wait()
	}

	for i, srv := range servers {
		if strings.TrimSpace(srv.Name) == "" {
			srv.Name = strings.TrimSpace(srv.Address)
		}
		if verifyErr[i] != "" {
			results[i] = BulkAddResult{Index: i, Name: srv.Name, Success: false, Message: verifyErr[i]}
			continue
		}
		if err := s.Create(srv); err != nil {
			results[i] = BulkAddResult{Index: i, Success: false, Message: err.Error()}
			continue
		}
		results[i] = BulkAddResult{Index: i, Name: srv.Name, Success: true}
	}
	return &BulkAddResponse{Results: results}
}

func (s *ManagedServerService) Update(id int, in *model.ManagedServer) error {
	db := database.GetDB()
	existing := &model.ManagedServer{}
	if err := db.Where("id = ?", id).First(existing).Error; err != nil {
		return err
	}
	// SSH credentials are json:"-", so an edit that does not re-enter them
	// arrives empty. Carry the stored ciphertext forward before normalizing,
	// otherwise a rename would both fail validation and blank the secret.
	if in.SshPassword == "" {
		in.SshPassword = existing.SshPassword
	}
	if in.SshPrivateKey == "" {
		in.SshPrivateKey = existing.SshPrivateKey
	}
	if in.SshKeyPassphrase == "" {
		in.SshKeyPassphrase = existing.SshKeyPassphrase
	}
	// In trust-on-first-use mode the fingerprint is learned by the heartbeat, not
	// typed in the form, so an edit that leaves it blank must keep the stored
	// anchor rather than reset it — resetting would re-trust whatever the host
	// presents on the next probe, silently defeating TOFU. Only "pin" carries a
	// user-supplied fingerprint through the form.
	if in.SshHostKeyMode != "pin" && strings.TrimSpace(in.SshHostKeySha256) == "" {
		in.SshHostKeySha256 = existing.SshHostKeySha256
	}
	if err := s.normalize(in); err != nil {
		return err
	}
	updates := map[string]any{
		"name":                  in.Name,
		"remark":                in.Remark,
		"address":               in.Address,
		"enable":                in.Enable,
		"allow_private_address": in.AllowPrivateAddress,
		"ssh_port":              in.SshPort,
		"ssh_user":              in.SshUser,
		"ssh_auth_type":         in.SshAuthType,
		"ssh_password":          in.SshPassword,
		"ssh_private_key":       in.SshPrivateKey,
		"ssh_key_passphrase":    in.SshKeyPassphrase,
		"ssh_host_key_mode":     in.SshHostKeyMode,
		"ssh_host_key_sha256":   in.SshHostKeySha256,
	}
	return db.Model(model.ManagedServer{}).Where("id = ?", id).Updates(updates).Error
}

// Delete removes one managed server. Several server rows can point at the same
// physical box (same address+port+user, different names), sharing one derived
// panel Node. So deleting a row that has a linked node only deletes the node
// when this was the LAST row referencing it — otherwise the node stays, still
// managed by the remaining sibling rows. (See sameHostFilter for what "same
// box" means.)
func (s *ManagedServerService) Delete(id int) error {
	srv, err := s.GetById(id)
	if err != nil || srv == nil {
		return common.NewError("server not found")
	}
	db := database.GetDB()
	if err := db.Where("id = ?", id).Delete(&model.ManagedServer{}).Error; err != nil {
		return err
	}
	if srv.NodeId != 0 {
		var others int64
		if err := db.Model(&model.ManagedServer{}).Where("node_id = ?", srv.NodeId).Count(&others).Error; err != nil {
			return err
		}
		if others == 0 {
			return (&NodeService{}).Delete(srv.NodeId)
		}
	}
	return nil
}

// DeleteBatch removes several managed servers, each independently (a failure on
// one does not abort the rest). Node teardown follows the same last-reference
// rule as Delete. The count of rows actually removed is returned.
func (s *ManagedServerService) DeleteBatch(ids []int) int {
	removed := 0
	for _, id := range ids {
		if err := s.Delete(id); err == nil {
			removed++
		}
	}
	return removed
}

// sameHostFilter scopes a query to every managed server that reaches the same
// physical box as srv — same address, SSH port and SSH user — which is how a
// box is identified for sharing one derived panel node across differently-named
// rows. It excludes srv's own id so callers can target "the siblings".
func sameHostFilter(tx *gorm.DB, srv *model.ManagedServer) *gorm.DB {
	return tx.Where("address = ? AND ssh_port = ? AND ssh_user = ? AND id <> ?",
		srv.Address, srv.SshPort, srv.SshUser, srv.Id)
}

// linkedNodeForHost returns the panel node id already derived for srv's box by
// any sibling row, or 0 if none. Used so a second install/import on the same box
// reuses the existing node instead of installing again.
func (s *ManagedServerService) linkedNodeForHost(srv *model.ManagedServer) int {
	var sibling model.ManagedServer
	err := sameHostFilter(database.GetDB(), srv).
		Where("node_id <> 0").First(&sibling).Error
	if err != nil {
		return 0
	}
	return sibling.NodeId
}

// linkHostToNode points srv and every sibling row for the same box at nodeId, so
// the shared panel node is reflected on all of them at once.
func (s *ManagedServerService) linkHostToNode(tx *gorm.DB, srv *model.ManagedServer, nodeId int) error {
	if err := tx.Model(&model.ManagedServer{}).Where("id = ?", srv.Id).
		Update("node_id", nodeId).Error; err != nil {
		return err
	}
	return sameHostFilter(tx, srv).Model(&model.ManagedServer{}).
		Update("node_id", nodeId).Error
}

func (s *ManagedServerService) SetEnable(id int, enable bool) error {
	return database.GetDB().Model(model.ManagedServer{}).Where("id = ?", id).Update("enable", enable).Error
}

// SSHHeartbeatPatch is the result of probing a managed server. It is
// deliberately narrower than the node HeartbeatPatch: an SSH probe learns
// reachability and the host's identity, but nothing about a panel or Xray.
// Status is "reachable"/"unreachable" rather than online/offline — "online"
// drives panel-only work such as traffic sync and the CPU/memory history
// charts, and a bare SSH server feeds neither.
type SSHHeartbeatPatch struct {
	Status        string
	LastHeartbeat int64
	LatencyMs     int
	LastError     string
	OsName        string
	OsVersion     string
	HostKeySha256 string
	// PanelKnown is set only when the probe actually reached the box and could
	// evaluate whether a panel is installed, so an unreachable probe leaves the
	// last-known PanelInstalled/PanelVersion untouched instead of clearing them.
	PanelKnown     bool
	PanelInstalled bool
	PanelVersion   string
}

// UpdateSSHHeartbeat records the outcome of an SSH reachability probe. It never
// overwrites a stored host key with an empty one, so a failed probe cannot
// silently unpin a server.
func (s *ManagedServerService) UpdateSSHHeartbeat(id int, p SSHHeartbeatPatch) error {
	db := database.GetDB()
	updates := map[string]any{
		"status":         p.Status,
		"last_heartbeat": p.LastHeartbeat,
		"latency_ms":     p.LatencyMs,
		"last_error":     p.LastError,
	}
	if p.OsName != "" {
		updates["os_name"] = p.OsName
	}
	if p.OsVersion != "" {
		updates["os_version"] = p.OsVersion
	}
	if p.HostKeySha256 != "" {
		updates["ssh_host_key_sha256"] = p.HostKeySha256
	}
	// Only overwrite the panel-installed state when the probe reached the box.
	// An unreachable probe must not flip a known-installed server to "not
	// installed". Updates with a map writes false/"" explicitly, so this is
	// gated on PanelKnown rather than on the value.
	if p.PanelKnown {
		updates["panel_installed"] = p.PanelInstalled
		updates["panel_version"] = p.PanelVersion
	}
	return db.Model(model.ManagedServer{}).Where("id = ?", id).Updates(updates).Error
}

// ProbeSSH reports whether a managed server accepts a connection, learning its
// OS and host key in passing. On a trust-on-first-use server the observed key
// is returned so the caller can pin it.
func (s *ManagedServerService) ProbeSSH(ctx context.Context, srv *model.ManagedServer) SSHHeartbeatPatch {
	started := time.Now()
	sshService := SSHService{}
	result := sshService.TestConnection(ctx, srv)
	patch := SSHHeartbeatPatch{
		LastHeartbeat: time.Now().Unix(),
		LatencyMs:     int(time.Since(started).Milliseconds()),
		OsName:        result.OsName,
		OsVersion:     result.OsVersion,
	}
	if result.Success {
		patch.Status = "reachable"
		if srv.SshHostKeyMode == "trust" && strings.TrimSpace(srv.SshHostKeySha256) == "" {
			patch.HostKeySha256 = result.HostKeySha256
		}
		// The panel state is only trustworthy when we actually connected.
		patch.PanelKnown = true
		patch.PanelInstalled = result.PanelInstalled
		patch.PanelVersion = result.PanelVersion
		return patch
	}
	patch.Status = "unreachable"
	patch.LastError = result.Message
	return patch
}
