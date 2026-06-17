// platform_from_sonic.go — translate a SONiC device-tree platform.json
// into a newtron PlatformSpec (issue #185).
//
// PlatformSpec lives in this package; per DPN §27 single owner, the
// SONiC→newtron translation also lives here so the only writer of
// PlatformSpec field semantics is the package that defines it. The
// CLI subcommand (cmd/newtron/cmd_platform_generate.go) is a thin
// I/O wrapper around FromSONiCPlatformJSON.
//
// What the translator derives from platform.json:
//
//   - PortCount: len(interfaces)
//   - DefaultSpeed: highest-rate 1xN mode across every port's
//     breakout_modes map (the "headline" speed of the platform —
//     the speed each port runs at without a breakout split)
//   - Breakouts: sorted union of every breakout_modes key across
//     every interface
//
// What the operator provides via flags (NOT derivable from
// platform.json): HWSKU, Description, DeviceType ("switch"),
// Dataplane, and every VM/lab field (VMImage, VMMemory, etc.).
// PlatformSpec leaves them at their zero values; the operator
// fills them in for simulator platforms or omits them for real
// hardware.
//
// What the issue body assumed but reality refuted: SONiC
// platform.json does NOT carry a default_brkout_mode field at the
// per-interface level. Verified against three real fixtures
// (Z9332f, Arista 7060) under testdata/sonic-platform-json/. The
// headline-speed derivation above replaces the original "first
// interface's default_brkout_mode" plan.
package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// ErrEmptyInterfaces is returned by FromSONiCPlatformJSON when the
// loaded platform.json has the older per-HWSKU port_config.ini
// convention — `"interfaces": {}` at the top level with per-port
// shape living in `<hwsku>/port_config.ini` instead (issue #190).
// CLI callers (cmd/newtron/cmd_platform_generate.go) detect this
// sentinel and fall through to FromPortConfigINI.
//
// The error message that wraps ErrEmptyInterfaces also names the
// fallback path so an operator running the translator outside the
// CLI gets the same actionable phrase the CLI's auto-discovery
// surfaces.
var ErrEmptyInterfaces = errors.New("SONiC platform.json: interfaces map is present but empty (per-HWSKU port_config.ini convention)")

// sonicPlatformFile is the subset of platform.json this translator
// reads. The chassis section and the rest are ignored — newtron
// PlatformSpec doesn't carry chassis metadata.
type sonicPlatformFile struct {
	Interfaces map[string]sonicInterface `json:"interfaces"`
}

// sonicInterface is the subset of each port entry the translator
// reads. lanes and index are deliberately ignored — newtron
// PlatformSpec doesn't carry per-lane front-panel metadata
// (filed-out under §non-goals in #185).
type sonicInterface struct {
	BreakoutModes map[string]json.RawMessage `json:"breakout_modes"`
}

// SONiCImportOptions is the operator-provided side of the
// translation — fields that aren't derivable from platform.json
// and must be supplied by the CLI wrapper or programmatic caller.
type SONiCImportOptions struct {
	// HWSKU is required. SONiC platform.json does not carry HWSKU
	// (it lives in the sibling <hwsku>/ directory under
	// /usr/share/sonic/device/<vendor>-<platform>/).
	HWSKU string

	// Description is optional. Set on PlatformSpec.Description; the
	// generator emits an empty string when unset.
	Description string

	// Dataplane is optional ("" for real hardware; "vpp" or similar
	// for simulator platforms). Set on PlatformSpec.Dataplane.
	Dataplane string
}

// FromSONiCPlatformJSON parses a SONiC platform.json byte stream
// and returns the derived PlatformSpec. opts.HWSKU is required.
//
// Errors on:
//   - JSON parse failure
//   - missing "interfaces" map (or empty)
//   - empty opts.HWSKU
//   - no parseable 1xN mode anywhere in the file (the headline-
//     speed derivation has nothing to work with — file is real
//     SONiC but lacks the convention)
func FromSONiCPlatformJSON(data []byte, opts SONiCImportOptions) (*PlatformSpec, error) {
	if opts.HWSKU == "" {
		return nil, fmt.Errorf("HWSKU is required (not carried in platform.json; supply via SONiCImportOptions.HWSKU)")
	}
	// Two-pass decode so the empty-map vs missing-key cases can
	// be distinguished. SONiC's older per-HWSKU port_config.ini
	// convention (see issue #190) is recognized by an empty
	// interfaces map at the top level; the error message points
	// the operator at the fallback path rather than reading as a
	// generic "no entries."
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parsing SONiC platform.json: %w", err)
	}
	rawIfaces, hasInterfacesKey := probe["interfaces"]
	var raw sonicPlatformFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing SONiC platform.json: %w", err)
	}
	if len(raw.Interfaces) == 0 {
		if hasInterfacesKey && isEmptyJSONMap(rawIfaces) {
			// Wrap the typed sentinel so callers can detect the
			// older-convention case (CLI auto-discovers
			// <hwsku>/port_config.ini) while preserving the
			// actionable message for callers that just print
			// the error.
			return nil, fmt.Errorf("%w. Per-port shape lives in <hwsku>/port_config.ini "+
				"under the device tree (sibling to platform.json), not in platform.json. "+
				"Pass that path to FromPortConfigINI (or `--port-config-ini` on the CLI; "+
				"the CLI auto-discovers a sibling `<hwsku>/port_config.ini` when none is supplied).",
				ErrEmptyInterfaces)
		}
		return nil, fmt.Errorf("SONiC platform.json: no \"interfaces\" entries (expected one per front-panel port)")
	}
	defaultSpeed, err := deriveHeadlineSpeed(raw.Interfaces)
	if err != nil {
		return nil, fmt.Errorf("deriving default_speed from breakout_modes: %w", err)
	}
	breakouts := unionBreakouts(raw.Interfaces)
	return &PlatformSpec{
		HWSKU:        opts.HWSKU,
		Description:  opts.Description,
		DeviceType:   "switch",
		PortCount:    len(raw.Interfaces),
		DefaultSpeed: defaultSpeed,
		Breakouts:    breakouts,
		Dataplane:    opts.Dataplane,
	}, nil
}

