package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"

	"golang.org/x/crypto/ssh"
)

const (
	sshDialTimeout    = 10 * time.Second
	sshCommandTimeout = 30 * time.Second

	// execDefaultTimeout is used when a caller does not specify one; execMaxTimeout
	// is the hard ceiling so a hung command cannot hold a connection open forever.
	execDefaultTimeout = 30 * time.Second
	execMaxTimeout     = 5 * time.Minute

	// execOutputCap bounds how much combined output is retained per execution, so
	// a single large-output command (e.g. cat of a big file) cannot bloat the
	// audit table.
	execOutputCap = 64 * 1024

	// execConcurrency caps how many servers a batch command runs on at once. It is
	// deliberately lower than the heartbeat's 32: each execution is a real
	// side-effecting action, not a read-only probe, so a smaller fan-out limits
	// the blast radius of a mistaken command.
	execConcurrency = 8
)

// SSHService opens SSH sessions to managed servers: machines reachable over SSH
// that may not have a 3x-ui panel installed at all.
type SSHService struct{}

// SSHDialResult reports the outcome of a connection attempt. HostKeySha256 is
// the key the server actually presented, so a caller doing trust-on-first-use
// can persist it, and a caller testing a connection can show the operator the
// fingerprint they are about to trust.
type SSHDialResult struct {
	HostKeySha256 string
	Client        *ssh.Client
}

// FormatHostKeyFingerprint renders a host key in the sha256:BASE64 form that
// OpenSSH prints, so an operator can compare it against `ssh-keyscan` output.
func FormatHostKeyFingerprint(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "sha256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func sshAuthMethods(srv *model.ManagedServer) ([]ssh.AuthMethod, error) {
	switch srv.SshAuthType {
	case "key":
		privateKey, err := crypto.DecryptSecret(srv.SshPrivateKey)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(privateKey) == "" {
			return nil, fmt.Errorf("ssh private key is required")
		}
		passphrase, err := crypto.DecryptSecret(srv.SshKeyPassphrase)
		if err != nil {
			return nil, err
		}
		var signer ssh.Signer
		if passphrase == "" {
			signer, err = ssh.ParsePrivateKey([]byte(privateKey))
		} else {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
		}
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, fmt.Errorf("ssh private key is passphrase-protected; a passphrase is required")
			}
			return nil, fmt.Errorf("ssh private key could not be parsed")
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	default:
		password, err := crypto.DecryptSecret(srv.SshPassword)
		if err != nil {
			return nil, err
		}
		if password == "" {
			return nil, fmt.Errorf("ssh password is required")
		}
		return []ssh.AuthMethod{ssh.Password(password)}, nil
	}
}

// hostKeyCallback enforces the server's SshHostKeyMode. "pin" verifies against
// the stored fingerprint and fails on mismatch; "trust" accepts any key on the
// first connect and pins it afterwards; "skip" accepts anything. Accepting an
// unknown host key means handing the credential to whoever answered on the
// port, so "trust" records what it saw and "pin" refuses to be silently
// re-pointed.
func hostKeyCallback(srv *model.ManagedServer, seen *string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := FormatHostKeyFingerprint(key)
		*seen = fingerprint
		switch srv.SshHostKeyMode {
		case "skip":
			return nil
		case "pin":
			expected := strings.TrimSpace(srv.SshHostKeySha256)
			if expected == "" {
				return fmt.Errorf("host key pinning is enabled but no fingerprint is stored for this server")
			}
			if !strings.EqualFold(expected, fingerprint) {
				return fmt.Errorf("host key mismatch: expected %s, server presented %s", expected, fingerprint)
			}
			return nil
		default:
			pinned := strings.TrimSpace(srv.SshHostKeySha256)
			if pinned != "" && !strings.EqualFold(pinned, fingerprint) {
				return fmt.Errorf("host key changed: expected %s, server presented %s", pinned, fingerprint)
			}
			return nil
		}
	}
}

// Dial opens an SSH connection to the server. The caller owns the returned
// client and must Close it.
func (s *SSHService) Dial(ctx context.Context, srv *model.ManagedServer) (*SSHDialResult, error) {
	host, err := netsafe.NormalizeHost(srv.Address)
	if err != nil {
		return nil, err
	}
	port := srv.SshPort
	if port <= 0 {
		port = 22
	}
	authMethods, err := sshAuthMethods(srv)
	if err != nil {
		return nil, err
	}
	user := strings.TrimSpace(srv.SshUser)
	if user == "" {
		return nil, fmt.Errorf("ssh username is required")
	}

	var seenHostKey string
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback(srv, &seenHostKey),
		Timeout:         sshDialTimeout,
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	// Resolve and dial through the shared SSRF guard so a hostname cannot escape
	// the private-address protection: it checks every resolved IP against
	// IsBlockedIP unless AllowPrivateAddress is set, exactly as the HTTP node
	// probe does. A bare net.Dialer would trust whatever the OS resolver
	// returned and leak the SSH credential to an internal host.
	dialCtx := netsafe.ContextWithAllowPrivate(ctx, srv.AllowPrivateAddress)
	conn, err := netsafe.SSRFGuardedDialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return &SSHDialResult{HostKeySha256: seenHostKey}, err
	}
	_ = conn.SetDeadline(time.Time{})
	return &SSHDialResult{
		HostKeySha256: seenHostKey,
		Client:        ssh.NewClient(clientConn, chans, reqs),
	}, nil
}

