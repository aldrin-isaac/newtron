package newtrun

import (
	"strings"
	"testing"
)

// TestCheckRequiredParams_Empty pins the no-requirements path: a
// scenario with no requires_params: always proceeds (returns ""),
// even when the runner has no resolved parameters at all.
func TestCheckRequiredParams_Empty(t *testing.T) {
	r := &Runner{resolvedParameters: nil}
	sc := &Scenario{RequiresParams: nil}
	if got := r.checkRequiredParams(sc); got != "" {
		t.Errorf("empty requirements should not skip, got %q", got)
	}
}

// TestCheckRequiredParams_Unset pins that a required parameter not
// present in the resolved map yields a skip reason that names the
// missing parameter.
func TestCheckRequiredParams_Unset(t *testing.T) {
	r := &Runner{resolvedParameters: map[string]any{}}
	sc := &Scenario{RequiresParams: []string{"alice_basic_auth"}}
	got := r.checkRequiredParams(sc)
	if got == "" {
		t.Fatal("expected skip reason; got empty")
	}
	if !strings.Contains(got, "alice_basic_auth") {
		t.Errorf("skip reason missing param name: %q", got)
	}
}

// TestCheckRequiredParams_EmptyString pins that the empty-string
// default of a `type: string` parameter counts as "missing" — the
// operator left the suite-level default in place.
func TestCheckRequiredParams_EmptyString(t *testing.T) {
	r := &Runner{resolvedParameters: map[string]any{"alice_basic_auth": ""}}
	sc := &Scenario{RequiresParams: []string{"alice_basic_auth"}}
	got := r.checkRequiredParams(sc)
	if got == "" {
		t.Fatal("expected skip reason for empty string")
	}
	if !strings.Contains(got, "empty") {
		t.Errorf("skip reason should name the empty condition: %q", got)
	}
}

// TestCheckRequiredParams_NonEmpty pins the happy path: a non-empty
// supplied value lets the scenario proceed.
func TestCheckRequiredParams_NonEmpty(t *testing.T) {
	r := &Runner{resolvedParameters: map[string]any{"alice_basic_auth": "YWxpY2U6cHc="}}
	sc := &Scenario{RequiresParams: []string{"alice_basic_auth"}}
	if got := r.checkRequiredParams(sc); got != "" {
		t.Errorf("non-empty supplied value should proceed, got skip %q", got)
	}
}

// TestCheckRequiredParams_ZeroInt pins the zero-int case for
// numeric parameters — a `type: int` left at 0 also counts as
// "operator didn't supply a value."
func TestCheckRequiredParams_ZeroInt(t *testing.T) {
	r := &Runner{resolvedParameters: map[string]any{"port": 0}}
	sc := &Scenario{RequiresParams: []string{"port"}}
	got := r.checkRequiredParams(sc)
	if got == "" {
		t.Fatal("expected skip reason for zero int")
	}
	if !strings.Contains(got, "port") {
		t.Errorf("skip reason should name the param: %q", got)
	}
}

// TestCheckRequiredParams_FalseBool pins that bool false is treated
// as "operator left at default" — explicit true is the only
// non-skipping value. Matches the "opt-in" framing.
func TestCheckRequiredParams_FalseBool(t *testing.T) {
	r := &Runner{resolvedParameters: map[string]any{"enable_x": false}}
	sc := &Scenario{RequiresParams: []string{"enable_x"}}
	got := r.checkRequiredParams(sc)
	if got == "" {
		t.Fatal("expected skip reason for bool false")
	}
}

// TestCheckRequiredParams_FirstMissing pins that the first missing
// parameter is named in the skip reason — error messages are
// fail-fast rather than aggregating, so the operator knows the
// shortest path to making the scenario runnable.
func TestCheckRequiredParams_FirstMissing(t *testing.T) {
	r := &Runner{resolvedParameters: map[string]any{
		"alpha": "set",
		"beta":  "",
	}}
	sc := &Scenario{RequiresParams: []string{"alpha", "beta", "gamma"}}
	got := r.checkRequiredParams(sc)
	if got == "" {
		t.Fatal("expected skip reason")
	}
	if !strings.Contains(got, "beta") {
		t.Errorf("expected first-missing 'beta' in reason; got %q", got)
	}
	if strings.Contains(got, "gamma") {
		t.Errorf("reason should fail fast at beta, not enumerate gamma; got %q", got)
	}
}
