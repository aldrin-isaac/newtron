package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestParseModeSpeed pins the speed-parser grammar against every
// mode-string form observed in real SONiC platform.json files
// under testdata/sonic-platform-json/ — plus a handful of
// adversarial inputs the regex deliberately rejects.
//
// The grammar (modeRE): ^(\d+)x(\d+)([GM]). What follows the
// suffix (alt list in brackets, lane-count override in parens) is
// ignored — the speed AT the suffix is what the headline-speed
// derivation cares about, and the canonical form drops everything
// after the suffix.
func TestParseModeSpeed(t *testing.T) {
	cases := []struct {
		mode      string
		canonical string
		rateMbps  int
		ok        bool
	}{
		// Real modes from testdata/sonic-platform-json/z9332f.json.
		{"1x400G", "400G", 400000, true},
		{"2x200G[100G,40G]", "200G", 200000, true},
		{"4x100G[50G]", "100G", 100000, true},
		{"1x100G(4)", "100G", 100000, true},
		// Real modes from testdata/sonic-platform-json/arista-7060-cx32s.json.
		{"1x100G[50G,40G,25G,10G]", "100G", 100000, true},
		{"2x50G[40G,25G,10G]", "50G", 50000, true},
		{"4x25G[10G]", "25G", 25000, true},
		// Forms not in the in-repo fixtures but valid SONiC convention.
		{"1x40G", "40G", 40000, true},
		{"1x800G[400G]", "800G", 800000, true},
		// 4x25G(4)[10G,1G] — parens-before-brackets, observed in
		// the wild on mellanox-sn4700 (PR #189 follow-up real
		// platform run). The regex captures `4x25G` and ignores
		// everything after the suffix; this case pins that
		// behavior so a future regex change can't silently
		// regress on the grammar variant.
		{"4x25G(4)[10G,1G]", "25G", 25000, true},
		// 8x100G — observed on arista-7060x6 (8-way breakouts).
		{"8x100G", "100G", 100000, true},
		// Adversarial — strings the parser MUST reject so callers
		// can skip-rather-than-misinterpret. Each case documents
		// what reality would look like if the parser silently
		// accepted it.
		{"", "", 0, false},
		{"100G", "", 0, false},     // missing "count x" prefix
		{"1xfoo", "", 0, false},    // non-digit speed
		{"0x100G", "", 0, false},   // zero-count rejected (would 0-divide downstream)
		{"1x0G", "", 0, false},     // zero-speed rejected (no real port runs at 0)
		{"1x100T", "", 0, false},   // unknown suffix — translator only models G and M
		{"foo", "", 0, false},
	}
	for _, c := range cases {
		gotCanonical, gotRate, gotOK := parseModeSpeed(c.mode)
		if gotOK != c.ok || gotCanonical != c.canonical || gotRate != c.rateMbps {
			t.Errorf("parseModeSpeed(%q) = (%q, %d, %v); want (%q, %d, %v)",
				c.mode, gotCanonical, gotRate, gotOK, c.canonical, c.rateMbps, c.ok)
		}
	}
}

