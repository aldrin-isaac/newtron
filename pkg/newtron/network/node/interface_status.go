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

// Status composes the interface's live operational picture. Every section
// is best-effort observation: a DB or table a platform doesn't populate
// (COUNTERS_DB on some -vs, TRANSCEIVER_* on all -vs) yields a nil/empty
// section, not an error — the read reports what exists.
func (i *Interface) Status(ctx context.Context) (*InterfaceStatus, error) {
	st := &InterfaceStatus{Name: i.name}

	// Link state — STATE_DB PORT_TABLE is the substrate; oper_status lives
	// in APPL_DB PORT_TABLE (portsyncd publishes it there, not to STATE_DB).
	port, err := i.node.OperDBEntry(ctx, "STATE_DB", "PORT_TABLE", i.name)
	if err != nil {
		return nil, fmt.Errorf("reading STATE_DB PORT_TABLE for %s: %w", i.name, err)
	}
	st.AdminStatus = port["admin_status"]
	st.Speed = port["speed"]
	st.MTU = port["mtu"]
	st.FEC = port["fec"]
	st.HostTxReady = port["host_tx_ready"]
	if applPort, err := i.node.OperDBEntry(ctx, "APPL_DB", "PORT_TABLE", i.name); err == nil {
		st.OperStatus = applPort["oper_status"]
		if st.AdminStatus == "" {
			st.AdminStatus = applPort["admin_status"]
		}
	}

	// Counters + rates — resolve the interface's OID through
	// COUNTERS_PORT_NAME_MAP (a flat hash: key "" is the whole table).
	nameMap, err := i.node.OperDBEntry(ctx, "COUNTERS_DB", "COUNTERS_PORT_NAME_MAP", "")
	if err == nil {
		if oid := nameMap[i.name]; oid != "" {
			if raw, err := i.node.OperDBEntry(ctx, "COUNTERS_DB", "COUNTERS", oid); err == nil && len(raw) > 0 {
				st.Counters = parseCounters(raw)
			}
			if raw, err := i.node.OperDBEntry(ctx, "COUNTERS_DB", "RATES", oid); err == nil && len(raw) > 0 {
				st.Rates = parseRates(raw)
			}
		}
	}

	// Resolved neighbors — APPL_DB NEIGH_TABLE keys are
	// "<iface>:<address>"; scan the table and keep this interface's rows.
	st.Neighbors = []ARPNeighbor{}
	if neigh, err := i.node.OperDBTable(ctx, "APPL_DB", "NEIGH_TABLE"); err == nil {
		prefix := i.name + ":"
		for key, fields := range neigh {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			st.Neighbors = append(st.Neighbors, ARPNeighbor{
				Address: strings.TrimPrefix(key, prefix),
				MAC:     fields["neigh"],
				Family:  fields["family"],
			})
		}
		sort.Slice(st.Neighbors, func(a, b int) bool { return st.Neighbors[a].Address < st.Neighbors[b].Address })
	}

	// LLDP far end.
	if lldp, err := i.node.OperDBEntry(ctx, "APPL_DB", "LLDP_ENTRY_TABLE", i.name); err == nil && len(lldp) > 0 {
		st.LLDPPeer = &LLDPPeer{
			ChassisID:       lldp["lldp_rem_chassis_id"],
			PortID:          lldp["lldp_rem_port_id"],
			PortDescription: lldp["lldp_rem_port_desc"],
			SystemName:      lldp["lldp_rem_sys_name"],
			SystemDesc:      lldp["lldp_rem_sys_desc"],
		}
	}

	// Optics — present only where pmon populates the transceiver tables.
	info, _ := i.node.OperDBEntry(ctx, "STATE_DB", "TRANSCEIVER_INFO", i.name)
	if len(info) > 0 {
		dom, _ := i.node.OperDBEntry(ctx, "STATE_DB", "TRANSCEIVER_DOM_SENSOR", i.name)
		status, _ := i.node.OperDBEntry(ctx, "STATE_DB", "TRANSCEIVER_STATUS", i.name)
		st.Optics = &OpticsInfo{Present: true, Info: info, DOM: dom, Status: status}
	}

	return st, nil
}
