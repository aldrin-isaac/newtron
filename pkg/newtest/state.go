package newtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// RunStatus is the lifecycle state of a suite run.
type RunStatus string

const (
	StatusRunning  RunStatus = "running"
	StatusPausing  RunStatus = "pausing"
	StatusPaused   RunStatus = "paused"
	StatusComplete RunStatus = "complete"
	StatusAborted  RunStatus = "aborted"
	// Note: "failed" is intentionally StatusRunFailed to avoid collision with
	// the step-level StatusFailed constant.
	StatusRunFailed RunStatus = "failed"
)

// RunState is persisted to ~/.newtron/newtest/<suite>/state.json.
type RunState struct {
	Suite     string          `json:"suite"`
	SuiteDir  string          `json:"suite_dir"`
	Topology  string          `json:"topology"`
	Platform  string          `json:"platform"`
	LabName   string          `json:"lab_name"`
	PID       int             `json:"pid"`
	Status    RunStatus       `json:"status"`
	Started   time.Time       `json:"started"`
	Updated   time.Time       `json:"updated"`
	Scenarios []ScenarioState `json:"scenarios"`
}

// ScenarioState tracks the outcome of a single scenario within a suite run.
type ScenarioState struct {
	Name     string `json:"name"`
	Status   string `json:"status"`   // "PASS","FAIL","SKIP","ERROR","" (pending)
	Duration string `json:"duration"` // e.g. "2s", "15s"
}

// StateDir returns the state directory path for a suite name.
func StateDir(suite string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".newtron", "newtest", suite)
}

// SuiteName extracts the suite name from a directory path.
func SuiteName(dir string) string {
	return filepath.Base(dir)
}

// SaveRunState writes run state to state.json in the suite state directory.
func SaveRunState(state *RunState) error {
	state.Updated = time.Now()
	dir := StateDir(state.Suite)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("newtest: create state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "    ")
	if err != nil {
		return fmt.Errorf("newtest: marshal state: %w", err)
	}

	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("newtest: write state: %w", err)
	}
	return nil
}

// LoadRunState reads run state from state.json. Returns nil, nil if not found.
func LoadRunState(suite string) (*RunState, error) {
	path := filepath.Join(StateDir(suite), "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("newtest: read state: %w", err)
	}

	var state RunState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("newtest: parse state.json: %w", err)
	}
	return &state, nil
}

// RemoveRunState deletes the entire suite state directory.
func RemoveRunState(suite string) error {
	return os.RemoveAll(StateDir(suite))
}

// ListSuiteStates returns names of all suites with state directories.
func ListSuiteStates() ([]string, error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".newtron", "newtest")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("newtest: list suites: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// AcquireLock checks for an existing active runner and claims the lock.
// Returns an error if another process is actively running this suite.
func AcquireLock(state *RunState) error {
	existing, err := LoadRunState(state.Suite)
	if err != nil {
		return err
	}

	if existing != nil && existing.PID != 0 && isProcessAlive(existing.PID) {
		return fmt.Errorf("suite %s already running (pid %d)", state.Suite, existing.PID)
	}

	state.PID = os.Getpid()
	return SaveRunState(state)
}

// ReleaseLock clears the PID and saves state.
func ReleaseLock(state *RunState) error {
	state.PID = 0
	return SaveRunState(state)
}

// CheckPausing returns true if the suite's status is "pausing".
func CheckPausing(suite string) bool {
	state, err := LoadRunState(suite)
	if err != nil || state == nil {
		return false
	}
	return state.Status == StatusPausing
}

// isProcessAlive checks if a process with the given PID exists.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}
