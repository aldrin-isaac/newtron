package node

import (
	"context"
	"errors"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// TestApplyServiceMissingPeerASIsValidationError pins #464: applying a
// routed service whose spec marks peer_as as caller-supplied ("request")
// without providing it fails with a typed *util.ValidationError attributing
// the field — the API maps it to 400, not 500. The fixture replays the
// round-trip sequence up to (not including) apply-service so the device
// carries the same prerequisites the real apply path has.
func TestApplyServiceMissingPeerASIsValidationError(t *testing.T) {
	ctx := context.Background()
	n := roundTripNode()
	for _, inv := range roundTripSequence {
		if inv.op == "apply-service" {
			break
		}
		if err := inv.invoke(ctx, n); err != nil {
			t.Fatalf("prereq op %q: %v", inv.op, err)
		}
	}

	i, err := iface(n, "Ethernet16")
	if err != nil {
		t.Fatalf("iface: %v", err)
	}
	// TRANSIT has Routing.PeerAS == "request"; omit PeerAS.
	_, err = i.ApplyService(ctx, "TRANSIT", ApplyServiceOpts{IPAddress: "10.2.0.0/31"})
	if err == nil {
		t.Fatal("expected error for missing peer_as, got nil")
	}
	var ve *util.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v (%T); want *util.ValidationError in the chain", err, err)
	}
	if ve.Field != "peer_as" {
		t.Errorf("Field = %q, want peer_as", ve.Field)
	}
}
