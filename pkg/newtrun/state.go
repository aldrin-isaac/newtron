package newtrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// DateTimeFormat is the timestamp format used in reports and status output.
const DateTimeFormat = "2006-01-02 15:04:05"

// SuiteStatus is the lifecycle state of a suite run.
type SuiteStatus string

const (
	SuiteStatusRunning  SuiteStatus = "running"
	SuiteStatusPausing  SuiteStatus = "pausing"
	SuiteStatusPaused   SuiteStatus = "paused"
	SuiteStatusComplete SuiteStatus = "complete"
	SuiteStatusAborted  SuiteStatus = "aborted"
	SuiteStatusFailed   SuiteStatus = "failed"
)

// RunState is persisted to ~/.newtron/newtrun/<suite>/state.json.
type RunState struct {
	Suite     string          `json:"suite"`
	SuiteDir  string          `json:"suite_dir"`
	Topology  string          `json:"topology"`
	Platform  string          `json:"platform"`
	PID       int             `json:"pid"`
	Status    SuiteStatus     `json:"status"`
	Started   time.Time       `json:"started"`
	Updated   time.Time       `json:"updated"`
	Finished  time.Time       `json:"finished,omitempty"` // set when suite completes (pass/fail/error)
	Scenarios []ScenarioState `json:"scenarios"`
}

// ScenarioState tracks the outcome of a single scenario within a suite run.
type ScenarioState struct {
	Name             string      `json:"name"`
	Description      string      `json:"description,omitempty"`         // scenario intent (from YAML)
	Status           string      `json:"status"`                        // "PASS","FAIL","SKIP","ERROR","running","" (pending)
	Duration         string      `json:"duration"`                      // e.g. "2s", "15s"
	CurrentStep       string      `json:"current_step,omitempty"`        // step name while in-progress
	CurrentStepAction string      `json:"current_step_action,omitempty"` // step action while in-progress
	CurrentStepIndex  int         `json:"current_step_index,omitempty"`  // 0-based step index
	TotalSteps       int         `json:"total_steps,omitempty"`         // total steps in scenario
	Requires         []string    `json:"requires,omitempty"`            // dependency scenario names
	SkipReason       string      `json:"skip_reason,omitempty"`         // reason for skip
	Steps            []StepState `json:"steps,omitempty"`               // per-step results (populated incrementally)
}

// StepState tracks the outcome of a single step within a scenario.
type StepState struct {
	Name     string `json:"name"`
	Action   string `json:"action"`
	Status   string `json:"status"`   // "PASS","FAIL","SKIP","ERROR"
	Duration string `json:"duration"` // e.g. "2s", "<1s"
	Message  string `json:"message,omitempty"`
}

// StateDir returns the state directory path for a suite name.
func StateDir(suite string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("newtrun: user home dir: %w", err)
	}
	return filepath.Join(home, ".newtron", "newtrun", suite), nil
}

// SuiteName extracts the suite name from a directory path.
func SuiteName(dir string) string {
	return filepath.Base(dir)
}

// SaveRunState writes run state to state.json in the suite state directory.
func SaveRunState(state *RunState) error {
	state.Updated = time.Now()
	dir, err := StateDir(state.Suite)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("newtrun: create state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "    ")
	if err != nil {
		return fmt.Errorf("newtrun: marshal state: %w", err)
	}

	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("newtrun: write state: %w", err)
	}
	return nil
}

// LoadRunState reads run state from state.json. Returns nil, nil if not found.
func LoadRunState(suite string) (*RunState, error) {
	dir, err := StateDir(suite)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("newtrun: read state: %w", err)
	}

	var state RunState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("newtrun: parse state.json: %w", err)
	}
	return &state, nil
}

// RemoveRunState deletes the entire suite state directory.
func RemoveRunState(suite string) error {
	dir, err := StateDir(suite)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// ListSuiteStates returns names of all suites with state directories.
// Only returns suites that have actual suite directories in the suites base directory.
func ListSuiteStates() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("newtrun: user home dir: %w", err)
	}
	dir := filepath.Join(home, ".newtron", "newtrun")

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("newtrun: list suites: %w", err)
	}

	// Determine suites base directory (env > default)
	suitesBase := os.Getenv("NEWTRUN_SUITES_BASE")
	if suitesBase == "" {
		suitesBase = "newtrun/suites"
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Check if actual suite directory exists
		suitePath := filepath.Join(suitesBase, e.Name())
		if info, err := os.Stat(suitePath); err == nil && info.IsDir() {
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

	if existing != nil && existing.PID != 0 && IsProcessAlive(existing.PID) {
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
	return state.Status == SuiteStatusPausing
}

// IsProcessAlive checks if a process with the given PID exists.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}
