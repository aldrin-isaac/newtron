package newtlab

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

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

// LabState is persisted to ~/.newtlab/labs/<name>/state.json.
type LabState struct {
	Name string `json:"name"`
	// NetworkID is the newtron network this lab was deployed against — persisted
	// at Deploy so post-deploy operations (provision, resync) reach the SAME
	// network the lab was built from, rather than re-deriving it from the lab
	// name. The two are equal by the long-standing convention, but an operator can
	// pin a distinct id (`newtlab deploy -N <id>`); persisting it makes the binding
	// explicit and keeps provision correct when they diverge. Empty on a lab
	// deployed before this field existed — resolution falls back to the lab name.
	NetworkID string                  `json:"network_id,omitempty"`
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

// LabDir returns the state directory path for a lab name.
// Uses the cached home directory from getHomeDir().
func LabDir(name string) string {
	home, err := getHomeDir()
	if err != nil {
		// Best effort: return a relative path that will likely fail downstream
		// with a more informative error.
		return filepath.Join(".newtlab", "labs", name)
	}
	return filepath.Join(home, ".newtlab", "labs", name)
}

// SaveState writes lab state to state.json in the lab directory.
func SaveState(state *LabState) error {
	dir := LabDir(state.Name)
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

// LoadState reads lab state from state.json. Returns error if not found.
func LoadState(name string) (*LabState, error) {
	path := filepath.Join(LabDir(name), "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("newtlab: lab %s not found (no state.json)", name)
	}

	var state LabState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("newtlab: parse state.json: %w", err)
	}
	return &state, nil
}

// RemoveState deletes the entire lab state directory.
func RemoveState(name string) error {
	return os.RemoveAll(LabDir(name))
}

// ListLabs returns names of all labs with state directories.
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
