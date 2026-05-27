package node

import (
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// fakeConfigDBReader returns predetermined hash data; nothing talks to Redis.
// An absent map entry means the key does not exist on the device.
type fakeConfigDBReader struct {
	data map[string]map[string]string // keyed by "TABLE|KEY"
}

func newFakeReader(data map[string]map[string]string) *fakeConfigDBReader {
	return &fakeConfigDBReader{data: data}
}

func (f *fakeConfigDBReader) Get(table, key string) (map[string]string, error) {
	if m, ok := f.data[table+"|"+key]; ok {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	return map[string]string{}, nil
}

func (f *fakeConfigDBReader) Exists(table, key string) (bool, error) {
	_, ok := f.data[table+"|"+key]
	return ok, nil
}

// TestVerifyWithReader_Site1_KeyAbsent — Add/Modify change where the key
// does not exist on the device. DeviceResponse must be the key-absent sentinel.
func TestVerifyWithReader_Site1_KeyAbsent(t *testing.T) {
	reader := newFakeReader(nil) // no data — every key is absent

	changes := []sonic.ConfigChange{{
		Table:  "BGP_GLOBALS",
		Key:    "default",
		Type:   sonic.ChangeTypeAdd,
		Fields: map[string]string{"local_asn": "65001"},
	}}

	result, ops, err := verifyWithReader(reader, changes, 0)
	if err != nil {
		t.Fatalf("verifyWithReader: %v", err)
	}
	if result.Failed != 1 || len(result.Errors) != 1 {
		t.Fatalf("expected 1 failure, got Failed=%d Errors=%d", result.Failed, len(result.Errors))
	}
	got := result.Errors[0].DeviceResponse
	const want = "(key absent — HGETALL returned no fields)"
	if got != want {
		t.Errorf("Site 1 DeviceResponse = %q, want %q", got, want)
	}
	// One verify_read op per change with result=rejected.
	if len(ops) != 1 {
		t.Fatalf("expected 1 verify_read op, got %d", len(ops))
	}
	if ops[0].Kind != sonic.PerWriteKindVerifyRead || ops[0].Result != sonic.PerWriteResultRejected {
		t.Errorf("Site 1: op Kind=%q Result=%q, want %q/%q",
			ops[0].Kind, ops[0].Result, sonic.PerWriteKindVerifyRead, sonic.PerWriteResultRejected)
	}
}

// TestVerifyWithReader_Site2_FieldMismatch — Add/Modify change where the key
// exists but a field carries the wrong value. DeviceResponse must contain the
// full HGETALL content so the operator sees the complete key state.
func TestVerifyWithReader_Site2_FieldMismatch(t *testing.T) {
	// Device has local_asn=65002 but the change wrote local_asn=65001.
	reader := newFakeReader(map[string]map[string]string{
		"BGP_GLOBALS|default": {
			"local_asn":   "65002",
			"router_id":   "10.0.0.1",
			"hold_timer":  "180",
		},
	})

	changes := []sonic.ConfigChange{{
		Table:  "BGP_GLOBALS",
		Key:    "default",
		Type:   sonic.ChangeTypeAdd,
		Fields: map[string]string{"local_asn": "65001", "router_id": "10.0.0.1"},
	}}

	result, ops, err := verifyWithReader(reader, changes, 0)
	if err != nil {
		t.Fatalf("verifyWithReader: %v", err)
	}
	if result.Failed != 1 || len(result.Errors) != 1 {
		t.Fatalf("expected 1 failure, got Failed=%d Errors=%d", result.Failed, len(result.Errors))
	}
	e := result.Errors[0]
	if e.Field != "local_asn" {
		t.Errorf("expected failing field = local_asn, got %q", e.Field)
	}
	if e.DeviceResponse == "" {
		t.Fatal("Site 2: DeviceResponse must be non-empty for field-mismatch failure")
	}
	// DeviceResponse must carry ALL fields from the actual hash, sorted alphabetically.
	for _, sub := range []string{"hold_timer=180", "local_asn=65002", "router_id=10.0.0.1"} {
		if !strings.Contains(e.DeviceResponse, sub) {
			t.Errorf("Site 2: DeviceResponse %q missing %q", e.DeviceResponse, sub)
		}
	}
	// verify_read op carries the same full-hash device_response.
	if len(ops) != 1 || ops[0].Result != sonic.PerWriteResultRejected {
		t.Fatalf("expected 1 rejected verify_read op, got %d ops", len(ops))
	}
	if !strings.Contains(ops[0].DeviceResponse, "local_asn=65002") {
		t.Errorf("Site 2 op DeviceResponse %q missing %q", ops[0].DeviceResponse, "local_asn=65002")
	}
}

// TestVerifyWithReader_Site3_DeleteStillPresent — Delete change where the key
// is still present on the device. DeviceResponse must carry the verbatim hash
// fetched by the extra round-trip.
func TestVerifyWithReader_Site3_DeleteStillPresent(t *testing.T) {
	reader := newFakeReader(map[string]map[string]string{
		"VRF|CUSTOMER": {"vni": "10001"},
	})

	changes := []sonic.ConfigChange{{
		Table: "VRF",
		Key:   "CUSTOMER",
		Type:  sonic.ChangeTypeDelete,
	}}

	result, ops, err := verifyWithReader(reader, changes, 0)
	if err != nil {
		t.Fatalf("verifyWithReader: %v", err)
	}
	if result.Failed != 1 || len(result.Errors) != 1 {
		t.Fatalf("expected 1 failure, got Failed=%d Errors=%d", result.Failed, len(result.Errors))
	}
	e := result.Errors[0]
	if !strings.Contains(e.DeviceResponse, "vni=10001") {
		t.Errorf("Site 3: DeviceResponse %q missing %q", e.DeviceResponse, "vni=10001")
	}
	if len(ops) != 1 || ops[0].Result != sonic.PerWriteResultRejected {
		t.Fatalf("expected 1 rejected verify_read op, got %d ops", len(ops))
	}
}

// TestVerifyWithReader_AllPassed — sanity check that matching state produces
// zero failures and DeviceResponse fields stay empty.
func TestVerifyWithReader_AllPassed(t *testing.T) {
	reader := newFakeReader(map[string]map[string]string{
		"VLAN|Vlan100": {"vlanid": "100"},
	})
	changes := []sonic.ConfigChange{{
		Table:  "VLAN",
		Key:    "Vlan100",
		Type:   sonic.ChangeTypeAdd,
		Fields: map[string]string{"vlanid": "100"},
	}}

	result, ops, err := verifyWithReader(reader, changes, 0)
	if err != nil {
		t.Fatalf("verifyWithReader: %v", err)
	}
	if result.Failed != 0 || result.Passed != 1 || len(result.Errors) != 0 {
		t.Errorf("expected Passed=1 Failed=0 Errors=0, got Passed=%d Failed=%d Errors=%d",
			result.Passed, result.Failed, len(result.Errors))
	}
	// One applied verify_read op with the full hash content.
	if len(ops) != 1 || ops[0].Kind != sonic.PerWriteKindVerifyRead || ops[0].Result != sonic.PerWriteResultApplied {
		t.Fatalf("expected 1 applied verify_read op, got %d ops with first Kind=%q Result=%q",
			len(ops), ops[0].Kind, ops[0].Result)
	}
}

// TestFormatRedisHash_Deterministic confirms formatRedisHash sorts by field
// name so the output is stable across map iteration.
func TestFormatRedisHash_Deterministic(t *testing.T) {
	m := map[string]string{
		"router_id":  "10.0.0.1",
		"local_asn":  "65001",
		"hold_timer": "180",
	}
	got := formatRedisHash(m)
	want := "hold_timer=180 local_asn=65001 router_id=10.0.0.1"
	if got != want {
		t.Errorf("formatRedisHash = %q, want %q", got, want)
	}
}

// TestFormatRedisHash_Empty confirms the empty-map sentinel.
func TestFormatRedisHash_Empty(t *testing.T) {
	if got := formatRedisHash(map[string]string{}); got != "(empty hash)" {
		t.Errorf("formatRedisHash(empty) = %q, want %q", got, "(empty hash)")
	}
}
