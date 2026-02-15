# RCA-016: Post-provision BGP routes stale until manual soft clear

## Symptom

After provisioning all 4 nodes in the 4-node topology, eBGP underlay sessions
were Established but exchanged zero or partial prefixes. Some peers showed
`PfxRcvd=0` despite the remote side claiming `PfxSnt=4`. Overlay sessions
remained in "Connect" because loopback routes were not propagated.

A manual `vtysh -c 'clear bgp * soft'` on all nodes immediately resolved
the issue — all prefixes were exchanged and overlay sessions established.

## Root Cause

When devices are provisioned in parallel, each device's post-provision sequence
(BGP restart → 15s wait → ApplyFRRDefaults → clear bgp * soft out) runs
independently. The soft clear on Device A happens before Device B's BGP is
fully initialized, so A's re-advertisement has no effect on B.

By the time Device B completes its own soft clear, Device A's stale route
state is not refreshed. The routes remain "not yet sent" until the next
BGP timer event (ConnectRetry = 120s, Update timer), causing a multi-minute
convergence delay.

## Impact

- BGP convergence delayed by up to 120 seconds after provisioning
- Overlay sessions stuck in Connect until underlay routes propagate
- Intermittent: depends on provisioning order and timing

## Fix

Two changes:

### 1. Per-device: `ApplyFRRDefaults` — `clear bgp * soft` (both directions)

`ApplyFRRDefaults()` in `pkg/device/device.go` originally ran
`clear bgp * soft out` (outbound only). This was insufficient: when device A
runs before device B, device A re-advertises to B, but B still has
`suppress-fib-pending` active and silently suppresses the routes. When B later
runs its own `ApplyFRRDefaults`, it only clears outbound — never reprocessing
the inbound routes it suppressed from A.

Changed to `clear bgp * soft` (both inbound and outbound) so each device also
reprocesses received routes after `no bgp suppress-fib-pending` takes effect.

### 2. Global: `refreshBGP` — post-provision convergence pass

Added a post-provision BGP refresh step in `Lab.Provision()` in
`pkg/newtlab/newtlab.go`. After all devices complete provisioning, the
function waits 5 seconds then SSHs to each device and runs
`vtysh -c 'clear bgp * soft'`:

```go
func (l *Lab) refreshBGP(state *LabState) {
    time.Sleep(5 * time.Second)
    for name, node := range state.Nodes {
        // SSH to device, run: vtysh -c 'clear bgp * soft'
    }
}
```

This ensures all devices re-advertise routes after all peers are ready.

## Lesson

1. `suppress-fib-pending` in FRR suppresses route advertisement until the
   route is confirmed in the FIB. When disabling it (`no bgp suppress-fib-pending`),
   routes already received while it was active remain suppressed. A `clear bgp *
   soft` (both directions) is required to reprocess them.

2. When provisioning multiple BGP speakers, always include a global convergence
   step after all individual provisions complete. Per-device soft clears during
   provisioning are insufficient because peers may not be ready yet.