// modeRE captures the speed component from a SONiC breakout-mode
// string. Grammar verified against testdata/sonic-platform-json/:
//
//	1x40G                           — count x speed (no alt)
//	1x100G[40G]                     — single alt in brackets
//	1x400G                          — single 400G mode
//	1x800G[400G]                    — 800G with one alt
//	2x200G[100G,40G]                — multi-alt list
//	4x25G[10G]                      — densest break
//	1x100G(4)                       — lane-count override in parens
//	2x400G                          — 400G two-up
//
// Captures: count, primary speed, suffix (G/M).
var modeRE = regexp.MustCompile(`^(\d+)x(\d+)([GM])`)

// parseModeSpeed reads the primary speed from one mode key. Returns
// the speed as a canonical string (e.g. "100G") and the integer
// rate in megabits for ordering. Returns ok=false for unparseable
// strings — callers skip them rather than fail the whole import,
// because real platform.json files may carry exotic modes
// (gearbox, recirculation) that aren't worth blocking on.
func parseModeSpeed(mode string) (canonical string, rateMbps int, ok bool) {
	m := modeRE.FindStringSubmatch(mode)
	if len(m) != 4 {
		return "", 0, false
	}
	count, err1 := strconv.Atoi(m[1])
	speed, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil || count == 0 || speed == 0 {
		return "", 0, false
	}
	suffix := m[3]
	rate := speed
	if suffix == "G" {
		rate *= 1000
	}
	return fmt.Sprintf("%d%s", speed, suffix), rate, true
}

// deriveHeadlineSpeed returns the highest-rate 1xN mode across
// every interface's breakout_modes map. This is the "speed each
// port runs at without a breakout split" — the headline number an
// operator authoring a profile cares about. For a platform whose
// breakout_modes map advertises 1x100G + 4x25G[10G], the headline
// is 100G; the breakouts are the splits.
//
// Skips ports whose maps contain only breakout-split modes
// (count > 1) — those have no single-port headline. Errors only
// when NO port across the entire file has a parseable 1xN mode
// (operator can't reasonably proceed; the file may be malformed
// or use a convention this translator doesn't model).
func deriveHeadlineSpeed(interfaces map[string]sonicInterface) (string, error) {
	bestRate := 0
	bestCanonical := ""
	for _, iface := range interfaces {
		for mode := range iface.BreakoutModes {
			// Only 1xN modes count for "headline" — breakouts
			// (2x, 4x) describe splits, not the port's native
			// speed.
			m := modeRE.FindStringSubmatch(mode)
			if len(m) != 4 || m[1] != "1" {
				continue
			}
			canonical, rate, ok := parseModeSpeed(mode)
			if !ok {
				continue
			}
			if rate > bestRate {
				bestRate = rate
				bestCanonical = canonical
			}
		}
	}
	if bestCanonical == "" {
		return "", fmt.Errorf("no parseable 1xN mode in any port's breakout_modes (translator expects at least one non-breakout mode for the headline-speed derivation)")
	}
	return bestCanonical, nil
}

// isEmptyJSONMap reports whether raw is the JSON literal `{}` (with
// optional whitespace). Used by FromSONiCPlatformJSON to recognize
// the older per-HWSKU port_config.ini convention where platform.json
// ships an empty interfaces map and per-port info lives elsewhere
// (issue #190).
func isEmptyJSONMap(raw json.RawMessage) bool {
	for _, b := range raw {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b != '{' && b != '}' {
			return false
		}
	}
	// We've seen only `{`, `}`, and whitespace — verify exactly
	// one open and one close brace.
	open, close := 0, 0
	for _, b := range raw {
		if b == '{' {
			open++
		}
		if b == '}' {
			close++
		}
	}
	return open == 1 && close == 1
}

// unionBreakouts returns the sorted set of breakout_modes keys
// across every interface. Stable across runs (sort.Strings); empty
// when no interface has any breakout_modes (e.g., a platform.json
// with port entries but no breakout map at all — rare but valid).
func unionBreakouts(interfaces map[string]sonicInterface) []string {
	seen := make(map[string]bool)
	for _, iface := range interfaces {
		for mode := range iface.BreakoutModes {
			seen[mode] = true
		}
	}
	out := make([]string, 0, len(seen))
	for mode := range seen {
		out = append(out, mode)
	}
	sort.Strings(out)
	return out
}
