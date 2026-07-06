package sonic

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// Configuration Entry and Change Types
// ============================================================================

// Entry is a single CONFIG_DB entry: table + key + fields.
// Used by config generators, composite builders, and pipeline delivery.
type Entry struct {
	Table  string
	Key    string
	Fields map[string]string
}

// ConfigChange represents a single configuration change.
//
// Fields is the AFTER state — the field values written by an add/modify (empty
// for a delete). From is the BEFORE state — the field values the change
// overwrote or deleted, captured from the projection at render time (issue
// #236). Together they make a change self-describing for audit and undo
// composition: the inverse of an add is to delete the key; the inverse of a
// modify or delete is to set the key back to From. From is empty on an add
// (nothing was there) and on any change against a previously-absent key, and is
// omitted from the wire in those cases.
type ConfigChange struct {
	Table  string            `json:"table"`
	Key    string            `json:"key"`
	Type   ChangeType        `json:"type"`
	Fields map[string]string `json:"fields,omitempty"`
	From   map[string]string `json:"from,omitempty"`
}

// ChangeType represents the type of configuration change
type ChangeType string

const (
	ChangeTypeAdd    ChangeType = "add"
	ChangeTypeModify ChangeType = "modify"
	ChangeTypeDelete ChangeType = "delete"
	// ChangeTypeReplace is an in-place row replace: HSET the new Fields and
	// HDEL the fields present in From but absent from Fields — the key is
	// never DELeted, so a SONiC daemon never observes it absent (hitless
	// update; DESIGN_PRINCIPLES §48). From carries the pre-change row.
	ChangeTypeReplace ChangeType = "replace"
)

// ============================================================================
// Route Verification Types
// ============================================================================

// RouteSource indicates which Redis database a route was read from.
type RouteSource string

const (
	RouteSourceAppDB  RouteSource = "APP_DB"
	RouteSourceAsicDB RouteSource = "ASIC_DB"
)

// RouteEntry represents a route read from a device's routing table.
// Returned by Device.GetRoute (APP_DB) and Device.GetRouteASIC (ASIC_DB).
type RouteEntry struct {
	Prefix   string // "10.1.0.0/31"
	VRF      string // "default", "Vrf-customer"
	Protocol string // "bgp", "connected", "static"
	NextHops []NextHop
	Source   RouteSource // AppDB or AsicDB
}

// NextHop represents a single next-hop in a route entry.
type NextHop struct {
	IP        string // "10.0.0.1" (or "0.0.0.0" for connected)
	Interface string // "Ethernet0", "Vlan500"
}

// VerificationResult reports ChangeSet verification outcome.
// Returned by Device.VerifyChangeSet after re-reading CONFIG_DB.
type VerificationResult struct {
	Passed int                 // entries that matched
	Failed int                 // entries missing or mismatched
	Errors []VerificationError // details of each failure
}

// DeviceOp records the outcome of one Device I/O Operation newtron
// performed against a device — one Redis HSET, one Redis DEL, one daemon-
// settle wait, or one post-deliver verify re-read. These are the operations
// defined by docs/newtron/unified-pipeline-architecture.md §7 (Device I/O).
//
// DeviceOp is distinct from ChangeSet entries (which represent
// *planned* writes) and from DriftEntry (which represents
// *expected-vs-actual* deltas). It records what *happened* — the operational
// outcome of one substrate operation, captured at the moment the operation
// executed.
//
// Per DESIGN_PRINCIPLES_NEWTRON §11 (ChangeSet Universal Contract) and §46
// (HTTP API Boundary), DeviceOp is the canonical per-substrate-op
// primitive surfaced on WriteResult.DeviceOps — the wire shape that lets
// consumers see exactly which Redis command landed, what the device
// returned verbatim, and which was rejected. The vocabulary matches the
// newtcon contract verbatim.
//
// Per-Node atomicity (§13, §18): within a single Redis TxPipeline bundle,
// every redis_write and redis_delete entry MUST carry the same Result —
// either all applied or all rejected — because EXEC is atomic. Mixed
// applied/rejected for redis_write/redis_delete within one bundle is a
// contract violation. daemon_wait and verify_read are post-EXEC operations
// and MAY have mixed results.
type DeviceOp struct {
	// Seq is the zero-based ordinal of this op within the per-target apply
	// sequence. Strictly monotonically increasing.
	Seq int `json:"seq"`

	// Kind names which Device I/O Operation produced this entry.
	// Bounded enum: DeviceOpsKind* constants.
	Kind string `json:"kind"`

	// Table, Key, Fields locate the substrate the op acted on. Fields is
	// the intended write content for redis_write; nil for redis_delete and
	// daemon_wait; the re-read content for verify_read.
	Table  string            `json:"table,omitempty"`
	Key    string            `json:"key,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`

	// Result is the outcome. Bounded enum: DeviceOpsResult* constants.
	Result string `json:"result"`

	// DeviceResponse is the verbatim device/Redis-level reply observed at
	// the moment the op executed. Empty when no device transport was used
	// or when nothing meaningful was captured.
	DeviceResponse string `json:"device_response,omitempty"`

	// At is the wall-clock timestamp the op completed at.
	At time.Time `json:"at"`
}

