package network

import (
	"fmt"
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

func TestGenerateQoSDeviceEntries_TwoQueue(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "best-effort", Type: "dwrr", Weight: 70, DSCP: []int{0}},
			{Name: "voice", Type: "strict", DSCP: []int{46}},
		},
	}

	entries := generateQoSDeviceEntries("test-2q", policy)

	// Expect: 1 DSCP_TO_TC_MAP + 1 TC_TO_QUEUE_MAP + 2 SCHEDULER = 4 entries
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// DSCP_TO_TC_MAP
	dscpMap := entries[0]
	if dscpMap.Table != "DSCP_TO_TC_MAP" || dscpMap.Key != "test-2q" {
		t.Errorf("entry[0]: got %s|%s, want DSCP_TO_TC_MAP|test-2q", dscpMap.Table, dscpMap.Key)
	}
	if len(dscpMap.Fields) != 64 {
		t.Errorf("DSCP map should have 64 entries, got %d", len(dscpMap.Fields))
	}
	// DSCP 0 → "0" (queue 0), DSCP 46 → "1" (queue 1), unmapped → "0"
	if dscpMap.Fields["0"] != "0" {
		t.Errorf("DSCP 0 should map to TC 0, got %q", dscpMap.Fields["0"])
	}
	if dscpMap.Fields["46"] != "1" {
		t.Errorf("DSCP 46 should map to TC 1, got %q", dscpMap.Fields["46"])
	}
	if dscpMap.Fields["10"] != "0" {
		t.Errorf("unmapped DSCP 10 should default to TC 0, got %q", dscpMap.Fields["10"])
	}

	// TC_TO_QUEUE_MAP
	tcMap := entries[1]
	if tcMap.Table != "TC_TO_QUEUE_MAP" || tcMap.Key != "test-2q" {
		t.Errorf("entry[1]: got %s|%s, want TC_TO_QUEUE_MAP|test-2q", tcMap.Table, tcMap.Key)
	}
	if len(tcMap.Fields) != 2 {
		t.Errorf("TC map should have 2 entries, got %d", len(tcMap.Fields))
	}
	if tcMap.Fields["0"] != "0" || tcMap.Fields["1"] != "1" {
		t.Errorf("TC map should be identity: got %v", tcMap.Fields)
	}

	// SCHEDULER entries
	sched0 := entries[2]
	if sched0.Table != "SCHEDULER" || sched0.Key != "test-2q.0" {
		t.Errorf("entry[2]: got %s|%s, want SCHEDULER|test-2q.0", sched0.Table, sched0.Key)
	}
	if sched0.Fields["type"] != "DWRR" {
		t.Errorf("scheduler 0 type: got %q, want DWRR", sched0.Fields["type"])
	}
	if sched0.Fields["weight"] != "70" {
		t.Errorf("scheduler 0 weight: got %q, want 70", sched0.Fields["weight"])
	}

	sched1 := entries[3]
	if sched1.Fields["type"] != "STRICT" {
		t.Errorf("scheduler 1 type: got %q, want STRICT", sched1.Fields["type"])
	}
	if _, hasWeight := sched1.Fields["weight"]; hasWeight {
		t.Error("strict scheduler should not have weight")
	}
}

func TestGenerateQoSDeviceEntries_EightQueueWithECN(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "be", Type: "dwrr", Weight: 20, DSCP: []int{0}},
			{Name: "bulk", Type: "dwrr", Weight: 15, DSCP: []int{8, 10, 12, 14}},
			{Name: "tx", Type: "dwrr", Weight: 15, DSCP: []int{18, 20, 22}},
			{Name: "lossless", Type: "dwrr", Weight: 10, DSCP: []int{3, 4}, ECN: true},
			{Name: "lossless-hi", Type: "dwrr", Weight: 10, DSCP: []int{19, 21}, ECN: true},
			{Name: "voice", Type: "strict", DSCP: []int{46}},
			{Name: "signaling", Type: "dwrr", Weight: 10, DSCP: []int{24, 26, 48}},
			{Name: "nc", Type: "strict", DSCP: []int{56}},
		},
	}

	entries := generateQoSDeviceEntries("8q-dc", policy)

	// 1 DSCP + 1 TC + 8 SCHEDULER + 1 WRED = 11
	if len(entries) != 11 {
		t.Fatalf("expected 11 entries, got %d", len(entries))
	}

	// Last entry should be WRED_PROFILE
	wred := entries[10]
	if wred.Table != "WRED_PROFILE" || wred.Key != "8q-dc.ecn" {
		t.Errorf("last entry: got %s|%s, want WRED_PROFILE|8q-dc.ecn", wred.Table, wred.Key)
	}
	if wred.Fields["ecn"] != "ecn_all" {
		t.Errorf("WRED ecn: got %q, want ecn_all", wred.Fields["ecn"])
	}
}