// TestFromSONiCPlatformJSON_Z9332f_RealFixture exercises the
// translator against a real (in-tree, captured 2026-06-16) dell
// Z9332f platform.json — 32 front-panel + 2 management ports,
// 400G headline, breakouts down to 4x100G[50G].
//
// §16: the assertions verify behavior, not just "no error." Every
// derived field matches a fact you can hand-check in the input
// file (port_count = 34, headline = 400G, breakouts include 4
// distinct mode-strings).
func TestFromSONiCPlatformJSON_Z9332f_RealFixture(t *testing.T) {
	data := mustRead(t, "testdata/sonic-platform-json/z9332f.json")
	got, err := FromSONiCPlatformJSON(data, SONiCImportOptions{
		HWSKU:       "DellEMC-Z9332f-O32",
		Description: "Dell Z9332F-ON 32x400G",
		Dataplane:   "",
	})
	if err != nil {
		t.Fatalf("FromSONiCPlatformJSON: %v", err)
	}
	if got.HWSKU != "DellEMC-Z9332f-O32" {
		t.Errorf("HWSKU: got %q", got.HWSKU)
	}
	if got.Description != "Dell Z9332F-ON 32x400G" {
		t.Errorf("Description: got %q", got.Description)
	}
	if got.DeviceType != "switch" {
		t.Errorf("DeviceType: got %q, want switch", got.DeviceType)
	}
	if got.PortCount != 34 {
		t.Errorf("PortCount: got %d, want 34", got.PortCount)
	}
	if got.DefaultSpeed != "400G" {
		t.Errorf("DefaultSpeed: got %q, want 400G", got.DefaultSpeed)
	}
	// Breakouts: every observed mode-key across all 34 interfaces.
	// Hand-verified from the file: the 32 front-panel 400G ports
	// carry the four high-speed modes; the 2 management ports
	// carry 1x10G. The union picks up all five.
	wantBreakouts := []string{
		"1x100G(4)",
		"1x10G",
		"1x400G",
		"2x200G[100G,40G]",
		"4x100G[50G]",
	}
	if !reflect.DeepEqual(got.Breakouts, wantBreakouts) {
		t.Errorf("Breakouts: got %v, want %v", got.Breakouts, wantBreakouts)
	}
	if got.Dataplane != "" {
		t.Errorf("Dataplane: got %q, want \"\" (real-hardware default)", got.Dataplane)
	}
	// VM deployment fields are NOT derivable from platform.json and the
	// generator must leave them zero so the operator can fill them in for
	// simulator platforms or omit them for real hardware.
	if got.VMImage != "" || got.VMMemory != 0 || got.VMCPUs != 0 {
		t.Errorf("VM fields should be zero (not derivable from SONiC platform.json); got VMImage=%q VMMemory=%d VMCPUs=%d",
			got.VMImage, got.VMMemory, got.VMCPUs)
	}
	// VMInterfaceMap is the exception — the one VM field with a universal-safe
	// default ("sequential"; see FromPortConfigINI / RCA-013), so it is set, not
	// left zero.
	if got.VMInterfaceMap != "sequential" {
		t.Errorf("VMInterfaceMap: got %q, want \"sequential\" (universal-safe default)", got.VMInterfaceMap)
	}
	// Ports: one per interface (34), sorted by front-panel index, NIC slots
	// 1..34. platform.json carries no per-port speed, so Speed is empty (the
	// consumer falls back to default_speed); lanes come from the `lanes` field.
	if len(got.Ports) != 34 {
		t.Fatalf("len(Ports): got %d, want 34", len(got.Ports))
	}
	for i, p := range got.Ports {
		if p.NICIndex != i+1 {
			t.Errorf("Ports[%d].NICIndex: got %d, want %d", i, p.NICIndex, i+1)
		}
		if p.Speed != "" {
			t.Errorf("Ports[%d] (%s).Speed: got %q, want empty (platform.json has no per-port speed)", i, p.Name, p.Speed)
		}
	}
	// Lowest front-panel index (Ethernet0, index "1,1,1,…") sorts to NIC 1;
	// Ethernet8 (index 2) follows. Hand-verified from the fixture.
	wantFirst := PortSpec{Name: "Ethernet0", NICIndex: 1, Lanes: []int{33, 34, 35, 36, 37, 38, 39, 40}}
	if !reflect.DeepEqual(got.Ports[0], wantFirst) {
		t.Errorf("Ports[0]: got %+v, want %+v", got.Ports[0], wantFirst)
	}
	if got.Ports[1].Name != "Ethernet8" {
		t.Errorf("Ports[1].Name: got %q, want Ethernet8 (front-panel index 2)", got.Ports[1].Name)
	}
}

