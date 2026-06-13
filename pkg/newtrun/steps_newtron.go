package newtrun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// newtronExecutor implements the generic "newtron" action — a single step type
// that can make arbitrary HTTP calls to newtron-server with template-expanded
// URLs, optional polling, batch mode, and jq-based response evaluation.
//
// URL templates support {{device}} (expanded per-device by executeForDevices/
// pollForDevices). The network prefix /networks/<id> is implicit — URLs start
// from the path after the network segment (e.g., /nodes/{{device}}/vlans).
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
			msg, _, err := e.doCall(r, step, method, step.URL, step.Params, name, step.Headers, step.Expect)
			return msg, err
		})
	}

	// No {{device}} template — network-scoped call (no device parallelism).
	msg, raw, err := e.doCall(r, step, method, step.URL, step.Params, "", step.Headers, step.Expect)
	if err != nil {
		return &StepOutput{Result: &StepResult{
			Status:  StepStatusFailed,
			Message: err.Error(),
		}}
	}
	// Response-capture runs only on the single-call path. Batch and
	// poll modes don't expose a single response shape (batch =
	// multiple calls; poll = the assertion result, not the final
	// body); the parser rejects capture: on those step shapes so
	// this is the only place capture extraction is wired.
	if len(step.Capture) > 0 {
		if err := applyCaptures(r.captured, step.Capture, raw); err != nil {
			return &StepOutput{Result: &StepResult{
				Status:  StepStatusFailed,
				Message: fmt.Sprintf("response-capture: %s", err),
			}}
		}
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
				_, _, err := e.doCall(r, step, method, call.URL, call.Params, name, step.Headers, nil)
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
		_, _, err := e.doCall(r, step, method, call.URL, call.Params, "", step.Headers, nil)
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
			msg, _, err := e.doCall(r, step, method, step.URL, step.Params, name, step.Headers, step.Expect)
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
		msg, _, err := e.doCall(r, step, method, step.URL, step.Params, "", step.Headers, step.Expect)
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

// doCall makes a single HTTP call, evaluates the jq expression if
// present, and returns (human-readable message, raw response body,
// error). The raw body is propagated so the single-call path in
// Execute can run response-capture against it. Step-level headers
// are applied uniformly across every call from a step (including
// each call in a batch) so one step = one caller identity.
func (e *newtronExecutor) doCall(r *Runner, step *Step, method, urlTemplate string, params map[string]any, device string, headers map[string]string, expect *ExpectBlock) (string, json.RawMessage, error) {
	path := expandURL(urlTemplate, r.Client.NetworkID(), device)

	// Build body for POST/PUT/DELETE — only if params are provided.
	var body any
	if params != nil && (method == "POST" || method == "PUT" || method == "DELETE") {
		body = params
	}

	opts := make([]client.RequestOption, 0, len(headers)+1)
	for k, v := range headers {
		opts = append(opts, client.WithHeader(k, v))
	}
	// Step.As selects the cached-session user whose Bearer the
	// runner attaches to this request. Only set when the step
	// explicitly opted into per-step impersonation; absent
	// step.As leaves whatever credential r.Client was built with
	// (daemon-side --newtron-basic-auth, mTLS cert CN, or none)
	// in charge.
	if step != nil && step.As != "" {
		key, ok := r.UserSessions[step.As]
		if !ok || key == "" {
			return "", nil, fmt.Errorf("step requires identity %q but no session was supplied — run `newtron auth login --user %s` before starting the suite", step.As, step.As)
		}
		opts = append(opts, client.WithHeader("Authorization", "Bearer "+key))
	}

	data, err := r.Client.RawRequest(method, path, body, opts...)
	if err != nil {
		return "", nil, err
	}

	// If no jq assertion, success is simply a non-error response.
	if expect == nil || expect.JQ == "" {
		return fmt.Sprintf("%s %s: ok", method, path), data, nil
	}

	// Evaluate jq expression against response data.
	msg, err := evalJQ(expect.JQ, data, method, path)
	return msg, data, err
}

// evalJQ runs a jq expression against JSON data and asserts the
// result is boolean true. Layered on runJQ (jq.go) — that helper
// owns the parse + decode + first-result plumbing; this function
// adds only the bool assertion + the "passed/failed" message
// shape the suite reporter expects.
func evalJQ(expr string, data json.RawMessage, method, path string) (string, error) {
	v, err := runJQ(expr, data)
	if err != nil {
		return "", err
	}
	if b, isBool := v.(bool); isBool && b {
		return fmt.Sprintf("%s %s: jq assertion passed", method, path), nil
	}
	// Format the actual value for debugging.
	out, _ := json.Marshal(v)
	return "", fmt.Errorf("jq assertion failed: expression %q evaluated to %s", expr, string(out))
}

// expandURL substitutes {{device}} in a URL template and prepends the
// /newtron/v1/networks/<networkID> prefix. The api version + network
// prefix is always implicit — URLs are relative to the network
// (e.g., /nodes/{{device}}/create-vlan).
//
// Two URL templates are server-scoped, not network-scoped, and bypass
// the per-network prefix: /auth/login and /auth/logout. These are
// identity routes mounted at /newt-server/v1/auth/{login,logout} by
// cmd/newt-server's outer middleware (auth-design.md §L2c) — they
// carry no network context, so a per-network prefix would not
// resolve. Both networkID and device are path-escaped for
// consistency with client.nodePath/interfacePath.
func expandURL(urlTemplate, networkID, device string) string {
	path := strings.ReplaceAll(urlTemplate, "{{device}}", url.PathEscape(device))
	if path == "/auth/login" || path == "/auth/logout" {
		return "/newt-server/v1" + path
	}
	return "/newtron/v1/networks/" + url.PathEscape(networkID) + path
}

// hasDeviceTemplate checks if a URL template contains {{device}}.
func hasDeviceTemplate(url string) bool {
	return strings.Contains(url, "{{device}}")
}