// DeviceOpsKind constants — the bounded enum for DeviceOp.Kind.
// Vocabulary matches the newtcon contract verbatim.
const (
	DeviceOpsKindRedisWrite  = "redis_write"
	DeviceOpsKindRedisDelete = "redis_delete"
	DeviceOpsKindDaemonWait  = "daemon_wait"
	DeviceOpsKindVerifyRead  = "verify_read"
)

// DeviceOpsResult constants — the bounded enum for DeviceOp.Result.
// Vocabulary matches the newtcon contract verbatim.
const (
	DeviceOpsResultApplied  = "applied"
	DeviceOpsResultRejected = "rejected"
	DeviceOpsResultSkipped  = "skipped"
)

// VerificationError describes a single verification failure.
//
// DeviceResponse carries the verbatim device-side reply observed at the
// moment the mismatch was detected. For field mismatches it is the full
// HGETALL content formatted as sorted `key=value` pairs; for missing-key
// or still-present cases it is the verbatim Redis-level status. Empty
// when verification ran without device transport (loopback mode).
type VerificationError struct {
	Table          string
	Key            string
	Field          string
	Expected       string
	Actual         string // "" if missing
	DeviceResponse string
}

// NeighEntry represents a neighbor (ARP/NDP) entry read from a device.
// Returned by Node.GetNeighbor (STATE_DB NEIGH_TABLE).
type NeighEntry struct {
	IP        string // "10.20.0.1"
	Interface string // "Ethernet1", "Vlan100"
	MAC       string // "aa:bb:cc:dd:ee:ff"
	Family    string // "IPv4", "IPv6"
}

// ============================================================================
// SSH Tunnel
// ============================================================================

// SSHTunnel forwards a local TCP port to a remote address through an SSH connection.
// Used to access Redis (127.0.0.1:6379) inside SONiC containers via SSH,
// since Redis has no authentication and port 6379 is not forwarded by QEMU.
type SSHTunnel struct {
	localAddr string // "127.0.0.1:<port>"
	sshClient *ssh.Client
	listener  net.Listener
	done      chan struct{}
	wg        sync.WaitGroup
}

// DefaultConnectTimeout bounds the device SSH dial (TCP connect + handshake)
// when a caller sets no shorter deadline — long enough to wait out a device that
// is mid-reconfiguration (e.g. during provisioning).
const DefaultConnectTimeout = 30 * time.Second

// LivenessConnectTimeout is the short dial bound a liveness/read path uses so an
// unreachable or mid-provision device fails fast — a status poll must not hang
// for the whole operation — instead of blocking for DefaultConnectTimeout.
const LivenessConnectTimeout = 3 * time.Second

type connectTimeoutKey struct{}

// WithConnectTimeout bounds the device SSH dial for connections opened under the
// returned context. Read/liveness paths set LivenessConnectTimeout (fail fast);
// config-mutating ops leave it unset so a device briefly unreachable
// mid-reconfigure is waited for (DefaultConnectTimeout). It bounds only the dial
// — an already-established tunnel's reads are unaffected.
func WithConnectTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, connectTimeoutKey{}, d)
}

