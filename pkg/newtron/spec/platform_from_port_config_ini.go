// platform_from_port_config_ini.go — translate a SONiC HWSKU-level
// port_config.ini into a newtron PlatformSpec (issue #190).
//
// SONiC ships two distinct conventions for per-port shape:
//
//	1. Modern: platform.json's `interfaces` map carries every port
//	   with its `breakout_modes`. platform_from_sonic.go handles
//	   this — port_count, default_speed (headline 1xN mode), and
//	   breakouts all derive from one file.
//
//	2. Older / chassis: platform.json is chassis-metadata-only with
//	   `"interfaces": {}`. Per-port shape lives in
//	   `<hwsku>/port_config.ini` under the device tree (one per
//	   HWSKU; chassis platforms put one per line-card subdirectory).
//	   This file handles that path.
//
// Both conventions remain current in sonic-buildimage as of
// 2026 (see PR #189 + #191 empirical run notes). The translator
// supports both so an operator onboarding any in-tree SONiC
// platform can drive a single CLI command.
//
// port_config.ini grammar (verified against four real fixtures
// under testdata/sonic-port-config-ini/):
//
//	# name        lanes      alias              index   speed
//	Ethernet0     5,6,7,8    fourhundredGigE1/1 1       400000
//
// Comments start with `#`. The first comment line that contains
// `name` AND `speed` IS the header — the parser uses it to find
// the column positions for those two fields by NAME, not by
// fixed offset. Chassis-platform port_config.ini files extend
// the column set with `role`, `asic_port_name`, `core_id`,
// `core_port_id`, `num_voq` (verified on Nokia 7250 IXR-X3B);
// header-driven parsing handles that variation without
// special-casing.
//
// Data rows are whitespace-delimited (tab or spaces; SONiC uses
// both depending on the platform tree). Speed is in Kbps:
// `40000` → `40G`, `100000` → `100G`, `400000` → `400G`.
package spec

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// FromPortConfigINI parses a SONiC HWSKU-level port_config.ini byte
// stream and returns the derived PlatformSpec. opts.HWSKU is
// required.
//
// What this derivation provides (and what it does NOT):
//
//   - port_count = data-row count (rows that aren't comments or blank)
//   - default_speed = dominant speed across rows (the mode — most
//     common value). Per-port speed variation is real (management
//     ports run at 10G on chassis platforms with 400G data ports);
//     picking the dominant value gives the headline.
//   - ports = one PortSpec per data row, in file order. NIC slots are
//     assigned by that order (row N → data NIC N); each port carries its
//     own name, per-row speed, and lanes (when the header has a lanes
//     column). This is the explicit form of the name → NIC mapping.
//   - breakouts = empty. port_config.ini does NOT carry breakout-mode
//     information. Operators onboarding a port_config.ini platform
//     who need breakouts will hand-author them post-generation or
//     wait for a hwsku.json parser (separate follow-up).
//   - vm_interface_map = "sequential" — a fixed default, NOT derived from
//     the port names. It is deliberately not inferred: vm_interface_map is a
//     *deployment* property, not a property of the source port_config.ini.
//     The same Force10-S6000 file is "sequential" for the VPP variant (VPP
//     renumbers ports to a contiguous stride-1 scheme at boot — RCA-013/-020)
//     and works as "sequential" for the ASIC-sim VS, so inferring "stride-4"
//     from the stride-4 names would regress VPP. "sequential" is universally
//     correct: it orders QEMU NICs by Ethernet index (matching port_config.ini
//     row order for any naming) and yields PortStride=1 for the VPP boot patch.
//     Operators override to "stride-4" (port-name validation) or "custom"
//     (irregular layouts) when a platform needs it.
//
// What this derivation does NOT provide — the VM fields (vm_image, vm_memory,
// vm_cpus, vm_nic_driver, credentials, …) are deployment choices absent from
// port_config.ini; the operator completes them after generation.
//
// Errors on: empty opts.HWSKU; no header row (no comment line
// containing `name` AND `speed`); no `speed` column in the
// header; no data rows; malformed speed value.
func FromPortConfigINI(data []byte, opts SONiCImportOptions) (*PlatformSpec, error) {
	if opts.HWSKU == "" {
		return nil, fmt.Errorf("HWSKU is required (supply via SONiCImportOptions.HWSKU)")
	}
	cols, err := findColumns(data)
	if err != nil {
		return nil, err
	}
	rows := readDataRows(data)
	if len(rows) == 0 {
		return nil, fmt.Errorf("port_config.ini: no data rows (only comments / blanks); cannot derive port_count")
	}
	defaultSpeed, err := pickDominantSpeed(rows, cols.speed)
	if err != nil {
		return nil, fmt.Errorf("port_config.ini: %w", err)
	}
	return &PlatformSpec{
		HWSKU:        opts.HWSKU,
		Description:  opts.Description,
		DeviceType:   "switch",
		PortCount:    len(rows),
		DefaultSpeed: defaultSpeed,
		Breakouts:    nil, // not derivable from port_config.ini
		Ports:        buildPortsFromRows(rows, cols),
		Dataplane:    opts.Dataplane, // operator-supplied
		// Universal-safe default — see the function doc on why this is fixed,
		// not inferred from the port-name stride.
		VMInterfaceMap: "sequential",
	}, nil
}

