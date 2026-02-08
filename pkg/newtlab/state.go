package newtlab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LabState is persisted to ~/.newtlab/labs/<name>/state.json.
type LabState struct {
	Name    string                `json:"name"`
	Created time.Time             `json:"created"`
	SpecDir string                `json:"spec_dir"`
	Nodes   map[string]*NodeState `json:"nodes"`
	Links   []*LinkState          `json:"links"`
}

// NodeState tracks per-node runtime state.
type NodeState struct {
	PID            int    `json:"pid"`
	Status         string `json:"status"` // "running", "stopped", "error"
	SSHPort        int    `json:"ssh_port"`
	ConsolePort    int    `json:"console_port"`
	OriginalMgmtIP string `json:"original_mgmt_ip"`
}

// LinkState tracks per-link allocation.
type LinkState struct {
	A    string `json:"a"`    // "device:interface"
	Z    string `json:"z"`    // "device:interface"
	Port int    `json:"port"`
}

// LabDir returns the state directory path for a lab name.
func LabDir(name string) string {
	home, _ := os.UserHomeDir()
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
	home, _ := os.UserHomeDir()
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
