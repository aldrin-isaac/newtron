package newtrun

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
//	poll:                 # optional — retry until expectations pass
//	  timeout: 60s
//	  interval: 5s
//	expect:
//	  success_rate: 0.8   # for ping commands
//	  contains: "string"  # string match
//
// With poll:, the command is re-executed until the expectations pass or the
// timeout expires — the host-side twin of the newtron action's polling
// (dataplane readiness is asynchronous: route install, ARP resolution, ACL
// programming all land some time after the CONFIG_DB write).
type hostExecExecutor struct{}

func (e *hostExecExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	deviceName := r.resolveDevices(step)[0]
	client, ok := r.HostConns[deviceName]
	if !ok {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusError,
			Message: fmt.Sprintf("no SSH connection for host device %q", deviceName),
		}}
	}

	// Namespace is always the device name. Wrap in sh -c so that compound
	// commands (semicolons, pipes) execute entirely inside the namespace.
	cmd := fmt.Sprintf("ip netns exec %s sh -c %s", deviceName, shellQuote(step.Command))

	attempt := func() *StepResult {
		output, err := runSSHCommand(client, cmd)
		return evaluateHostExpect(step, output, err)
	}

	if step.Poll != nil {
		var last *StepResult
		pollErr := pollUntil(ctx, step.Poll.Timeout, step.Poll.Interval, func() (bool, error) {
			last = attempt()
			return last.Status == StepStatusPassed, nil
		})
		if last == nil { // context cancelled before the first attempt
			return &StepOutput{Result: &StepResult{
				Status:  StepStatusError,
				Message: fmt.Sprintf("poll aborted before first attempt: %v", pollErr),
			}}
		}
		if pollErr != nil && last.Status != StepStatusPassed {
			last.Message = fmt.Sprintf("poll %s: %s", pollErr, last.Message)
		}
		return &StepOutput{Result: last}
	}

	return &StepOutput{Result: attempt()}
}

// evaluateHostExpect applies a host-exec step's expectations to one command
// execution and returns the step result. Shared by the one-shot and poll
// paths so both judge an attempt identically.
func evaluateHostExpect(step *Step, output string, err error) *StepResult {
	if step.Expect != nil && step.Expect.SuccessRate != nil {
		rate := parsePingSuccessRate(output)
		expected := *step.Expect.SuccessRate
		if rate >= expected {
			return &StepResult{
				Status:  StepStatusPassed,
				Message: fmt.Sprintf("%.0f%% success (≥ %.0f%%)", rate*100, expected*100),
			}
		}
		return &StepResult{
			Status:  StepStatusFailed,
			Message: fmt.Sprintf("%.0f%% success (expected ≥ %.0f%%)\n%s", rate*100, expected*100, output),
		}
	}

	if step.Expect != nil && step.Expect.Contains != "" {
		if strings.Contains(output, step.Expect.Contains) {
			return &StepResult{
				Status:  StepStatusPassed,
				Message: fmt.Sprintf("output contains %q", step.Expect.Contains),
			}
		}
		return &StepResult{
			Status:  StepStatusFailed,
			Message: fmt.Sprintf("output does not contain %q\n%s", step.Expect.Contains, output),
		}
	}

	// Bare exit code check
	if err != nil {
		return &StepResult{
			Status:  StepStatusFailed,
			Message: fmt.Sprintf("command failed: %s\n%s", err, output),
		}
	}
	return &StepResult{
		Status:  StepStatusPassed,
		Message: "command succeeded",
	}
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
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