// RunCommand executes cmd on the server and returns its combined output.
func (s *SSHService) RunCommand(ctx context.Context, srv *model.ManagedServer, cmd string) (string, error) {
	res, err := s.Dial(ctx, srv)
	if err != nil {
		return "", err
	}
	defer res.Client.Close()
	return runOnClient(ctx, res.Client, cmd)
}

func runOnClient(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	// Give the command an immediately-EOF stdin and no PTY. A command that waits
	// on input (an unguarded "apt install" prompt, a read) then hits EOF and
	// fails fast instead of blocking until the timeout ceiling. This is a
	// single-shot command model, not an interactive terminal — the timeout is
	// the backstop for anything that ignores EOF and reads from /dev/tty.
	session.Stdin = strings.NewReader("")

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, runErr := session.CombinedOutput(cmd)
		done <- result{out: out, err: runErr}
	}()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case r := <-done:
		return string(r.out), r.err
	}
}

// exitCodeOf extracts the remote exit status from a run error. An *ssh.ExitError
// carries the command's own exit code; anything else (a closed channel, a signal
// kill) has no meaningful code, reported as -1.
func exitCodeOf(err error) int {
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitStatus()
	}
	return -1
}

// SSHTestResult is what the panel shows after a connection test.
type SSHTestResult struct {
	Success        bool   `json:"success" example:"true"`
	Message        string `json:"message,omitempty" example:"Authentication failed"`
	HostKeySha256  string `json:"hostKeySha256,omitempty" example:"sha256:abc123"`
	OsName         string `json:"osName,omitempty" example:"Ubuntu"`
	OsVersion      string `json:"osVersion,omitempty" example:"24.04"`
	PanelInstalled bool   `json:"panelInstalled" example:"true"`
	PanelVersion   string `json:"panelVersion,omitempty" example:"v2.6.0"`
}

// panelVersionCommand asks the installed x-ui binary for its version. It targets
// the binary at its install path (not the global `x-ui` management wrapper) so a
// box with no panel simply fails the command rather than printing the wrapper's
// menu; a non-existent binary makes `bash -c` exit non-zero, which we treat as
// "no panel". Output is a single line like "x-ui version v2.6.0".
const panelVersionCommand = "/usr/local/x-ui/x-ui -v"

// TestConnection verifies the server's SSH credentials and reports the host key,
// detected OS, and whether a 3x-ui panel is already installed. It never returns
// the credential itself, and its error text is the transport's, which does not
// echo the password or key.
func (s *SSHService) TestConnection(ctx context.Context, srv *model.ManagedServer) *SSHTestResult {
	res, err := s.Dial(ctx, srv)
	if err != nil {
		out := &SSHTestResult{Success: false, Message: err.Error()}
		if res != nil {
			out.HostKeySha256 = res.HostKeySha256
		}
		return out
	}
	defer res.Client.Close()

	out := &SSHTestResult{Success: true, HostKeySha256: res.HostKeySha256}
	// Bound the OS probe on its own timeout so a host that accepts the
	// connection but stalls on the command can't hold the test open for the
	// whole parent budget.
	osCtx, cancel := context.WithTimeout(ctx, sshCommandTimeout)
	defer cancel()
	if release, err := runOnClient(osCtx, res.Client, "cat /etc/os-release"); err == nil {
		name, version := parseOsRelease(release)
		out.OsName = name
		out.OsVersion = version
	}
	// Reuse the same connection to learn whether a panel is installed. A
	// failure (no binary, non-zero exit) just means "not installed" and must
	// never fail the overall test, so the error is intentionally ignored.
	verCtx, verCancel := context.WithTimeout(ctx, sshCommandTimeout)
	defer verCancel()
	if verOut, err := runOnClient(verCtx, res.Client, panelVersionCommand); err == nil {
		if version := parsePanelVersion(verOut); version != "" {
			out.PanelInstalled = true
			out.PanelVersion = version
		}
	}
	return out
}

// panelVersionToken matches a release token like "v2.6.0" or "2.6.0" so noise
// (a "command not found" message, a shell banner) is not mistaken for a version.
var panelVersionToken = regexp.MustCompile(`^v?\d+\.\d+`)

// parsePanelVersion extracts a version token from `x-ui -v` output. The binary
// prints a line such as "x-ui version v2.6.0"; we scan its whitespace fields for
// the first that looks like a version. Returns "" when none is present so a
// garbled response, an error message, or empty output reads as "no panel".
func parsePanelVersion(out string) string {
	for _, line := range strings.Split(stripANSI(out), "\n") {
		for _, field := range strings.Fields(strings.TrimSpace(line)) {
			if panelVersionToken.MatchString(field) {
				return field
			}
		}
	}
	return ""
}

// parseOsRelease pulls the distro name and version out of /etc/os-release,
// preferring the human-readable NAME/VERSION_ID pair.
func parseOsRelease(content string) (string, string) {
	fields := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		fields[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	name := fields["NAME"]
	if name == "" {
		name = fields["ID"]
	}
	version := fields["VERSION_ID"]
	if version == "" {
		version = fields["VERSION"]
	}
	return name, version
}
