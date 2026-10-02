package node

import (
	"fmt"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

func TestGenerateDeviceQoSConfig_TwoQueue(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "best-effort", Type: "dwrr", Weight: 70, DSCP: []int{0}},
			{Name: "voice", Type: "strict", DSCP: []int{46}},
		},
	}

	entries := GenerateDeviceQoSConfig("TEST_2Q", policy)

	// 1 DSCP_TO_TC_MAP + 1 TC_TO_QUEUE_MAP + 2 policy SCHEDULER + 1 shared default
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	// DSCP_TO_TC_MAP
	dscpMap := entries[0]
	if dscpMap.Table != "DSCP_TO_TC_MAP" || dscpMap.Key != "TEST_2Q" {
		t.Errorf("entry[0]: got %s|%s, want DSCP_TO_TC_MAP|TEST_2Q", dscpMap.Table, dscpMap.Key)
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
	if tcMap.Table != "TC_TO_QUEUE_MAP" || tcMap.Key != "TEST_2Q" {
		t.Errorf("entry[1]: got %s|%s, want TC_TO_QUEUE_MAP|TEST_2Q", tcMap.Table, tcMap.Key)
	}
	// Total over the platform's classes, not the policy's two — a class with no
	// entry has no defined queue, and the map is a function.
	if len(tcMap.Fields) != sonic.MaxQueuesPerPort {
		t.Errorf("TC map should cover all %d classes, got %d", sonic.MaxQueuesPerPort, len(tcMap.Fields))
	}
	for i := 0; i < sonic.MaxQueuesPerPort; i++ {
		k := fmt.Sprintf("%d", i)
		if tcMap.Fields[k] != k {
			t.Errorf("TC map should be identity at %s: got %q", k, tcMap.Fields[k])
		}
	}

	// SCHEDULER entries
	sched0 := entries[2]
	if sched0.Table != "SCHEDULER" || sched0.Key != "TEST_2Q_Q0" {
		t.Errorf("entry[2]: got %s|%s, want SCHEDULER|TEST_2Q_Q0", sched0.Table, sched0.Key)
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

func TestGenerateDeviceQoSConfig_EightQueueWithECN(t *testing.T) {
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

	entries := GenerateDeviceQoSConfig("8Q_DC", policy)

	// 1 DSCP + 1 TC + 8 policy SCHEDULER + 1 shared default + 1 WRED = 12
	if len(entries) != 12 {
		t.Fatalf("expected 12 entries, got %d", len(entries))
	}

	// Last entry should be WRED_PROFILE
	wred := entries[11]
	if wred.Table != "WRED_PROFILE" || wred.Key != "8Q_DC_ECN" {
		t.Errorf("last entry: got %s|%s, want WRED_PROFILE|8Q_DC_ECN", wred.Table, wred.Key)
	}
	if wred.Fields["ecn"] != "ecn_all" {
		t.Errorf("WRED ecn: got %q, want ecn_all", wred.Fields["ecn"])
	}
}

func TestGenerateDeviceQoSConfig_NoECN(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "be", Type: "dwrr", Weight: 50, DSCP: []int{0}},
			{Name: "nc", Type: "strict", DSCP: []int{48}},
		},
	}

	entries := GenerateDeviceQoSConfig("NO_ECN", policy)

	// 1 DSCP + 1 TC + 2 policy SCHEDULER + 1 shared default = 5 (no WRED)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries (no WRED), got %d", len(entries))
	}
	for _, e := range entries {
		if e.Table == "WRED_PROFILE" {
			t.Error("should not have WRED_PROFILE entry without ECN")
		}
	}
}

