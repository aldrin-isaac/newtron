package newtrun

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Response-capture for newtron steps. A step's capture: map names a
// set of variables, each bound to a JQ expression that runs against
// the response body of the step's HTTP call. The extracted values
// land in the runner's per-iteration captured map; later steps in
// the same iteration read them via {{captured.NAME}} substitution.
//
// JQ syntax (the same gojq the existing expect.jq path uses) was
// picked deliberately so a suite author already familiar with
// expect.jq does not have to learn a second expression language for
// captures. The expression runs against the already-unwrapped data
// payload — the newtron client strips the `{data, error}` envelope
// before any assertion or capture sees the response — so a capture
// for a top-level field on /create-zone's `{"name": "..."}` is just
// `.name`, not `.data.name`. The capture runs only when the HTTP
// call succeeded — a failed call short-circuits before the capture
// phase, which is the behavior a suite author expects ("don't
// extract from an error response").
//
// Lifecycle (runner.go runScenarioSteps): the captured map is
// initialized empty at the start of every iteration and discarded
// when the iteration ends. Same-iteration step order in
// scenario.Steps fixes write-then-read ordering — there is no
// parallel execution of steps within one iteration, so a capture
// written in step N is guaranteed visible to step N+1.

// applyCaptures runs every entry in captures (variable name → JQ
// expression) against raw and writes the results into the captured
// map. A JQ path that selects a missing field returns nil per JQ
// semantics; the value is stored as nil, and the {{captured.NAME}}
// expansion in a later step surfaces this as an undefined-token
// error rather than embedding a literal "<nil>" string.
//
// On the first extraction error, applyCaptures returns without
// further mutation — partial captures would leave the map in a
// state that depends on map-iteration order, which Go intentionally
// randomizes.
func applyCaptures(captured map[string]any, captures map[string]string, raw json.RawMessage) error {
	for name, jqExpr := range captures {
		v, err := runJQ(jqExpr, raw)
		if err != nil {
			return fmt.Errorf("capture %q (%q): %w", name, jqExpr, err)
		}
		captured[name] = v
	}
	return nil
}

// stepReferencesCaptured reports whether any field on step contains
// a {{captured.X}} token. The runner uses this to decide whether to
// route a step through ExpandStep when no other reason (target /
// param substitution, populated captured map) already requires
// expansion — captured-only references in a non-parameterized
// scenario otherwise wouldn't be detected.
func stepReferencesCaptured(step Step) bool {
	if containsCapturedToken(step.URL) || containsCapturedToken(step.Command) {
		return true
	}
	if anyReferencesCaptured(step.Params) {
		return true
	}
	for _, v := range step.Headers {
		if containsCapturedToken(v) {
			return true
		}
	}
	for _, bc := range step.Batch {
		if containsCapturedToken(bc.URL) || anyReferencesCaptured(bc.Params) {
			return true
		}
	}
	if step.Expect != nil {
		if containsCapturedToken(step.Expect.JQ) || containsCapturedToken(step.Expect.Contains) {
			return true
		}
	}
	return false
}

func containsCapturedToken(s string) bool {
	return strings.Contains(s, "{{captured.")
}

func anyReferencesCaptured(v any) bool {
	switch t := v.(type) {
	case string:
		return containsCapturedToken(t)
	case map[string]any:
		for _, vv := range t {
			if anyReferencesCaptured(vv) {
				return true
			}
		}
	case []any:
		for _, item := range t {
			if anyReferencesCaptured(item) {
				return true
			}
		}
	}
	return false
}
