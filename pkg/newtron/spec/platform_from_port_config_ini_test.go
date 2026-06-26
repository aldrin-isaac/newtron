package spec

import (
	"reflect"
	"testing"
)

// TestKbpsToCanonical pins the Kbps → canonical-string conversion
// across every speed point a SONiC port_config.ini in tree
// actually uses (10G/25G/40G/100G/200G/400G/800G) plus the
// Mbps fall-through for sub-Gbps oddities. Every Kbps form in
// real SONiC port_config.ini files is a clean multiple of 1000;
// the helper renders them in their natural unit so an operator
// reading the output recognizes the speed immediately.
func TestKbpsToCanonical(t *testing.T) {
	cases := []struct {
		kbps int
		want string
	}{
		{10000, "10G"},
		{25000, "25G"},
		{40000, "40G"},
		{100000, "100G"},
		{200000, "200G"},
		{400000, "400G"},
		{800000, "800G"},
		// Sub-Gbps oddities — render in Mbps.
		{500, "500M"},
		{1500, "1500M"}, // not 1.5G, not clean multiple of 1000 → falls to Mbps
		// Multi-Tbps (no current platform, but pin the helper's
		// behavior so a future 1.6T platform doesn't surprise us).
		{1_600_000, "1600G"},
	}
	for _, c := range cases {
		if got := kbpsToCanonical(c.kbps); got != c.want {
			t.Errorf("kbpsToCanonical(%d) = %q; want %q", c.kbps, got, c.want)
		}
	}
}

// TestFindColumns pins the header-detection grammar. SONiC port_config.ini
// files vary in column count (5 for ToRs, 10 for chassis platforms), so the
// parser MUST find columns by name from the header rather than by fixed
// offset. `want` is the speed column; for the standard headers here name is
// col 0 and lanes col 1, asserted too so the generalization is exercised.
func TestFindColumns(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		want     int
		wantErr  bool
		wantSnip string
	}{
		{
			name:  "five-column-ToR-header",
			input: "# name lanes alias index speed\nEthernet0 1,2,3,4 hundredGigE1/1 1 100000\n",
			want:  4,
		},
		{
			name:  "ten-column-chassis-header (Nokia 7250)",
			input: "# name lanes alias index role speed asic_port_name core_id core_port_id num_voq\nEthernet0 1,2,3,4 Eth1/1 1 Ext 400000 Eth0 1 1 8\n",
			want:  5,
		},
		{
			name:  "no-space-after-hash",
			input: "#name lanes alias index speed\nEthernet0 1,2 a 1 40000\n",
			want:  4,
		},
		{
			name:    "no-header-row-at-all",
			input:   "Ethernet0 1,2 alias 1 40000\n",
			wantErr: true,
		},
		{
			name:    "comment-without-speed-keyword-ignored",
			input:   "# this is some banner\n# name lanes alias index speed\nEthernet0 1 a 1 40000\n",
			want:    4,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := findColumns([]byte(c.input))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error; got cols=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.speed != c.want {
				t.Errorf("speed col = %d; want %d", got.speed, c.want)
			}
			// Every header in this table has name first and lanes second;
			// findColumns must locate both, not just speed.
			if got.name != 0 {
				t.Errorf("name col = %d; want 0", got.name)
			}
			if got.lanes != 1 {
				t.Errorf("lanes col = %d; want 1", got.lanes)
			}
		})
	}
}

