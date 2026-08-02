package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// TestAddBGPPeer_StatusClasses pins the split the API layer maps to status
// codes: an unparseable neighbor address is the caller's input (400), while an
// address that is already peered conflicts with device state (409). Both
// previously returned bare fmt.Errorf, which httpStatusFromError could only
// render as 500 — so a console mapping 5xx to "engine unreachable" reported an
// outage for "you already peered that address".
func TestAddBGPPeer_StatusClasses(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid neighbor IP is caller input", func(t *testing.T) {
		n := roundTripNode()
		_, err := n.AddBGPEVPNPeer(ctx, "not-an-ip", 65001, "", true)
		if err == nil {
			t.Fatal("expected an error for an unparseable neighbor IP")
		}
		var ve *util.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err = %v (%T); want *util.ValidationError (→400)", err, err)
		}
		if ve.Field != "neighbor_ip" {
			t.Errorf("Field = %q, want neighbor_ip", ve.Field)
		}
	})

	t.Run("already-peered address is a conflict", func(t *testing.T) {
		n := roundTripNode()
		for _, inv := range roundTripSequence {
			if err := inv.invoke(ctx, n); err != nil {
				t.Fatalf("prereq %q: %v", inv.op, err)
			}
			if inv.op == "add-bgp-evpn-peer" {
				break
			}
		}
		// Overlay peers are keyed evpn-peer|<ip> (BGPNeighborExists reads that key).
		peer := ""
		for resource := range n.IntentsByPrefix("evpn-peer|") {
			peer = strings.TrimPrefix(resource, "evpn-peer|")
		}
		if peer == "" {
			t.Fatal("fixture produced no evpn peer — the conflict case cannot be proven")
		}
		_, err := n.AddBGPEVPNPeer(ctx, peer, 65001, "", true)
		if err == nil {
			t.Fatalf("re-adding peer %s succeeded; expected a conflict", peer)
		}
		if !errors.Is(err, util.ErrPreconditionFailed) {
			t.Errorf("err = %v; want errors.Is(_, ErrPreconditionFailed) (→409)", err)
		}
		var ve *util.ValidationError
		if errors.As(err, &ve) {
			t.Errorf("already-peered surfaced as a validation error (→400); it is a state conflict (→409)")
		}
	})
}
