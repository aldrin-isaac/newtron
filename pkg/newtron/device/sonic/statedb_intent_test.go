package sonic

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIntentOperation_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reversed := now.Add(10 * time.Second)
	op := IntentOperation{
		Name:      "device.create-vlan",
		Params:    map[string]string{"vlan_id": "100"},
		ReverseOp: "device.delete-vlan",
		Started:   &now,
		Completed: nil,
		Reversed:  &reversed,
	}

	data, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded IntentOperation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Name != op.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, op.Name)
	}
	if decoded.Params["vlan_id"] != "100" {
		t.Errorf("Params[vlan_id] = %q, want %q", decoded.Params["vlan_id"], "100")
	}
	if decoded.ReverseOp != "device.delete-vlan" {
		t.Errorf("ReverseOp = %q, want %q", decoded.ReverseOp, "device.delete-vlan")
	}
	if decoded.Started == nil {
		t.Fatal("Started is nil, want non-nil")
	}
	if !decoded.Started.Equal(now) {
		t.Errorf("Started = %v, want %v", *decoded.Started, now)
	}
	if decoded.Completed != nil {
		t.Errorf("Completed = %v, want nil", decoded.Completed)
	}
	if decoded.Reversed == nil {
		t.Fatal("Reversed is nil, want non-nil")
	}
	if !decoded.Reversed.Equal(reversed) {
		t.Errorf("Reversed = %v, want %v", *decoded.Reversed, reversed)
	}
}

func TestOperationIntent_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	completed := now.Add(5 * time.Second)
	reversed := now.Add(60 * time.Second)
	rbStarted := now.Add(30 * time.Second)

	intent := OperationIntent{
		Holder:          "admin@switch1",
		Created:         now,
		Phase:           IntentPhaseRollingBack,
		RollbackHolder:  "operator@switch1",
		RollbackStarted: &rbStarted,
		Operations: []IntentOperation{
			{
				Name:      "device.create-vlan",
				Params:    map[string]string{"vlan_id": "100"},
				ReverseOp: "device.delete-vlan",
				Started:   &now,
				Completed: &completed,
				Reversed:  &reversed,
			},
			{
				Name:      "device.configure-bgp",
				ReverseOp: "device.remove-bgp-globals",
				Params:    map[string]string{},
			},
		},
	}

	data, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded OperationIntent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Holder != "admin@switch1" {
		t.Errorf("Holder = %q, want %q", decoded.Holder, "admin@switch1")
	}
	if decoded.Phase != IntentPhaseRollingBack {
		t.Errorf("Phase = %q, want %q", decoded.Phase, IntentPhaseRollingBack)
	}
	if decoded.RollbackHolder != "operator@switch1" {
		t.Errorf("RollbackHolder = %q, want %q", decoded.RollbackHolder, "operator@switch1")
	}
	if decoded.RollbackStarted == nil || !decoded.RollbackStarted.Equal(rbStarted) {
		t.Errorf("RollbackStarted = %v, want %v", decoded.RollbackStarted, rbStarted)
	}
	if len(decoded.Operations) != 2 {
		t.Fatalf("Operations len = %d, want 2", len(decoded.Operations))
	}
	if decoded.Operations[0].Completed == nil {
		t.Error("Operations[0].Completed is nil, want non-nil")
	}
	if decoded.Operations[0].Reversed == nil {
		t.Error("Operations[0].Reversed is nil, want non-nil")
	}
	if decoded.Operations[0].ReverseOp != "device.delete-vlan" {
		t.Errorf("Operations[0].ReverseOp = %q, want %q", decoded.Operations[0].ReverseOp, "device.delete-vlan")
	}
	if decoded.Operations[1].Started != nil {
		t.Error("Operations[1].Started is non-nil, want nil")
	}
	if decoded.Operations[1].Reversed != nil {
		t.Error("Operations[1].Reversed is non-nil, want nil")
	}
}
