package node

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ============================================================================
// Curated per-interface operational status — the composed read that turns
// "BGP Active" into a full interface health picture in one call. Composes
// live reads across three operational DBs (STATE_DB, APPL_DB, COUNTERS_DB)
// so callers never touch the COUNTERS_DB OID indirection or per-DB key
// separators. Pure observation (§4): every value is reported as the daemons
// wrote it; newtron asserts nothing about correctness.
//
// This file owns the operational-DB table/field knowledge for interface
// status (§28) — the SAI counter names, the RATES fields, the NEIGH_TABLE
// and LLDP_ENTRY_TABLE layouts. Field maps verified live on
// cisco-p200-32x100-vs and sonic-vs (202505).
// ============================================================================

// InterfaceStatus is the composed operational picture of one interface.
type InterfaceStatus struct {
	Name string

	// STATE_DB PORT_TABLE (link state as portsyncd/xcvrd wrote it),
	// oper_status from APPL_DB PORT_TABLE (as portsyncd published it).
	AdminStatus string
	OperStatus  string
	Speed       string
	MTU         string
	FEC         string
	HostTxReady string

	// COUNTERS_DB COUNTERS:<oid> — cumulative SAI counters.
	Counters *InterfaceCounters

	// COUNTERS_DB RATES:<oid> — SONiC-computed rates; no poll-twice math.
	Rates *InterfaceRates

	// APPL_DB NEIGH_TABLE — RESOLVED neighbors only. The kernel does not
	// publish INCOMPLETE entries to APPL_DB, so an expected-but-absent
	// neighbor here IS the unresolved-ARP signal.
	Neighbors []ARPNeighbor

	// APPL_DB LLDP_ENTRY_TABLE — the far end as LLDP heard it.
	LLDPPeer *LLDPPeer

	// STATE_DB TRANSCEIVER_INFO / _DOM_SENSOR / _STATUS, passed through
	// as written — populated on physical hardware, absent on -vs platforms
	// (pmon has no sensors to read). Raw maps rather than curated fields:
	// the DOM schema varies by module type and the observation surface
	// reports what exists.
	Optics *OpticsInfo

	// Members — the constituent member ports of a composite interface: a
	// PortChannel's members, or an SVI's VLAN members. Nil for a physical
	// port (it has no members). Kind-aware: the composite's own row lives in
	// LAG_TABLE (LAG) or has no port row at all (SVI), so the member link
	// state is the operative detail a status on VlanN / PortChannelN must show.
	Members []MemberStatus
}

// MemberStatus is one constituent port of a composite interface — a PortChannel
// member or an SVI's VLAN member — reporting the member's link state as the oper
// DBs wrote it (§4). A composite forwards through its members (a LAG bundles a
// member; an SVI's bridge reaches a host over a member port), so a member's
// oper_status is the signal that explains a composite's reachability.
type MemberStatus struct {
	Name        string
	AdminStatus string
	OperStatus  string
	Speed       string
}

// InterfaceCounters holds the cumulative SAI port counters.
type InterfaceCounters struct {
	RxOctets         uint64
	RxUnicastPackets uint64
	RxNonUnicastPkts uint64
	RxDiscards       uint64
	RxErrors         uint64
	TxOctets         uint64
	TxUnicastPackets uint64
	TxNonUnicastPkts uint64
	TxDiscards       uint64
	TxErrors         uint64
}

// InterfaceRates holds the SONiC-computed port rates.
type InterfaceRates struct {
	RxBps      float64
	RxPps      float64
	TxBps      float64
	TxPps      float64
	FecPreBer  float64
	FecPostBer float64
}

// ARPNeighbor is one resolved L3 adjacency on the interface. The field is
// `address`, not `neighbor_ip`: this is an observed next-hop-side address,
// not a BGP peer identity (api.md wire conventions).
type ARPNeighbor struct {
	Address string
	MAC     string
	Family  string
}

