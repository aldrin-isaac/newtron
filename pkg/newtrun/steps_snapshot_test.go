package newtrun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// TestSnapshotVerify_CatchesLeak stands up a faux newtron-server that serves a
// controllable /intent/snapshot, then drives the real snapshot/verify-snapshot
// executors: capture a baseline, verify clean, inject a residual record, and
// assert verify-snapshot FAILs and names the leaked record. This is the
// end-to-end proof that "back where we started?" catches residual intent.
func TestSnapshotVerify_CatchesLeak(t *testing.T) {
	var mu sync.Mutex
	current := util.IntentRecords{
		"device":   {"operation": "setup-device", "state": "actuated"},
		"vlan|100": {"operation": "create-vlan", "vlan_id": "100", "state": "actuated"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/intent/snapshot") {
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": current})
			return
		}
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()

	r := &Runner{Client: client.New(srv.URL, "test-net")}
	ctx := context.Background()
	devices := deviceSelector{Devices: []string{"switch1"}}

	// Capture the baseline.
	snapOut := (&snapshotExecutor{}).Execute(ctx, r, &Step{
		Action: ActionSnapshot, Snapshot: "baseline", Devices: devices,
	})
	if snapOut.Result.Status != StepStatusPassed {
		t.Fatalf("snapshot capture failed: %+v", snapOut.Result)
	}

	verify := &Step{Action: ActionVerifySnapshot, Snapshot: "baseline", Devices: devices}

	// Nothing changed → verify passes.
	if out := (&verifySnapshotExecutor{}).Execute(ctx, r, verify); out.Result.Status != StepStatusPassed {
		t.Fatalf("verify against unchanged device should PASS, got %+v", out.Result)
	}

	// Leak a residual intent record (a forward op whose reverse never ran).
	mu.Lock()
	current["ipvpn|IPVPN"] = map[string]string{"operation": "bind-ipvpn", "vrf_name": "Vrf_A", "state": "actuated"}
	mu.Unlock()

	out := (&verifySnapshotExecutor{}).Execute(ctx, r, verify)
	if out.Result.Status != StepStatusFailed {
		t.Fatalf("verify against a leaked device should FAIL, got %+v", out.Result)
	}
	msg := out.Result.Details[0].Message
	if !strings.Contains(msg, "ipvpn|IPVPN") || !strings.Contains(msg, "residual") {
		t.Fatalf("failure message should name the residual record, got: %q", msg)
	}

	// Reverse the op (remove the residual) → verify passes again: back where we started.
	mu.Lock()
	delete(current, "ipvpn|IPVPN")
	mu.Unlock()
	if out := (&verifySnapshotExecutor{}).Execute(ctx, r, verify); out.Result.Status != StepStatusPassed {
		t.Fatalf("verify after the reverse op should PASS, got %+v", out.Result)
	}

	// verify-snapshot against a name never captured errors clearly (not a silent pass).
	missing := (&verifySnapshotExecutor{}).Execute(ctx, r, &Step{
		Action: ActionVerifySnapshot, Snapshot: "never-captured", Devices: devices,
	})
	if missing.Result.Details[0].Status != StepStatusError ||
		!strings.Contains(missing.Result.Details[0].Message, "no snapshot named") {
		t.Fatalf("expected a 'no snapshot named' error, got %+v", missing.Result)
	}
}
