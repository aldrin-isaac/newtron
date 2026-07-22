package newtlab

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLabNotFound reports that a lab has no runtime state on this host — the
// network may exist but was never deployed here (no state.json). It is the
// distinguishable "absent resource" case: API handlers map it to 404, while
// every other LoadState failure (unreadable or unparsable state) is a genuine
// server-side error (500). Wrapped, not returned bare, so the message keeps
// naming the lab.
var ErrLabNotFound = errors.New("not found (no state.json)")

// ErrNodeNotFound reports that a node name does not exist in a lab's state or
// specs — the same absent-resource class as ErrLabNotFound, mapped to 404 by
// API handlers.
var ErrNodeNotFound = errors.New("not found")

// NewTelemetryToken mints a 256-bit random token for LabState.TelemetryToken —
// the per-lab credential newtlink presents when pushing BridgeStats. Drawn from
// crypto/rand; a rand failure surfaces rather than falling back to a weaker
// source (mirrors sessionkey.Mint). Called once per deploy; the value is
// persisted in state.json so it survives a server restart.
func NewTelemetryToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("newtlab: mint telemetry token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// LabState is persisted to ~/.newtlab/labs/<network-id>/state.json.
type LabState struct {
	// NetworkID is the lab's sole identity: the newtron network it realizes
	// virtually (#396). It is the lab's address — the state-directory name and
	// the /newtlab/v1/labs/{networkID} key — so it is authoritative from the
	// path, not from this persisted copy: LoadState stamps it from the directory.
	// A lab has no second "name" that could drift from its network-id.
	NetworkID string                  `json:"network_id"`
	Created    time.Time               `json:"created"`
	Dir    string                  `json:"dir"`
	SSHKeyPath string                  `json:"ssh_key_path,omitempty"` // path to lab Ed25519 private key
	Nodes      map[string]*NodeState   `json:"nodes"`
	Links      []*LinkState            `json:"links"`
	Bridges    map[string]*BridgeState `json:"bridges,omitempty"` // host ("" = local) → bridge info
	// TelemetryToken is the per-lab credential newtlink presents (as a Bearer)
	// when pushing BridgeStats to newtlab-server. Minted at deploy and persisted
	// here so it survives a server restart: on rehydration the newtlab engine
	// re-reads it and keeps accepting the running newtlink's pushes without a
	// redeploy. It authorizes only this lab's stats push (least privilege), not
	// any user-facing operation. See handlePushBridgeStats.
	TelemetryToken string `json:"telemetry_token,omitempty"`
}

// NodeState tracks per-node runtime state.
type NodeState struct {
	PID            int    `json:"pid"`
	Status         string `json:"status"`          // "running", "stopped", "error"
	Phase          string `json:"phase,omitempty"` // deploy phase: "booting", "bootstrapping", "patching"
	DeviceType     string `json:"device_type,omitempty"` // "host" for non-switch devices, "host-vm" for coalesced VM
	Image          string `json:"image,omitempty"`       // VM image path
	SSHPort        int    `json:"ssh_port"`
	ConsolePort    int    `json:"console_port"`
	OriginalMgmtIP string `json:"original_mgmt_ip"`
	Host           string `json:"host,omitempty"`      // host name (empty = local)
	HostIP         string `json:"host_ip,omitempty"`   // host IP address (empty = 127.0.0.1)
	SSHUser        string `json:"ssh_user,omitempty"`   // SSH username (for cmd_ssh.go)
	VMName         string `json:"vm_name,omitempty"`    // virtual hosts: parent VM name
	Namespace      string `json:"namespace,omitempty"` // virtual hosts: netns name
}

// BridgeState tracks a per-host bridge process. As of #118 newtlink no
// longer listens on a stats port — it pushes BridgeStats to
// newtlab-server every pushInterval — so there is no stats address to
// record here.
type BridgeState struct {
	PID    int    `json:"pid"`
	HostIP string `json:"host_ip,omitempty"` // "" for local
}

// LinkState tracks per-link allocation.
type LinkState struct {
	A          string `json:"a"`                     // "device:interface"
	Z          string `json:"z"`                     // "device:interface"
	APort      int    `json:"a_port"`                // bridge worker A-side listen port
	ZPort      int    `json:"z_port"`                // bridge worker Z-side listen port
	WorkerHost string `json:"worker_host,omitempty"` // host running the bridge worker
}

// LabDir returns the state directory path for a lab, keyed by its network-id.
// Uses the cached home directory from getHomeDir().
func LabDir(networkID string) string {
	home, err := getHomeDir()
	if err != nil {
		// Best effort: return a relative path that will likely fail downstream
		// with a more informative error.
		return filepath.Join(".newtlab", "labs", networkID)
	}
	return filepath.Join(home, ".newtlab", "labs", networkID)
}

// SaveState writes lab state to state.json in the lab directory.
func SaveState(state *LabState) error {
	dir := LabDir(state.NetworkID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("newtlab: create state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "    ")
	if err != nil {
		return fmt.Errorf("newtlab: marshal state: %w", err)
	}

	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("newtlab: write state: %w", err)
	}
	return nil
}

// LoadState reads lab state from state.json. A missing state.json wraps
// ErrLabNotFound (the lab was never deployed here — an absent resource, not a
// failure); any other read error is surfaced as the real error it is.
func LoadState(networkID string) (*LabState, error) {
	path := filepath.Join(LabDir(networkID), "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("newtlab: lab %s %w", networkID, ErrLabNotFound)
		}
		return nil, fmt.Errorf("newtlab: read lab %s state: %w", networkID, err)
	}

	var state LabState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("newtlab: parse state.json: %w", err)
	}
	// The directory is the lab's address, and its address is its identity (#396):
	// stamp NetworkID from the path so it is correct regardless of what the file
	// carries. This is the single source of a lab's identity (§27).
	state.NetworkID = networkID
	return &state, nil
}

// RemoveState deletes the entire lab state directory.
func RemoveState(networkID string) error {
	return os.RemoveAll(LabDir(networkID))
}

// ListLabs returns the network-ids of all labs with state directories.
func ListLabs() ([]string, error) {
	home, err := getHomeDir()
	if err != nil {
		return nil, fmt.Errorf("newtlab: list labs: %w", err)
	}
	labsDir := filepath.Join(home, ".newtlab", "labs")

	entries, err := os.ReadDir(labsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("newtlab: list labs: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