// LLDPPeer is the interface's far end as LLDP reported it.
type LLDPPeer struct {
	ChassisID       string
	PortID          string
	PortDescription string
	SystemName      string
	SystemDesc      string
}

// OpticsInfo passes through the transceiver tables for the interface.
type OpticsInfo struct {
	Present bool
	Info    map[string]string
	DOM     map[string]string
	Status  map[string]string
}

// saiCounterFields maps SAI counter names to their InterfaceCounters slot —
// the one owner of the COUNTERS_DB field vocabulary.
var saiCounterFields = map[string]func(*InterfaceCounters, uint64){
	"SAI_PORT_STAT_IF_IN_OCTETS":          func(c *InterfaceCounters, v uint64) { c.RxOctets = v },
	"SAI_PORT_STAT_IF_IN_UCAST_PKTS":      func(c *InterfaceCounters, v uint64) { c.RxUnicastPackets = v },
	"SAI_PORT_STAT_IF_IN_NON_UCAST_PKTS":  func(c *InterfaceCounters, v uint64) { c.RxNonUnicastPkts = v },
	"SAI_PORT_STAT_IF_IN_DISCARDS":        func(c *InterfaceCounters, v uint64) { c.RxDiscards = v },
	"SAI_PORT_STAT_IF_IN_ERRORS":          func(c *InterfaceCounters, v uint64) { c.RxErrors = v },
	"SAI_PORT_STAT_IF_OUT_OCTETS":         func(c *InterfaceCounters, v uint64) { c.TxOctets = v },
	"SAI_PORT_STAT_IF_OUT_UCAST_PKTS":     func(c *InterfaceCounters, v uint64) { c.TxUnicastPackets = v },
	"SAI_PORT_STAT_IF_OUT_NON_UCAST_PKTS": func(c *InterfaceCounters, v uint64) { c.TxNonUnicastPkts = v },
	"SAI_PORT_STAT_IF_OUT_DISCARDS":       func(c *InterfaceCounters, v uint64) { c.TxDiscards = v },
	"SAI_PORT_STAT_IF_OUT_ERRORS":         func(c *InterfaceCounters, v uint64) { c.TxErrors = v },
}

// ratesFields maps RATES field names to their InterfaceRates slot.
var ratesFields = map[string]func(*InterfaceRates, float64){
	"RX_BPS":       func(r *InterfaceRates, v float64) { r.RxBps = v },
	"RX_PPS":       func(r *InterfaceRates, v float64) { r.RxPps = v },
	"TX_BPS":       func(r *InterfaceRates, v float64) { r.TxBps = v },
	"TX_PPS":       func(r *InterfaceRates, v float64) { r.TxPps = v },
	"FEC_PRE_BER":  func(r *InterfaceRates, v float64) { r.FecPreBer = v },
	"FEC_POST_BER": func(r *InterfaceRates, v float64) { r.FecPostBer = v },
}

// parseCounters converts a raw COUNTERS:<oid> hash into typed counters.
// Unparseable or absent fields stay zero — observation reports what exists.
func parseCounters(raw map[string]string) *InterfaceCounters {
	c := &InterfaceCounters{}
	for field, set := range saiCounterFields {
		if v, err := strconv.ParseUint(raw[field], 10, 64); err == nil {
			set(c, v)
		}
	}
	return c
}

// parseRates converts a raw RATES:<oid> hash into typed rates.
func parseRates(raw map[string]string) *InterfaceRates {
	r := &InterfaceRates{}
	for field, set := range ratesFields {
		if v, err := strconv.ParseFloat(raw[field], 64); err == nil {
			set(r, v)
		}
	}
	return r
}