func connectTimeoutFromContext(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(connectTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return DefaultConnectTimeout
}

// NewSSHTunnel dials SSH on host:port and opens a local listener on a random port.
// Connections to the local port are forwarded to 127.0.0.1:6379 inside the SSH host.
// If port is 0, defaults to 22. The dial is bounded by connectTimeoutFromContext(ctx)
// so a liveness read can fail fast on an unreachable device (WithConnectTimeout).
func NewSSHTunnel(ctx context.Context, host, user, pass string, port int) (*SSHTunnel, error) {
	if port == 0 {
		port = 22
	}
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		// Lab/test environment — production would need known_hosts verification.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         connectTimeoutFromContext(ctx),
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	util.Logger.Warnf("SSH tunnel to %s: host key verification disabled (InsecureIgnoreHostKey)", addr)
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s@%s: %w", user, addr, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("local listen: %w", err)
	}

	t := &SSHTunnel{
		localAddr: listener.Addr().String(),
		sshClient: sshClient,
		listener:  listener,
		done:      make(chan struct{}),
	}

	t.wg.Add(1)
	go t.acceptLoop()

	return t, nil
}

// LocalAddr returns the local address (e.g. "127.0.0.1:54321") that forwards
// to Redis inside the SSH host.
func (t *SSHTunnel) LocalAddr() string {
	return t.localAddr
}

// Close stops the listener, closes the SSH connection, and waits for
// all forwarding goroutines to finish.
func (t *SSHTunnel) Close() error {
	close(t.done)
	t.listener.Close()
	// Close SSH client first to tear down all forwarded connections,
	// unblocking any io.Copy goroutines waiting on remote reads.
	t.sshClient.Close()
	t.wg.Wait()
	return nil
}

func (t *SSHTunnel) acceptLoop() {
	defer t.wg.Done()
	for {
		local, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
				return
			default:
				continue
			}
		}
		t.wg.Add(1)
		go t.forward(local)
	}
}

// SSHClient returns the underlying ssh.Client for opening command sessions.
// Used by newtrun's verifyPingExecutor and sshCommandExecutor to run commands
// inside the device (e.g., "ping", "show interfaces status") via ssh.Session.
func (t *SSHTunnel) SSHClient() *ssh.Client { return t.sshClient }

// ExecCommand runs a command on the remote device via SSH and returns the combined output.
// The SSH session is created per-call (stateless).
func (t *SSHTunnel) ExecCommand(cmd string) (string, error) {
	session, err := t.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(output), fmt.Errorf("SSH exec '%s': %w", cmd, err)
	}
	return string(output), nil
}

// ExecCommandContext runs a command on the remote device via SSH with context cancellation.
// If the context is cancelled or times out, the SSH session is killed and an error is returned.
func (t *SSHTunnel) ExecCommandContext(ctx context.Context, cmd string) (string, error) {
	session, err := t.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	var outputBuf bytes.Buffer
	session.Stdout = &outputBuf
	session.Stderr = &outputBuf

	// Start command in background
	if err := session.Start(cmd); err != nil {
		return "", fmt.Errorf("SSH start '%s': %w", cmd, err)
	}

	// Wait for command or context cancellation
	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context cancelled — kill session
		session.Signal(ssh.SIGKILL)
		session.Close()
		<-done // wait for goroutine to finish
		return outputBuf.String(), fmt.Errorf("SSH exec '%s': %w", cmd, ctx.Err())
	case err := <-done:
		if err != nil {
			return outputBuf.String(), fmt.Errorf("SSH exec '%s': %w", cmd, err)
		}
		return outputBuf.String(), nil
	}
}

func (t *SSHTunnel) forward(local net.Conn) {
	defer t.wg.Done()
	defer local.Close()

	remote, err := t.sshClient.Dial("tcp", "127.0.0.1:6379")
	if err != nil {
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(remote, local)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(local, remote)
		done <- struct{}{}
	}()
	<-done
}
