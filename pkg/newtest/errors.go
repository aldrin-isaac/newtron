package newtest

import "fmt"

// InfraError represents an infrastructure-level error (deploy, connect, SSH).
type InfraError struct {
	Op     string // "deploy", "connect", "ssh"
	Device string // device name (or "" for topology-level)
	Err    error
}

func (e *InfraError) Error() string {
	if e.Device != "" {
		return fmt.Sprintf("newtest: %s %s: %v", e.Op, e.Device, e.Err)
	}
	return fmt.Sprintf("newtest: %s: %v", e.Op, e.Err)
}

func (e *InfraError) Unwrap() error {
	return e.Err
}

// StepError represents a step execution error.
type StepError struct {
	Step   string
	Action StepAction
	Err    error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("newtest: step %s (%s): %v", e.Step, e.Action, e.Err)
}

func (e *StepError) Unwrap() error {
	return e.Err
}
