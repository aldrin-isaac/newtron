package newtrun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/itchyny/gojq"
)

// newtronExecutor implements the generic "newtron" action — a single step type
// that can make arbitrary HTTP calls to newtron-server with template-expanded
// URLs, optional polling, batch mode, and jq-based response evaluation.
//
// URL templates support {{device}} (expanded per-device by executeForDevices/
// pollForDevices). The network prefix /network/<id> is implicit — URLs start
// from the path after the network segment (e.g., /node/{{device}}/vlan).
type newtronExecutor struct{}

func (e *newtronExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	// Batch mode: sequential calls, no per-device expansion within batch.
	if len(step.Batch) > 0 {
		return e.executeBatch(ctx, r, step)
	}

	// Single call mode.
	method := strings.ToUpper(step.Method)
	if method == "" {
		method = "GET"
	}

	// Polling mode: poll until jq expression passes.
	if step.Poll != nil {
		return e.executePoll(ctx, r, step, method)
	}

	// One-shot mode: per-device parallel execution.
	if hasDeviceTemplate(step.URL) {
		return r.executeForDevices(step, func(name string) (string, error) {
			return e.doCall(r, method, step.URL, step.Params, name, step.Expect)
		})
	}

	// No {{device}} template — network-scoped call (no device parallelism).
	msg, err := e.doCall(r, method, step.URL, step.Params, "", step.Expect)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusFailed,
			Message: err.Error(),
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status:  StepStatusPassed,
		Message: msg,
	}}
}

// executeBatch runs a sequence of HTTP calls. If the URL contains {{device}},
// the batch is executed in parallel across devices (each device runs the full
// sequence). If not, the batch runs once with no device scoping. Fails on the
// first error within a batch.
func (e *newtronExecutor) executeBatch(ctx context.Context, r *Runner, step *Step) *StepOutput {
	// Check if any batch call uses {{device}} — if so, run per-device.
	deviceScoped := false
	for _, call := range step.Batch {
		if hasDeviceTemplate(call.URL) {
			deviceScoped = true
			break
		}
	}

	if deviceScoped {
		return r.executeForDevices(step, func(name string) (string, error) {
			for i, call := range step.Batch {
				method := strings.ToUpper(call.Method)
				if method == "" {
					method = "GET"
				}
				_, err := e.doCall(r, method, call.URL, call.Params, name, nil)
				if err != nil {
					return "", fmt.Errorf("batch[%d] %s %s: %s", i, method, call.URL, err)
				}
			}
			return fmt.Sprintf("batch: %d calls completed", len(step.Batch)), nil
		})
	}

	// No device scoping — run the batch once.
	for i, call := range step.Batch {
		method := strings.ToUpper(call.Method)
		if method == "" {
			method = "GET"
		}
		_, err := e.doCall(r, method, call.URL, call.Params, "", nil)
		if err != nil {
			return &StepOutput{Result: &StepResult{
				Status:  StepStatusFailed,
				Message: fmt.Sprintf("batch[%d] %s %s: %s", i, method, call.URL, err),
			}}
		}
	}
	return &StepOutput{Result: &StepResult{
		Status:  StepStatusPassed,
		Message: fmt.Sprintf("batch: %d calls completed", len(step.Batch)),
	}}
}

// executePoll polls the API until the jq expression passes or the timeout expires.
func (e *newtronExecutor) executePoll(ctx context.Context, r *Runner, step *Step, method string) *StepOutput {
	if hasDeviceTemplate(step.URL) {
		// pollForDevices reads timeout/interval from step.Expect. Create a
		// shallow copy of the step with poll values in an ExpectBlock so we
		// don't mutate the original step.
		pollStep := *step
		pollExpect := ExpectBlock{
			Timeout:      step.Poll.Timeout,
			PollInterval: step.Poll.Interval,
		}
		if step.Expect != nil {
			pollExpect.JQ = step.Expect.JQ
		}
		pollStep.Expect = &pollExpect

		return r.pollForDevices(ctx, &pollStep, func(name string) (bool, string, error) {
			msg, err := e.doCall(r, method, step.URL, step.Params, name, step.Expect)
			if err != nil {
				// For polling, errors mean "not ready yet" — keep polling.
				return false, err.Error(), nil
			}
			return true, msg, nil
		})
	}

	// No device template — poll a network-scoped endpoint.
	var lastMsg string
	err := pollUntil(ctx, step.Poll.Timeout, step.Poll.Interval, func() (bool, error) {
		msg, err := e.doCall(r, method, step.URL, step.Params, "", step.Expect)
		if err != nil {
			lastMsg = err.Error()
			return false, nil
		}
		lastMsg = msg
		return true, nil
	})
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusFailed,
			Message: lastMsg,
		}}
	}
	return &StepOutput{Result: &StepResult{
		Status:  StepStatusPassed,
		Message: lastMsg,
	}}
}

// doCall makes a single HTTP call, evaluates the jq expression if present,
// and returns a human-readable message.
func (e *newtronExecutor) doCall(r *Runner, method, urlTemplate string, params map[string]any, device string, expect *ExpectBlock) (string, error) {
	path := expandURL(urlTemplate, r.Client.NetworkID(), device)

	// Build body for POST/PUT/DELETE — only if params are provided.
	var body any
	if params != nil && (method == "POST" || method == "PUT" || method == "DELETE") {
		body = params
	}

	data, err := r.Client.RawRequest(method, path, body)
	if err != nil {
		return "", err
	}

	// If no jq assertion, success is simply a non-error response.
	if expect == nil || expect.JQ == "" {
		return fmt.Sprintf("%s %s: ok", method, path), nil
	}

	// Evaluate jq expression against response data.
	return evalJQ(expect.JQ, data, method, path)
}

// evalJQ compiles and runs a jq expression against JSON data.
// The expression must produce a single boolean true to pass.
func evalJQ(expr string, data json.RawMessage, method, path string) (string, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return "", fmt.Errorf("jq parse error: %w", err)
	}

	// Decode response data into a generic value for jq evaluation.
	var input any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &input); err != nil {
			return "", fmt.Errorf("jq: cannot decode response: %w", err)
		}
	}

	iter := query.Run(input)
	v, ok := iter.Next()
	if !ok {
		return "", fmt.Errorf("jq: expression produced no output")
	}
	if err, isErr := v.(error); isErr {
		return "", fmt.Errorf("jq eval error: %w", err)
	}

	// Boolean true passes; anything else is a failure.
	if b, isBool := v.(bool); isBool && b {
		return fmt.Sprintf("%s %s: jq assertion passed", method, path), nil
	}

	// Format the actual value for debugging.
	out, _ := json.Marshal(v)
	return "", fmt.Errorf("jq assertion failed: expression %q evaluated to %s", expr, string(out))
}

// expandURL substitutes {{device}} in a URL template and prepends the
// /network/<networkID> prefix. The network prefix is always implicit —
// URLs are relative to the network (e.g., /node/{{device}}/vlan).
// Both networkID and device are path-escaped for consistency with
// client.nodePath/interfacePath.
func expandURL(urlTemplate, networkID, device string) string {
	path := strings.ReplaceAll(urlTemplate, "{{device}}", url.PathEscape(device))
	return "/network/" + url.PathEscape(networkID) + path
}

// hasDeviceTemplate checks if a URL template contains {{device}}.
func hasDeviceTemplate(url string) bool {
	return strings.Contains(url, "{{device}}")
}