func TestGenerateQoSDeviceEntries_NoECN(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "be", Type: "dwrr", Weight: 50, DSCP: []int{0}},
			{Name: "nc", Type: "strict", DSCP: []int{48}},
		},
	}

	entries := generateQoSDeviceEntries("no-ecn", policy)

	// 1 DSCP + 1 TC + 2 SCHEDULER = 4 (no WRED)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries (no WRED), got %d", len(entries))
	}
	for _, e := range entries {
		if e.Table == "WRED_PROFILE" {
			t.Error("should not have WRED_PROFILE entry without ECN")
		}
	}
}

func TestGenerateQoSInterfaceEntries(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "be", Type: "dwrr", Weight: 40, DSCP: []int{0}},
			{Name: "voice", Type: "strict", DSCP: []int{46}},
			{Name: "lossless", Type: "dwrr", Weight: 20, DSCP: []int{3}, ECN: true},
		},
	}

	entries := generateQoSInterfaceEntries("test-3q", policy, "Ethernet0")

	// 1 PORT_QOS_MAP + 3 QUEUE = 4
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// PORT_QOS_MAP
	portMap := entries[0]
	if portMap.Table != "PORT_QOS_MAP" || portMap.Key != "Ethernet0" {
		t.Errorf("entry[0]: got %s|%s, want PORT_QOS_MAP|Ethernet0", portMap.Table, portMap.Key)
	}
	if portMap.Fields["dscp_to_tc_map"] != "[DSCP_TO_TC_MAP|test-3q]" {
		t.Errorf("dscp_to_tc_map bracket-ref: got %q", portMap.Fields["dscp_to_tc_map"])
	}
	if portMap.Fields["tc_to_queue_map"] != "[TC_TO_QUEUE_MAP|test-3q]" {
		t.Errorf("tc_to_queue_map bracket-ref: got %q", portMap.Fields["tc_to_queue_map"])
	}

	// QUEUE entries
	q0 := entries[1]
	if q0.Table != "QUEUE" || q0.Key != "Ethernet0|0" {
		t.Errorf("entry[1]: got %s|%s, want QUEUE|Ethernet0|0", q0.Table, q0.Key)
	}
	if q0.Fields["scheduler"] != "[SCHEDULER|test-3q.0]" {
		t.Errorf("queue 0 scheduler ref: got %q", q0.Fields["scheduler"])
	}
	if _, hasWred := q0.Fields["wred_profile"]; hasWred {
		t.Error("queue 0 (be) should not have wred_profile")
	}

	// Queue 2 (lossless, ECN) should have wred_profile
	q2 := entries[3]
	if q2.Key != "Ethernet0|2" {
		t.Errorf("entry[3] key: got %q, want Ethernet0|2", q2.Key)
	}
	if q2.Fields["wred_profile"] != "[WRED_PROFILE|test-3q.ecn]" {
		t.Errorf("queue 2 wred_profile ref: got %q", q2.Fields["wred_profile"])
	}
}

func TestDSCPDefaultMapping(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "be", Type: "dwrr", Weight: 80, DSCP: []int{0, 8}},
			{Name: "nc", Type: "strict", DSCP: []int{48}},
		},
	}

	entries := generateQoSDeviceEntries("dscp-test", policy)
	dscpMap := entries[0]

	// Explicitly mapped
	if dscpMap.Fields["0"] != "0" {
		t.Errorf("DSCP 0: got %q, want 0", dscpMap.Fields["0"])
	}
	if dscpMap.Fields["8"] != "0" {
		t.Errorf("DSCP 8: got %q, want 0", dscpMap.Fields["8"])
	}
	if dscpMap.Fields["48"] != "1" {
		t.Errorf("DSCP 48: got %q, want 1", dscpMap.Fields["48"])
	}

	// Unmapped DSCP values all default to "0"
	for i := 0; i < 64; i++ {
		key := fmt.Sprintf("%d", i)
		if i == 0 || i == 8 || i == 48 {
			continue // already checked
		}
		if dscpMap.Fields[key] != "0" {
			t.Errorf("unmapped DSCP %d: got %q, want 0", i, dscpMap.Fields[key])
		}
	}
}
