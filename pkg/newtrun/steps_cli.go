package newtrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// newtronCLIExecutor runs newtron CLI commands as subprocesses.
// Works like host-exec: run a command, capture output, evaluate expect.
//
// The command field contains CLI args after "newtron <device>", with
// {{device}} expanded per device. When expect.jq is set, --json is
// added automatically. Write commands include -x directly in the
// command string.
//
// YAML:
//
//	action: newtron-cli
//	devices: [leaf1]
//	command: "vlan list"
//	expect:
//	  jq: '.[0].vlan_id == 100'
//
//	action: newtron-cli
//	devices: [leaf1]
//	command: "vlan create 100 -x"
//
//	action: newtron-cli
//	command: "service list"
//	expect:
//	  contains: "transit"
//
// The newtron binary is resolved from PATH. Server URL is inherited
// from the runner's configuration.
type newtronCLIExecutor struct{}

func (e *newtronCLIExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	// Device routing: if devices: is set OR {{device}} appears in the command,
	// run per-device. The newtron CLI pattern is "newtron <device> <command>"
	// where device is prepended as the first arg — it doesn't need to appear
	// as a {{device}} template in the command string.
	if step.Devices.All || len(step.Devices.Devices) > 0 || hasDeviceTemplate(step.Command) {
		return r.executeForDevices(step, func(name string) (string, error) {
			return e.runCLI(ctx, r, step, name)
		})
	}

	// No devices — network-scoped command (e.g., "service list").
	msg, err := e.runCLI(ctx, r, step, "")
	if err != nil {
		return &StepOutput{Result: &StepResult{Status: StepStatusFailed, Message: err.Error()}}
	}
	return &StepOutput{Result: &StepResult{Status: StepStatusPassed, Message: msg}}
}

func (e *newtronCLIExecutor) runCLI(ctx context.Context, r *Runner, step *Step, device string) (string, error) {
	cmdStr := step.Command
	if device != "" {
		cmdStr = strings.ReplaceAll(cmdStr, "{{device}}", device)
	}

	// Build args: [device] <command...> [--json] --server <url>
	var args []string
	if device != "" {
		args = append(args, device)
	}
	args = append(args, strings.Fields(cmdStr)...)
	if step.Expect != nil && step.Expect.JQ != "" {
		args = append(args, "--json")
	}
	args = append(args, "--server", r.ServerURL)

	bin := "newtron"
	if p, err := exec.LookPath("newtron"); err == nil {
		bin = p
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	outStr := stdout.String()
	errStr := stderr.String()

	// On command failure, include stderr in the error message.
	if err != nil && (step.Expect == nil || step.Expect.JQ == "") {
		combined := outStr
		if errStr != "" {
			combined = errStr + outStr
		}
		return "", fmt.Errorf("command failed: %s\n%s", err, combined)
	}

	// Evaluate expectations against stdout only (stderr has warnings/logs).
	if step.Expect != nil && step.Expect.JQ != "" {
		var data json.RawMessage
		if jsonErr := json.Unmarshal(stdout.Bytes(), &data); jsonErr != nil {
			if err != nil {
				return "", fmt.Errorf("command failed: %s\n%s%s", err, errStr, outStr)
			}
			return "", fmt.Errorf("cannot parse JSON output: %s\n%s", jsonErr, outStr)
		}
		return evalJQ(step.Expect.JQ, data, bin, cmdStr)
	}

	if step.Expect != nil && step.Expect.Contains != "" {
		if strings.Contains(outStr, step.Expect.Contains) {
			return fmt.Sprintf("output contains %q", step.Expect.Contains), nil
		}
		return "", fmt.Errorf("output does not contain %q\n%s", step.Expect.Contains, outStr)
	}

	return "ok", nil
}