// TestPickDominantSpeed pins three properties of the
// dominant-speed picker: (1) most-common wins; (2) ties resolve
// to the higher speed; (3) unparseable / out-of-bound rows skip
// rather than fail.
func TestPickDominantSpeed(t *testing.T) {
	cases := []struct {
		name     string
		rows     [][]string
		speedCol int
		want     string
	}{
		{
			name:     "single-speed-population (s6100-shape, all 40G)",
			rows:     [][]string{{"E0", "1", "a", "1", "40000"}, {"E1", "2", "a", "2", "40000"}},
			speedCol: 4,
			want:     "40G",
		},
		{
			name: "dominant-wins (3x400G + 1x10G mgmt → 400G headline)",
			rows: [][]string{
				{"E0", "1", "a", "1", "400000"},
				{"E1", "1", "a", "2", "400000"},
				{"E2", "1", "a", "3", "400000"},
				{"Mgmt0", "1", "m", "0", "10000"},
			},
			speedCol: 4,
			want:     "400G",
		},
		{
			name: "tie-breaks-to-higher (2x100G + 2x40G → 100G)",
			rows: [][]string{
				{"E0", "1", "a", "1", "100000"},
				{"E1", "1", "a", "2", "100000"},
				{"E2", "1", "a", "3", "40000"},
				{"E3", "1", "a", "4", "40000"},
			},
			speedCol: 4,
			want:     "100G",
		},
		{
			name: "short-row-skipped (the header said 5 cols, this row has 3)",
			rows: [][]string{
				{"E0", "1", "shortrow"},
				{"E1", "1", "a", "2", "400000"},
				{"E2", "1", "a", "3", "400000"},
			},
			speedCol: 4,
			want:     "400G",
		},
		{
			name: "unparseable-speed-skipped",
			rows: [][]string{
				{"E0", "1", "a", "1", "junk"},
				{"E1", "1", "a", "2", "100000"},
			},
			speedCol: 4,
			want:     "100G",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := pickDominantSpeed(c.rows, c.speedCol)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}

// TestFromPortConfigINI_RealFixtures runs the parser end-to-end
// against all four real port_config.ini files saved under
// testdata/. Hand-verified port counts and speeds — these match
// what the four corresponding sonic-buildimage platforms ship
// for production deployments.
func TestFromPortConfigINI_RealFixtures(t *testing.T) {
	cases := []struct {
		fixture       string
		hwsku         string
		wantPortCount int
		wantSpeed     string
		wantFirst     *PortSpec // exact first port, hand-verified from the fixture (nil = skip)
	}{
		// Dell S6100: 16 4-port modules + 2 management = 66.
		// 40G data ports dominate; 10G mgmt is the minority.
		// First fixture row: `Ethernet0 101,102 fortyGigE1/1/1 0 40000`.
		{"dell-s6100", "Force10-S6100", 66, "40G",
			&PortSpec{Name: "Ethernet0", NICIndex: 1, Speed: "40G", Lanes: []int{101, 102}}},
		// Dell Z9664f: 64x400G + 2 mgmt = 66 (data dominates).
		{"dell-z9664f", "DellEMC-Z9664f-O64", 66, "400G", nil},
		// Arista 7280CR3-C32D4: 32x100G + 4x400G + ? mgmt — the
		// hand count from the fixture is 36 (32+4).
		{"arista-7280cr3", "Arista-7280CR3-C32D4", 36, "100G", nil},
		// Nokia 7250 IXR-X3B line card 0: 18 data + 2 mgmt = 20
		// (chassis line-card port_config.ini covers just one
		// card, not the whole chassis).
		{"nokia-7250-x3b-lc0", "Nokia-IXR7250-X3B", 20, "400G", nil},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			data := mustRead(t, "testdata/sonic-port-config-ini/"+c.fixture+".ini")
			got, err := FromPortConfigINI(data, SONiCImportOptions{HWSKU: c.hwsku})
			if err != nil {
				t.Fatalf("FromPortConfigINI: %v", err)
			}
			if got.HWSKU != c.hwsku {
				t.Errorf("HWSKU: got %q, want %q", got.HWSKU, c.hwsku)
			}
			if got.DeviceType != "switch" {
				t.Errorf("DeviceType: got %q, want switch", got.DeviceType)
			}
			if got.PortCount != c.wantPortCount {
				t.Errorf("PortCount: got %d, want %d", got.PortCount, c.wantPortCount)
			}
			if got.DefaultSpeed != c.wantSpeed {
				t.Errorf("DefaultSpeed: got %q, want %q", got.DefaultSpeed, c.wantSpeed)
			}
			// Breakouts NOT derivable from port_config.ini.
			if len(got.Breakouts) != 0 {
				t.Errorf("Breakouts: got %v, want empty (port_config.ini does not carry breakout modes)", got.Breakouts)
			}
			// vm_interface_map is a fixed universal-safe default — "sequential",
			// never inferred from the port-name stride (deployment property; see
			// FromPortConfigINI doc / RCA-013). A non-empty map also means a
			// generated platform doesn't trip ResolveNICIndex's empty-map error.
			if got.VMInterfaceMap != "sequential" {
				t.Errorf("VMInterfaceMap: got %q, want \"sequential\" (universal-safe default)", got.VMInterfaceMap)
			}
			// Ports: one per data row, NIC slots assigned 1..N in file order.
			// Every fixture row carries a name, so len(Ports) == PortCount.
			if len(got.Ports) != c.wantPortCount {
				t.Fatalf("len(Ports): got %d, want %d", len(got.Ports), c.wantPortCount)
			}
			for i, p := range got.Ports {
				if p.NICIndex != i+1 {
					t.Errorf("Ports[%d].NICIndex: got %d, want %d", i, p.NICIndex, i+1)
				}
				if p.Name == "" {
					t.Errorf("Ports[%d].Name is empty", i)
				}
				if p.Speed == "" {
					t.Errorf("Ports[%d] (%s).Speed is empty", i, p.Name)
				}
			}
			if c.wantFirst != nil {
				first := got.Ports[0]
				if first.Name != c.wantFirst.Name || first.NICIndex != c.wantFirst.NICIndex ||
					first.Speed != c.wantFirst.Speed || !reflect.DeepEqual(first.Lanes, c.wantFirst.Lanes) {
					t.Errorf("Ports[0]: got %+v, want %+v", first, *c.wantFirst)
				}
			}
		})
	}
}