// TestFromSONiCPlatformJSON_Arista7060_RealFixture covers a
// different shape: 32x100G with 4x25G[10G] breakouts. Headline is
// 100G (no 400G or 200G modes anywhere in the file). Confirms the
// translator picks the right headline for a non-400G platform.
func TestFromSONiCPlatformJSON_Arista7060_RealFixture(t *testing.T) {
	data := mustRead(t, "testdata/sonic-platform-json/arista-7060-cx32s.json")
	got, err := FromSONiCPlatformJSON(data, SONiCImportOptions{
		HWSKU: "Arista-7060CX-32S-D48C8",
	})
	if err != nil {
		t.Fatalf("FromSONiCPlatformJSON: %v", err)
	}
	if got.PortCount != 34 {
		t.Errorf("PortCount: got %d, want 34", got.PortCount)
	}
	if got.DefaultSpeed != "100G" {
		t.Errorf("DefaultSpeed: got %q, want 100G (highest 1xN in file)", got.DefaultSpeed)
	}
	// Hand-verified from the file: 32 front-panel 100G ports with
	// three modes (1x100G[50G,40G,25G,10G], 2x50G[40G,25G,10G],
	// 4x25G[10G]); 2 management ports with 1x10G.
	wantBreakouts := []string{
		"1x100G[50G,40G,25G,10G]",
		"1x10G",
		"2x50G[40G,25G,10G]",
		"4x25G[10G]",
	}
	if !reflect.DeepEqual(got.Breakouts, wantBreakouts) {
		t.Errorf("Breakouts: got %v, want %v", got.Breakouts, wantBreakouts)
	}
}

// TestFromSONiCPlatformJSON_Errors pins every error path the
// translator returns. Each case names the failure mode an
// operator would hit AND the actionable phrase from the error
// message — so a future change to wording can't silently make a
// previously-discoverable error harder to diagnose.
func TestFromSONiCPlatformJSON_Errors(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		opts     SONiCImportOptions
		wantSnip string
	}{
		{
			name:     "missing-hwsku",
			data:     []byte(`{"interfaces":{"Ethernet0":{"breakout_modes":{"1x100G":[]}}}}`),
			opts:     SONiCImportOptions{},
			wantSnip: "HWSKU is required",
		},
		{
			name:     "bad-json",
			data:     []byte(`{"interfaces": not-json`),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "parsing SONiC platform.json",
		},
		{
			name:     "no-interfaces",
			data:     []byte(`{"chassis": {"name": "no-ports"}}`),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "no \"interfaces\" entries",
		},
		{
			// Empty interfaces map is the older per-HWSKU
			// port_config.ini convention (issue #190). The
			// error MUST distinguish this case from the
			// missing-key case so the operator knows to look
			// at port_config.ini rather than thinking the
			// platform is unparseable.
			name:     "empty-interfaces-points-at-port-config-ini",
			data:     []byte(`{"interfaces": {}}`),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "port_config.ini",
		},
		{
			name:     "no-1xn-mode",
			data:     []byte(`{"interfaces":{"Ethernet0":{"breakout_modes":{"4x25G[10G]":[]}}}}`),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "no parseable 1xN mode",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FromSONiCPlatformJSON(c.data, c.opts)
			if err == nil {
				t.Fatalf("expected error containing %q; got nil err, spec=%+v", c.wantSnip, got)
			}
			if !contains(err.Error(), c.wantSnip) {
				t.Errorf("error message %q missing required snippet %q", err.Error(), c.wantSnip)
			}
		})
	}
}

// TestUnionBreakouts pins the dedup + sort contract directly.
// Same-mode key appearing on multiple ports collapses to one.
// Sorted output is deterministic across runs (no map-iteration
// flakiness).
func TestUnionBreakouts(t *testing.T) {
	interfaces := map[string]sonicInterface{
		"Ethernet0": {BreakoutModes: rawModes("1x100G", "4x25G")},
		"Ethernet4": {BreakoutModes: rawModes("1x100G", "2x50G", "4x25G")},
		"Ethernet8": {BreakoutModes: rawModes("1x40G")},
	}
	got := unionBreakouts(interfaces)
	want := []string{"1x100G", "1x40G", "2x50G", "4x25G"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unionBreakouts: got %v, want %v", got, want)
	}
}

// ---- helpers ----

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	return data
}

// rawModes builds the breakout_modes map shape unionBreakouts reads.
// The values don't matter for unionBreakouts; nil is fine.
func rawModes(keys ...string) map[string]json.RawMessage {
	m := make(map[string]json.RawMessage, len(keys))
	for _, k := range keys {
		m[k] = nil
	}
	return m
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
