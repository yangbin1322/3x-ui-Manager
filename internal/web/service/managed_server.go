package service

import (
	"context"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
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
	return database.GetDB().Create(srv).Error
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

func (s *ManagedServerService) Delete(id int) error {
	return database.GetDB().Where("id = ?", id).Delete(&model.ManagedServer{}).Error
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
		return patch
	}
	patch.Status = "unreachable"
	patch.LastError = result.Message
	return patch
}
