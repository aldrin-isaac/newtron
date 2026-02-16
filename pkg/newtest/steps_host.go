package newtest

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// hostExecExecutor runs a command inside a network namespace on a host device.
// The namespace name is the device name (e.g., host1, host2).
//
// YAML:
//
//	action: host-exec
//	devices: [host1]
//	command: "ping -c 5 -W 2 10.1.100.20"
//	expect:
//	  success_rate: 0.8   # for ping commands
//	  contains: "string"  # string match
type hostExecExecutor struct{}

func (e *hostExecExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	deviceName := r.resolveDevices(step)[0]
	client, ok := r.HostConns[deviceName]
	if !ok {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusError, Device: deviceName,
			Message: fmt.Sprintf("no SSH connection for host device %q", deviceName),
		}}
	}

	// Namespace is always the device name
	cmd := fmt.Sprintf("ip netns exec %s %s", deviceName, step.Command)

	output, err := runSSHCommand(client, cmd)

	// Check expectations
	if step.Expect != nil && step.Expect.SuccessRate != nil {
		rate := parsePingSuccessRate(output)
		expected := *step.Expect.SuccessRate
		if rate >= expected {
			return &StepOutput{Result: &StepResult{
				Status: StepStatusPassed, Device: deviceName,
				Message: fmt.Sprintf("%.0f%% success (≥ %.0f%%)", rate*100, expected*100),
			}}
		}
		return &StepOutput{Result: &StepResult{
			Status: StepStatusFailed, Device: deviceName,
			Message: fmt.Sprintf("%.0f%% success (expected ≥ %.0f%%)\n%s", rate*100, expected*100, output),
		}}
	}

	if step.Expect != nil && step.Expect.Contains != "" {
		if strings.Contains(output, step.Expect.Contains) {
			return &StepOutput{Result: &StepResult{
				Status: StepStatusPassed, Device: deviceName,
				Message: fmt.Sprintf("output contains %q", step.Expect.Contains),
			}}
		}
		return &StepOutput{Result: &StepResult{
			Status: StepStatusFailed, Device: deviceName,
			Message: fmt.Sprintf("output does not contain %q\n%s", step.Expect.Contains, output),
		}}
	}

	// Bare exit code check
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status: StepStatusFailed, Device: deviceName,
			Message: fmt.Sprintf("command failed: %s\n%s", err, output),
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status:  StepStatusPassed,
		Device:  deviceName,
		Message: "command succeeded",
	}}
}

// runSSHCommand executes a command on an SSH client and returns combined output.
func runSSHCommand(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	return string(output), err
}
