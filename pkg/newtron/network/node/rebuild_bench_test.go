package node

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ============================================================================
// RebuildProjection cost fence (good/bad/ugly Bad-5: "measure first")
// ============================================================================
//
// RebuildProjection runs before EVERY operation (execute() → RebuildProjection
// → op), so its cost is a per-operation tax that grows with the device's
// intent count: O(intents) replay of IntentsToSteps → ReplayStep. In actuated
// mode one GetRawTable wire read precedes the replay; everything below is the
// in-memory component, which is the part that scales with N.
//
// The benchmark answers "where is the cliff"; the budget test is the fence —
// if a change to the replay machinery (or an op's replay closure) makes
// rebuild cost explode, the fence fails before an operator feels it. Should
// the fence ever become the bottleneck for a legitimate reason, the designed
// escape is the epoch skip: NEWTRON_INTENT has a single writer (§27), so a
// generation counter bumped on every intent write makes "has anything
// changed?" an O(1) read — rebuild only when it moved.
//
// Baseline (2026-07-06, dev host): 100 intents ≈ 5.3ms, 1000 ≈ 94ms,
// 4000 ≈ 828ms — SUPER-linear (~N^1.3), so a cliff exists but sits far past
// any real topology in this repository (fully-loaded ToR ≈ hundreds of
// intents ⇒ tens of ms per-op tax). Revisit the epoch skip if fleets reach
// thousands of intents per device.

// buildNodeWithVLANs returns an offline node carrying n create-vlan intents —
// the cheapest homogeneous replayable op, so the measurement isolates the
// replay machinery rather than any one op's config generation.
func buildNodeWithVLANs(tb testing.TB, n int) *Node {
	tb.Helper()
	ctx := context.Background()
	node := testDevice()
	if _, err := node.SetupDevice(ctx, SetupDeviceOpts{
		Fields: map[string]string{"hostname": "bench", "bgp_asn": "65001"},
	}); err != nil {
		tb.Fatalf("setup: %v", err)
	}
	for i := 0; i < n; i++ {
		vlanID := 2 + i // 2..4093 stays inside the VLAN range
		if _, err := node.CreateVLAN(ctx, vlanID, VLANConfig{Description: "bench"}); err != nil {
			tb.Fatalf("create-vlan %d: %v", vlanID, err)
		}
	}
	return node
}

func benchmarkRebuild(b *testing.B, n int) {
	node := buildNodeWithVLANs(b, n)
	intents := node.configDB.NewtronIntent
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := node.RebuildProjectionFromIntents(ctx, intents); err != nil {
			b.Fatalf("rebuild: %v", err)
		}
	}
}

func BenchmarkRebuildProjection_100(b *testing.B)  { benchmarkRebuild(b, 100) }
func BenchmarkRebuildProjection_1000(b *testing.B) { benchmarkRebuild(b, 1000) }
func BenchmarkRebuildProjection_4000(b *testing.B) { benchmarkRebuild(b, 4000) }

// TestRebuildProjectionBudget is the fence: a 1000-intent device — well past
// any topology in this repository (a fully-loaded ToR is hundreds) — must
// rebuild in under 2 seconds. The budget is deliberately ~20× the measured
// cost so machine variance never trips it; it exists to catch complexity
// regressions (an O(N²) slip in replay or the DAG sort), not to enforce a
// latency SLO.
func TestRebuildProjectionBudget(t *testing.T) {
	node := buildNodeWithVLANs(t, 1000)
	intents := node.configDB.NewtronIntent
	ctx := context.Background()

	start := time.Now()
	if err := node.RebuildProjectionFromIntents(ctx, intents); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	elapsed := time.Since(start)

	const budget = 2 * time.Second
	if elapsed > budget {
		t.Errorf("rebuild of 1000 intents took %v — over the %v fence; the replay path has a complexity regression (see rebuild_bench_test.go for the epoch-skip escape)", elapsed, budget)
	}
	t.Log(fmt.Sprintf("rebuild of 1000 intents: %v (fence %v)", elapsed, budget))
}