// Status composes the interface's live operational picture, dispatched on the
// interface kind — the composite's own row lives in a different table per kind
// (a physical port in PORT_TABLE, a PortChannel in LAG_TABLE, an SVI in neither),
// and a composite additionally reports its member ports. Beyond a physical port's
// PORT_TABLE row (whose absence is a genuine fault), every section is best-effort
// observation: a DB or table a platform doesn't populate yields a nil/empty
// section, not an error — the read reports what exists (§4).
func (i *Interface) Status(ctx context.Context) (*InterfaceStatus, error) {
	st := &InterfaceStatus{Name: i.name}
	switch interfaceKindOf(i.name) {
	case KindPortChannel:
		i.readLAGStatus(ctx, st)
	case KindIRB:
		i.readSVIStatus(ctx, st)
	default:
		// Physical port (and any other name): its PORT_TABLE row must exist.
		if err := i.readPortStatus(ctx, st); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// portLink is the link-state slice of a physical port, read from STATE_DB
// PORT_TABLE and APPL_DB PORT_TABLE. Shared by the physical-port path and the member
// reader so both draw the same fields from the same tables (§30).
type portLink struct{ admin, oper, speed, mtu, fec, hostTxReady string }

// readPortLink reads a physical port's link state. STATE_DB PORT_TABLE is the
// substrate; oper_status lives in APPL_DB PORT_TABLE (portsyncd publishes it
// there, not to STATE_DB). The error is the STATE_DB miss — the caller decides
// whether that is a fault (a physical port) or an empty member.
func (i *Interface) readPortLink(ctx context.Context, name string) (portLink, error) {
	port, err := i.node.OperDBEntry(ctx, "STATE_DB", "PORT_TABLE", name)
	if err != nil {
		return portLink{}, err
	}
	l := portLink{admin: port["admin_status"], speed: port["speed"], mtu: port["mtu"], fec: port["fec"], hostTxReady: port["host_tx_ready"]}
	if appl, err := i.node.OperDBEntry(ctx, "APPL_DB", "PORT_TABLE", name); err == nil {
		l.oper = appl["oper_status"]
		if l.admin == "" {
			l.admin = appl["admin_status"]
		}
	}
	return l, nil
}

// readPortStatus fills st for a physical port: link state, counters, resolved
// neighbors, LLDP far end, and optics. The PORT_TABLE read is the one hard error —
// a physical port without a STATE_DB row is a genuine fault, not an empty read.
func (i *Interface) readPortStatus(ctx context.Context, st *InterfaceStatus) error {
	l, err := i.readPortLink(ctx, i.name)
	if err != nil {
		return fmt.Errorf("reading STATE_DB PORT_TABLE for %s: %w", i.name, err)
	}
	st.AdminStatus, st.OperStatus, st.Speed = l.admin, l.oper, l.speed
	st.MTU, st.FEC, st.HostTxReady = l.mtu, l.fec, l.hostTxReady
	i.readCountersVia(ctx, st, "COUNTERS_PORT_NAME_MAP")
	st.Neighbors = i.readNeighbors(ctx)

	// LLDP far end + optics — physical-port only.
	if lldp, err := i.node.OperDBEntry(ctx, "APPL_DB", "LLDP_ENTRY_TABLE", i.name); err == nil && len(lldp) > 0 {
		st.LLDPPeer = &LLDPPeer{
			ChassisID:       lldp["lldp_rem_chassis_id"],
			PortID:          lldp["lldp_rem_port_id"],
			PortDescription: lldp["lldp_rem_port_desc"],
			SystemName:      lldp["lldp_rem_sys_name"],
			SystemDesc:      lldp["lldp_rem_sys_desc"],
		}
	}
	if info, _ := i.node.OperDBEntry(ctx, "STATE_DB", "TRANSCEIVER_INFO", i.name); len(info) > 0 {
		dom, _ := i.node.OperDBEntry(ctx, "STATE_DB", "TRANSCEIVER_DOM_SENSOR", i.name)
		status, _ := i.node.OperDBEntry(ctx, "STATE_DB", "TRANSCEIVER_STATUS", i.name)
		st.Optics = &OpticsInfo{Present: true, Info: info, DOM: dom, Status: status}
	}
	return nil
}

// readLAGStatus fills st for a PortChannel from APPL_DB LAG_TABLE (the aggregate's
// admin/oper as teamd/lagmgrd published it), its LAG counters, resolved neighbors
// (a LAG can carry L3), and its member ports. No LLDP/optics — a LAG is not a
// physical port. Best-effort: an unpopulated table yields an empty section.
func (i *Interface) readLAGStatus(ctx context.Context, st *InterfaceStatus) {
	if lag, err := i.node.OperDBEntry(ctx, "APPL_DB", "LAG_TABLE", i.name); err == nil {
		st.AdminStatus = lag["admin_status"]
		st.OperStatus = lag["oper_status"]
		st.MTU = lag["mtu"]
	}
	i.readCountersVia(ctx, st, "COUNTERS_LAG_NAME_MAP")
	st.Neighbors = i.readNeighbors(ctx)
	st.Members = i.readMembers(ctx, i.PortChannelMembers())
}

// readSVIStatus fills st for an SVI (VlanN). An SVI has no PORT_TABLE or LAG_TABLE
// row; its L2 oper lives in APPL_DB VLAN_TABLE (vlanmgrd), it can carry L3 (so
// resolved neighbors apply), and its members are the VLAN's member ports. Best-effort.
func (i *Interface) readSVIStatus(ctx context.Context, st *InterfaceStatus) {
	if vlan, err := i.node.OperDBEntry(ctx, "APPL_DB", "VLAN_TABLE", i.name); err == nil {
		st.AdminStatus = vlan["admin_status"]
		st.OperStatus = vlan["oper_status"]
		st.MTU = vlan["mtu"]
	}
	st.Neighbors = i.readNeighbors(ctx)
	st.Members = i.readMembers(ctx, i.VLANMembers())
}

// readMembers reads each member port's link state, sorted for a stable list.
// Shared by the LAG and SVI paths (§30); best-effort per member — a member with no
// STATE_DB row yields an empty entry, not an error.
func (i *Interface) readMembers(ctx context.Context, members []string) []MemberStatus {
	out := make([]MemberStatus, 0, len(members))
	for _, m := range members {
		l, _ := i.readPortLink(ctx, m)
		out = append(out, MemberStatus{Name: m, AdminStatus: l.admin, OperStatus: l.oper, Speed: l.speed})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// readCountersVia resolves the interface's counter OID through the given
// NAME_MAP (COUNTERS_PORT_NAME_MAP for ports, COUNTERS_LAG_NAME_MAP for LAGs — a
// flat hash whose key "" is the whole table) and fills counters + rates.
func (i *Interface) readCountersVia(ctx context.Context, st *InterfaceStatus, nameMapTable string) {
	nameMap, err := i.node.OperDBEntry(ctx, "COUNTERS_DB", nameMapTable, "")
	if err != nil {
		return
	}
	oid := nameMap[i.name]
	if oid == "" {
		return
	}
	if raw, err := i.node.OperDBEntry(ctx, "COUNTERS_DB", "COUNTERS", oid); err == nil && len(raw) > 0 {
		st.Counters = parseCounters(raw)
	}
	if raw, err := i.node.OperDBEntry(ctx, "COUNTERS_DB", "RATES", oid); err == nil && len(raw) > 0 {
		st.Rates = parseRates(raw)
	}
}

// readNeighbors scans APPL_DB NEIGH_TABLE (keys "<iface>:<address>") for this
// interface's resolved adjacencies, sorted by address. Shared by every
// L3-capable kind (physical, LAG, SVI).
func (i *Interface) readNeighbors(ctx context.Context) []ARPNeighbor {
	out := []ARPNeighbor{}
	neigh, err := i.node.OperDBTable(ctx, "APPL_DB", "NEIGH_TABLE")
	if err != nil {
		return out
	}
	prefix := i.name + ":"
	for key, fields := range neigh {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		out = append(out, ARPNeighbor{
			Address: strings.TrimPrefix(key, prefix),
			MAC:     fields["neigh"],
			Family:  fields["family"],
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Address < out[b].Address })
	return out
}
