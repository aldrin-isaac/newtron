package auth

import "testing"

// TestMatchPattern_Exact pins the simplest case — literal string
// match, no globs, no commas.
func TestMatchPattern_Exact(t *testing.T) {
	if !matchPattern("edge-1", "edge-1") {
		t.Error("exact match should succeed")
	}
	if matchPattern("edge-1", "edge-2") {
		t.Error("non-match should fail")
	}
}

// TestMatchPattern_Glob pins the trailing-* semantics. Only suffix
// globs are supported; embedded or leading globs aren't (the
// matcher treats `*` only as the last character in an atom).
func TestMatchPattern_Glob(t *testing.T) {
	if !matchPattern("edge-*", "edge-1") {
		t.Error("glob should match prefix")
	}
	if !matchPattern("edge-*", "edge-anything") {
		t.Error("glob should match arbitrary suffix")
	}
	if matchPattern("edge-*", "spine-1") {
		t.Error("glob should not match different prefix")
	}
	if !matchPattern("*", "anything") {
		t.Error("bare * should match anything")
	}
}

// TestMatchPattern_CommaList pins the OR semantics: any include
// matches.
func TestMatchPattern_CommaList(t *testing.T) {
	if !matchPattern("edge-1,edge-2", "edge-1") {
		t.Error("first item should match")
	}
	if !matchPattern("edge-1,edge-2", "edge-2") {
		t.Error("second item should match")
	}
	if matchPattern("edge-1,edge-2", "edge-3") {
		t.Error("neither item matching should fail")
	}
}

// TestMatchPattern_Exclusion pins the bang-prefix semantics. An
// exclude-only pattern matches anything that doesn't match any
// exclude — the shape the meta-authorization scenario uses.
func TestMatchPattern_Exclusion(t *testing.T) {
	if matchPattern("!permissions", "permissions") {
		t.Error("exact exclusion should reject the value")
	}
	if !matchPattern("!permissions", "services") {
		t.Error("exclude-only should pass a value the exclude doesn't catch")
	}
	if !matchPattern("!permissions,!user_groups,!super_users", "services") {
		t.Error("multi-exclude should pass any value none of them catches")
	}
	if matchPattern("!permissions,!user_groups,!super_users", "user_groups") {
		t.Error("multi-exclude should reject a value the second exclude catches")
	}
}

// TestMatchPattern_Mixed pins include+exclude composition: must
// match an include AND not match any exclude.
func TestMatchPattern_Mixed(t *testing.T) {
	if !matchPattern("edge-*,!edge-broken", "edge-1") {
		t.Error("include match + no exclude match should pass")
	}
	if matchPattern("edge-*,!edge-broken", "edge-broken") {
		t.Error("include match + exclude match should fail")
	}
	if matchPattern("edge-*,!edge-broken", "spine-1") {
		t.Error("no include match should fail")
	}
}

// TestMatchPattern_EmptyPatternEmptyValue pins that empty matches
// only empty — guards against an operator accidentally granting all
// by leaving a dimension blank in network.json.
func TestMatchPattern_EmptyPatternEmptyValue(t *testing.T) {
	if !matchPattern("", "") {
		t.Error("empty pattern should match empty value")
	}
	if matchPattern("", "anything") {
		t.Error("empty pattern should not match non-empty value")
	}
}

// TestWhereMatches_EmptyAllowsAll pins the legacy-compat path: an
// empty Where map matches every Context, which is how a pre-L5 flat
// group list behaves.
func TestWhereMatches_EmptyAllowsAll(t *testing.T) {
	ctx := NewContext().WithDevice("edge-1")
	if !whereMatches(map[string]string{}, ctx) {
		t.Error("empty where should match any context")
	}
	if !whereMatches(nil, ctx) {
		t.Error("nil where should match any context")
	}
}

// TestWhereMatches_DeviceDimension pins device matching against
// glob patterns.
func TestWhereMatches_DeviceDimension(t *testing.T) {
	ctx := NewContext().WithDevice("edge-1")
	if !whereMatches(map[string]string{"device": "edge-*"}, ctx) {
		t.Error("edge-1 should match edge-*")
	}
	if whereMatches(map[string]string{"device": "spine-*"}, ctx) {
		t.Error("edge-1 should not match spine-*")
	}
}

// TestWhereMatches_ResourceDimension pins the generic-identifier
// dimension. Resource is populated by every gate (alongside the
// more specific dimension when applicable) so operators can scope
// on the entity name uniformly regardless of which dimension the
// gate populated. The authorization-howto.md table documents
// resource as a valid dimension; this test enforces it.
func TestWhereMatches_ResourceDimension(t *testing.T) {
	ctx := NewContext().WithResource("transit-1")
	if !whereMatches(map[string]string{"resource": "transit-*"}, ctx) {
		t.Error("transit-1 should match transit-*")
	}
	if whereMatches(map[string]string{"resource": "vpn-*"}, ctx) {
		t.Error("transit-1 should not match vpn-*")
	}
}

// TestWhereMatches_FieldDimension pins the meta-authorization
// scenario: an admin role with `field: "permissions,user_groups"`
// can edit those fields, and a separate spec.author role excluding
// them with `field: "!permissions,!user_groups,!super_users"` covers
// the rest. The two together implement the §3 criterion 9 separation.
func TestWhereMatches_FieldDimension(t *testing.T) {
	specAuthorWhere := map[string]string{"field": "!permissions,!user_groups,!super_users"}
	iamWhere := map[string]string{"field": "permissions,user_groups,super_users"}

	cases := []struct {
		field      string
		specAuthor bool
		iam        bool
	}{
		{"services", true, false},
		{"profiles", true, false},
		{"topology", true, false},
		{"permissions", false, true},
		{"user_groups", false, true},
		{"super_users", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			ctx := NewContext().WithField(tc.field)
			got := whereMatches(specAuthorWhere, ctx)
			if got != tc.specAuthor {
				t.Errorf("specAuthor where on field=%q: got %v, want %v", tc.field, got, tc.specAuthor)
			}
			got = whereMatches(iamWhere, ctx)
			if got != tc.iam {
				t.Errorf("iam where on field=%q: got %v, want %v", tc.field, got, tc.iam)
			}
		})
	}
}

// TestWhereMatches_UnknownDimensionFailsClosed pins the fail-closed
// contract: an operator typo like "devic" in network.json yields
// denial, not silent always-allow.
func TestWhereMatches_UnknownDimensionFailsClosed(t *testing.T) {
	ctx := NewContext().WithDevice("edge-1")
	if whereMatches(map[string]string{"devic": "edge-*"}, ctx) {
		t.Error("typo in dimension name should deny (fail closed)")
	}
}

// TestWhereMatches_MultipleDimensions pins AND semantics across
// dimensions: every named dimension must match.
func TestWhereMatches_MultipleDimensions(t *testing.T) {
	ctx := NewContext().WithDevice("edge-1").WithService("transit-1")
	if !whereMatches(map[string]string{"device": "edge-*", "service": "transit-*"}, ctx) {
		t.Error("both dimensions matching should pass")
	}
	if whereMatches(map[string]string{"device": "edge-*", "service": "vpn-*"}, ctx) {
		t.Error("one dimension failing should fail the where")
	}
}