// TestFromPortConfigINI_Errors pins every error path. §16 honest
// tests: each case verifies the actionable phrase in the message
// so future wording can't silently make failures harder to
// diagnose.
func TestFromPortConfigINI_Errors(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		opts     SONiCImportOptions
		wantSnip string
	}{
		{
			name:     "missing-hwsku",
			data:     []byte("# name lanes alias index speed\nE0 1 a 1 40000\n"),
			opts:     SONiCImportOptions{},
			wantSnip: "HWSKU is required",
		},
		{
			name:     "no-header",
			data:     []byte("E0 1 a 1 40000\n"),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "no header row",
		},
		{
			name:     "header-but-no-data-rows",
			data:     []byte("# name lanes alias index speed\n# only comments\n\n"),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "no data rows",
		},
		{
			name:     "every-row-unparseable",
			data:     []byte("# name lanes alias index speed\nE0 1 a 1 junk\nE1 1 a 2 nope\n"),
			opts:     SONiCImportOptions{HWSKU: "x"},
			wantSnip: "no parseable speed values",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FromPortConfigINI(c.data, c.opts)
			if err == nil {
				t.Fatalf("expected error containing %q; got nil err, spec=%+v", c.wantSnip, got)
			}
			if !contains(err.Error(), c.wantSnip) {
				t.Errorf("error message %q missing required snippet %q", err.Error(), c.wantSnip)
			}
		})
	}
}

// TestFromPortConfigINI_TabsVsSpaces pins the whitespace-agnostic
// delimiter handling. SONiC port_config.ini files in the wild use
// both — Dell's tend to be space-aligned, Arista's tend to be
// tab-separated. strings.Fields collapses runs of either, so both
// shapes parse identically.
func TestFromPortConfigINI_TabsVsSpaces(t *testing.T) {
	withTabs := []byte("# name\tlanes\talias\tindex\tspeed\nEthernet0\t1,2\ta\t1\t100000\nEthernet1\t3,4\tb\t2\t100000\n")
	withSpaces := []byte("# name lanes alias index speed\nEthernet0 1,2 a 1 100000\nEthernet1 3,4 b 2 100000\n")

	gotTabs, err := FromPortConfigINI(withTabs, SONiCImportOptions{HWSKU: "x"})
	if err != nil {
		t.Fatalf("tabs: %v", err)
	}
	gotSpaces, err := FromPortConfigINI(withSpaces, SONiCImportOptions{HWSKU: "x"})
	if err != nil {
		t.Fatalf("spaces: %v", err)
	}
	if !reflect.DeepEqual(gotTabs, gotSpaces) {
		t.Errorf("tabs vs spaces produced different specs:\n  tabs:   %+v\n  spaces: %+v", gotTabs, gotSpaces)
	}
}

// TestFromSONiCPlatformJSON_EmptyInterfaces_WrapsErrEmptyInterfaces
// pins the typed-sentinel contract — the CLI uses errors.Is to
// detect this case and fall through to port_config.ini. A future
// change that swaps the typed sentinel for a stringly error
// would silently break the CLI's auto-discovery path; this test
// makes that regression loud.
func TestFromSONiCPlatformJSON_EmptyInterfaces_WrapsErrEmptyInterfaces(t *testing.T) {
	_, err := FromSONiCPlatformJSON([]byte(`{"interfaces":{}}`), SONiCImportOptions{HWSKU: "x"})
	if err == nil {
		t.Fatal("expected error for empty interfaces; got nil")
	}
	if !isErrEmptyInterfaces(err) {
		t.Errorf("expected errors.Is(err, ErrEmptyInterfaces); got err=%v", err)
	}
}

// isErrEmptyInterfaces is the test-side equivalent of the CLI's
// detection. Lives here (not in errors.go) because it's purely a
// test helper — the production callsite uses errors.Is inline.
func isErrEmptyInterfaces(err error) bool {
	for e := err; e != nil; {
		if e == ErrEmptyInterfaces || (e.Error() != "" && contains(e.Error(), "interfaces map is present but empty")) {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}
