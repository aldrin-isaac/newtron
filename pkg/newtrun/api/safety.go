package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// InlineSafetyPolicy is the set of guardrails applied to a scenario
// submitted via POST /api/runs/inline. The policy is constructed per
// request — server defaults plus optional request-level overrides — and
// validated against the parsed scenario before the runner is spawned.
//
// The defaults track the issue spec for newtron#23 §Production safety
// and operationalize DESIGN_PRINCIPLES_NEWTRON §13 (Prevent Bad Writes,
// Don't Just Detect Them): catch unsafe scenarios at validation time —
// before any device-facing operation runs — rather than report them
// after the fact. The inline endpoint accepts scenarios composed by a
// browser frontend in response to operator clicks, not scenarios
// authored by test engineers. The framework cannot trust the YAML the
// same way it trusts file-backed suites; the defaults enforce that.
type InlineSafetyPolicy struct {
	// AllowedActions lists the StepActions permitted in the scenario.
	// Default: {ActionNewtron, ActionWait}. host-exec and newtron-cli
	// are excluded by default because they shell out with unbounded
	// blast radius; topology-reconcile is excluded because it can
	// replace an entire device's intent state in one call.
	AllowedActions map[newtrun.StepAction]bool

	// AllowedURLPrefixes lists the URL prefixes the newtron action may
	// call. Calls to URLs not matching any prefix are rejected. Default:
	// the configured newtron-server URL. Empty list = allow any URL
	// (used by tests).
	AllowedURLPrefixes []string

	// AllowReconcile, when true, permits ActionProvision (topology-
	// reconcile) in the scenario. Default false — operators must opt in
	// per-run via the ?allow_reconcile=true query parameter.
	AllowReconcile bool

	// WallTimeBudget is the maximum duration the run may take before
	// the server cancels it. Default 60 seconds. The default lab-suite
	// timeouts are minutes; production inline runs should be much
	// tighter to keep operator-driven flows responsive.
	WallTimeBudget time.Duration
}

// DefaultInlineSafetyPolicy returns the v0 policy: newtron and wait
// actions only, reconcile gated off, 60-second wall-time budget, URL
// allow-list deferred to caller (typically the server's configured
// newtron-server URL).
func DefaultInlineSafetyPolicy() InlineSafetyPolicy {
	return InlineSafetyPolicy{
		AllowedActions: map[newtrun.StepAction]bool{
			newtrun.ActionNewtron: true,
			newtrun.ActionWait:    true,
		},
		AllowReconcile: false,
		WallTimeBudget: 60 * time.Second,
	}
}

// SafetyViolation lists every reason a scenario was rejected by the
// inline safety policy. Multiple reasons are accumulated so the operator
// sees the full picture at once rather than fixing them one at a time.
type SafetyViolation struct {
	Reasons []string
}

func (v *SafetyViolation) Error() string {
	if len(v.Reasons) == 1 {
		return "inline scenario rejected: " + v.Reasons[0]
	}
	return fmt.Sprintf("inline scenario rejected:\n  - %s", strings.Join(v.Reasons, "\n  - "))
}

// Validate checks the scenario against the policy and returns a non-nil
// *SafetyViolation listing every guardrail it tripped. Returns nil when
// the scenario is acceptable.
func (p InlineSafetyPolicy) Validate(s *newtrun.Scenario) error {
	v := &SafetyViolation{}

	// Self-contained: no cross-scenario dependencies.
	if len(s.Requires) > 0 {
		v.Reasons = append(v.Reasons,
			fmt.Sprintf("scenario must be self-contained; 'requires' is not allowed on inline runs (got %v)", s.Requires))
	}
	if len(s.After) > 0 {
		v.Reasons = append(v.Reasons,
			fmt.Sprintf("scenario must be self-contained; 'after' is not allowed on inline runs (got %v)", s.After))
	}

	for i, step := range s.Steps {
		stepID := fmt.Sprintf("steps[%d] (%q)", i, step.Name)

		// Action allow-list. topology-reconcile is checked separately
		// because it has its own opt-in toggle.
		if step.Action == newtrun.ActionProvision {
			if !p.AllowReconcile {
				v.Reasons = append(v.Reasons,
					fmt.Sprintf("%s: action %q is high-impact and gated off; pass ?allow_reconcile=true to opt in",
						stepID, step.Action))
			}
			continue
		}
		if !p.AllowedActions[step.Action] {
			v.Reasons = append(v.Reasons,
				fmt.Sprintf("%s: action %q is not permitted by the inline safety policy", stepID, step.Action))
			continue
		}

		// For newtron actions, URL must match a configured prefix when
		// the prefix list is non-empty.
		if step.Action == newtrun.ActionNewtron && len(p.AllowedURLPrefixes) > 0 {
			if !urlAllowed(step.URL, p.AllowedURLPrefixes) {
				v.Reasons = append(v.Reasons,
					fmt.Sprintf("%s: URL %q is not in the allowed prefix list", stepID, step.URL))
			}
			// Batch calls share the same URL prefix policy.
			for j, batch := range step.Batch {
				if !urlAllowed(batch.URL, p.AllowedURLPrefixes) {
					v.Reasons = append(v.Reasons,
						fmt.Sprintf("%s.batch[%d]: URL %q is not in the allowed prefix list",
							stepID, j, batch.URL))
				}
			}
		}
	}

	if len(v.Reasons) > 0 {
		return v
	}
	return nil
}

// urlAllowed returns true when the URL matches any allowed prefix. URLs
// in newtrun scenarios are typically relative paths under the newtron-
// server base ("/api/network/.../apply-service"); the prefix check is a
// simple HasPrefix.
func urlAllowed(url string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(url, p) {
			return true
		}
	}
	return false
}