// portConfigColumns holds the header column positions findColumns located.
// name and speed are required (findColumns errors otherwise); lanes is
// optional — lanesCol is -1 when the header has no `lanes` column.
type portConfigColumns struct {
	name  int
	speed int
	lanes int
}

// buildPortsFromRows produces the explicit per-port inventory. NIC slots are
// assigned by data-row order — the port_config.ini row order IS the
// authoritative QEMU NIC ordering (the Nth data port is backed by data NIC N;
// NIC 0 is management, never a row here). Each port carries its own speed (the
// row's speed cell) and lanes (when the header had a lanes column). A row too
// short to reach the name column cannot name a port and is skipped.
func buildPortsFromRows(rows [][]string, cols portConfigColumns) []PortSpec {
	ports := make([]PortSpec, 0, len(rows))
	for i, row := range rows {
		if cols.name >= len(row) {
			continue
		}
		p := PortSpec{Name: row[cols.name], NICIndex: i + 1}
		if cols.speed < len(row) {
			if kbps, err := strconv.Atoi(row[cols.speed]); err == nil {
				p.Speed = kbpsToCanonical(kbps)
			}
		}
		if cols.lanes >= 0 && cols.lanes < len(row) {
			p.Lanes = parseLanes(row[cols.lanes])
		}
		ports = append(ports, p)
	}
	return ports
}

// parseLanes splits a port_config.ini lanes cell ("101,102") into its integer
// serdes lanes. Unparseable entries are skipped; an all-unparseable cell
// yields nil so lanes stays omitted on the wire.
func parseLanes(cell string) []int {
	parts := strings.Split(cell, ",")
	lanes := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			lanes = append(lanes, n)
		}
	}
	if len(lanes) == 0 {
		return nil
	}
	return lanes
}

// findColumns scans for the header row — the first comment line that contains
// BOTH `name` AND `speed` as whitespace-separated tokens — and returns the
// positions of name, speed, and (when present) lanes. The leading `#` and any
// surrounding whitespace are stripped before tokenization, so
// `# name lanes alias index speed` and `#name lanes alias index speed` both
// parse identically. Chassis platforms extend the column set (role,
// asic_port_name, …); header-driven lookup by token name handles that without
// special-casing.
func findColumns(data []byte) (portConfigColumns, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "#") {
			continue
		}
		tokens := strings.Fields(strings.TrimLeft(line, "#"))
		cols := portConfigColumns{name: -1, speed: -1, lanes: -1}
		for i, tok := range tokens {
			switch tok {
			case "name":
				cols.name = i
			case "speed":
				cols.speed = i
			case "lanes":
				cols.lanes = i
			}
		}
		if cols.name >= 0 && cols.speed >= 0 {
			return cols, nil
		}
	}
	return portConfigColumns{}, fmt.Errorf("port_config.ini: no header row (expected a `# ... name ... speed ...` comment line as the column legend)")
}

// readDataRows returns every non-comment, non-blank line tokenized
// by whitespace. Tab and space delimiters are handled identically
// (Fields collapses runs of either).
func readDataRows(data []byte) [][]string {
	var rows [][]string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

// pickDominantSpeed returns the most-common speed across rows,
// converted from SONiC's Kbps wire form to the canonical PlatformSpec
// form (e.g. `400000` → `400G`). Ties on the dominant value resolve
// to the higher speed — picking the higher rate on a tie produces
// the "headline" answer an operator authoring a profile cares about.
//
// Rows whose speedCol position is beyond the row's token count
// are skipped (the operator's port_config.ini may include a row
// shape variation the header didn't anticipate). Rows whose speed
// value doesn't parse are also skipped. If every row is skipped,
// returns an error so the operator knows the file is malformed
// rather than getting a confusing zero-speed PlatformSpec.
func pickDominantSpeed(rows [][]string, speedCol int) (string, error) {
	counts := make(map[int]int)
	for _, row := range rows {
		if speedCol >= len(row) {
			continue
		}
		kbps, err := strconv.Atoi(row[speedCol])
		if err != nil {
			continue
		}
		counts[kbps]++
	}
	if len(counts) == 0 {
		return "", fmt.Errorf("no parseable speed values in any data row (header column %d, but every row's speed-column value failed to parse as an integer)", speedCol)
	}
	bestKbps, bestCount := 0, 0
	for kbps, count := range counts {
		if count > bestCount || (count == bestCount && kbps > bestKbps) {
			bestKbps = kbps
			bestCount = count
		}
	}
	return kbpsToCanonical(bestKbps), nil
}

// kbpsToCanonical renders a Kbps value as a PlatformSpec
// default_speed string. SONiC port_config.ini speeds are always in
// Kbps multiples of 1000 (10000=10G, 25000=25G, 40000=40G,
// 100000=100G, 200000=200G, 400000=400G, 800000=800G). Sub-Gbps
// values are theoretically possible but no in-tree platform has
// them; this helper renders them in Mbps form (e.g. `500M`) on the
// rare chance an operator's file carries one.
func kbpsToCanonical(kbps int) string {
	if kbps%1_000_000 == 0 {
		return fmt.Sprintf("%dT", kbps/1_000_000)
	}
	if kbps%1_000 == 0 {
		return fmt.Sprintf("%dG", kbps/1_000)
	}
	return fmt.Sprintf("%dM", kbps)
}
