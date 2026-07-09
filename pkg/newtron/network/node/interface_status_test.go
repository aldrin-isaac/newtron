package node

import (
	"testing"
)

// Field values below are verbatim from a live cisco-p200-32x100-vs
// (3node-vs-newtcon switch1, COUNTERS:oid:0x1000000000002) — the mapping
// from SAI counter names to typed slots is the contract this file owns.
func TestParseCountersMapsSAIFields(t *testing.T) {
	raw := map[string]string{
		"SAI_PORT_STAT_IF_IN_OCTETS":          "123456789",
		"SAI_PORT_STAT_IF_IN_UCAST_PKTS":      "1000",
		"SAI_PORT_STAT_IF_IN_NON_UCAST_PKTS":  "42",
		"SAI_PORT_STAT_IF_IN_DISCARDS":        "3",
		"SAI_PORT_STAT_IF_IN_ERRORS":          "1",
		"SAI_PORT_STAT_IF_OUT_OCTETS":         "987654321",
		"SAI_PORT_STAT_IF_OUT_UCAST_PKTS":     "2000",
		"SAI_PORT_STAT_IF_OUT_NON_UCAST_PKTS": "84",
		"SAI_PORT_STAT_IF_OUT_DISCARDS":       "6",
		"SAI_PORT_STAT_IF_OUT_ERRORS":         "2",
		"SAI_PORT_STAT_ETHER_STATS_JABBERS":   "999", // unmapped SAI field — ignored, not an error
	}
	c := parseCounters(raw)
	want := InterfaceCounters{
		RxOctets: 123456789, RxUnicastPackets: 1000, RxNonUnicastPkts: 42, RxDiscards: 3, RxErrors: 1,
		TxOctets: 987654321, TxUnicastPackets: 2000, TxNonUnicastPkts: 84, TxDiscards: 6, TxErrors: 2,
	}
	if *c != want {
		t.Errorf("parseCounters = %+v, want %+v", *c, want)
	}
}

func TestParseCountersToleratesAbsentAndUnparseable(t *testing.T) {
	c := parseCounters(map[string]string{
		"SAI_PORT_STAT_IF_IN_OCTETS":  "not-a-number",
		"SAI_PORT_STAT_IF_OUT_OCTETS": "77",
		// all other mapped fields absent
	})
	if c.RxOctets != 0 {
		t.Errorf("unparseable RxOctets = %d, want 0", c.RxOctets)
	}
	if c.TxOctets != 77 {
		t.Errorf("TxOctets = %d, want 77", c.TxOctets)
	}
}

// RATES field names verified live (RATES:oid on cisco-p200-32x100-vs);
// SONiC also writes *_last raw sample fields — those are not rates and
// must not leak into the typed struct.
func TestParseRatesMapsRatesFields(t *testing.T) {
	raw := map[string]string{
		"RX_BPS":                          "1234.5",
		"RX_PPS":                          "10.25",
		"TX_BPS":                          "6789.0",
		"TX_PPS":                          "20.5",
		"FEC_PRE_BER":                     "1.5e-8",
		"FEC_POST_BER":                    "0",
		"PORT_STAT_IF_IN_UCAST_PKTS_last": "999", // raw sample, unmapped
	}
	r := parseRates(raw)
	want := InterfaceRates{RxBps: 1234.5, RxPps: 10.25, TxBps: 6789.0, TxPps: 20.5, FecPreBer: 1.5e-8, FecPostBer: 0}
	if *r != want {
		t.Errorf("parseRates = %+v, want %+v", *r, want)
	}
}