func TestQoSBinding(t *testing.T) {
	policy := &spec.QoSPolicy{
		Queues: []*spec.QoSQueue{
			{Name: "be", Type: "dwrr", Weight: 40, DSCP: []int{0}},
			{Name: "voice", Type: "strict", DSCP: []int{46}},
			{Name: "lossless", Type: "dwrr", Weight: 20, DSCP: []int{3}, ECN: true},
		},
	}

	entries := bindQosConfig("Ethernet0", "TEST_3Q", policy)

	// 1 PORT_QOS_MAP + one QUEUE row per *platform* queue — the policy's three
	// carry its schedulers, the rest carry the shared default, so the forward
	// writes the same range unbindQosConfig clears.
	if len(entries) != 1+sonic.MaxQueuesPerPort {
		t.Fatalf("expected %d entries, got %d", 1+sonic.MaxQueuesPerPort, len(entries))
	}

	// PORT_QOS_MAP
	portMap := entries[0]
	if portMap.Table != "PORT_QOS_MAP" || portMap.Key != "Ethernet0" {
		t.Errorf("entry[0]: got %s|%s, want PORT_QOS_MAP|Ethernet0", portMap.Table, portMap.Key)
	}
	if portMap.Fields["dscp_to_tc_map"] != "[DSCP_TO_TC_MAP|TEST_3Q]" {
		t.Errorf("dscp_to_tc_map bracket-ref: got %q", portMap.Fields["dscp_to_tc_map"])
	}
	if portMap.Fields["tc_to_queue_map"] != "[TC_TO_QUEUE_MAP|TEST_3Q]" {
		t.Errorf("tc_to_queue_map bracket-ref: got %q", portMap.Fields["tc_to_queue_map"])
	}

	// QUEUE entries
	q0 := entries[1]
	if q0.Table != "QUEUE" || q0.Key != "Ethernet0|0" {
		t.Errorf("entry[1]: got %s|%s, want QUEUE|Ethernet0|0", q0.Table, q0.Key)
	}
	if q0.Fields["scheduler"] != "[SCHEDULER|TEST_3Q_Q0]" {
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
	if q2.Fields["wred_profile"] != "[WRED_PROFILE|TEST_3Q_ECN]" {
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

	entries := GenerateDeviceQoSConfig("DSCP_TEST", policy)
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

// TestQoSBinding_TotalOverPlatformQueues pins the property increment 3 exists for:
// every queue the platform has carries a treatment, and the ones the policy does
// not describe carry the shared default. Before this, a port's queue rows were
// sized by the policy, which is what let the forward and the reverse disagree
// about how many rows a binding owns.
func TestQoSBinding_TotalOverPlatformQueues(t *testing.T) {
	policy := &spec.QoSPolicy{Queues: []*spec.QoSQueue{
		{Name: "be", Type: "dwrr", Weight: 60, DSCP: []int{0}},
		{Name: "voice", Type: "strict", DSCP: []int{46}},
	}}

	byKey := map[string]sonic.Entry{}
	for _, e := range bindQosConfig("Ethernet0", "TEST_2Q", policy) {
		if e.Table == "QUEUE" {
			byKey[e.Key] = e
		}
	}
	if len(byKey) != sonic.MaxQueuesPerPort {
		t.Fatalf("queue rows: got %d, want %d (one per platform queue)", len(byKey), sonic.MaxQueuesPerPort)
	}

	for idx := 0; idx < sonic.MaxQueuesPerPort; idx++ {
		e, ok := byKey[fmt.Sprintf("Ethernet0|%d", idx)]
		if !ok {
			t.Fatalf("queue %d has no row — the mapping is not total", idx)
		}
		want := fmt.Sprintf("[SCHEDULER|%s]", defaultSchedulerName)
		if idx < len(policy.Queues) {
			want = fmt.Sprintf("[SCHEDULER|TEST_2Q_Q%d]", idx)
		}
		if got := e.Fields["scheduler"]; got != want {
			t.Errorf("queue %d scheduler: got %q, want %q", idx, got, want)
		}
	}
}

// TestGenerateDeviceQoSConfig_DefaultScheduler pins the object those idle queues
// reference. It is emitted with every policy and identical each time — the §24
// create-on-first-reference shape — so whichever bind runs first brings it into
// being and a second bind is a no-op rather than a conflict.
func TestGenerateDeviceQoSConfig_DefaultScheduler(t *testing.T) {
	policy := &spec.QoSPolicy{Queues: []*spec.QoSQueue{{Name: "be", Type: "dwrr", Weight: 100, DSCP: []int{0}}}}

	var found *sonic.Entry
	for _, e := range GenerateDeviceQoSConfig("TEST_1Q", policy) {
		if e.Table == "SCHEDULER" && e.Key == defaultSchedulerName {
			found = &e
		}
	}
	if found == nil {
		t.Fatalf("no shared default scheduler emitted; idle queues would reference a key that does not exist")
	}
	// DWRR with a minimum share: nothing classifies into these queues, but if
	// anything ever did, a minimum-share queue degrades where strict would starve.
	if found.Fields["type"] != "DWRR" || found.Fields["weight"] != "1" {
		t.Errorf("default scheduler: got type=%q weight=%q, want DWRR/1",
			found.Fields["type"], found.Fields["weight"])
	}
}
