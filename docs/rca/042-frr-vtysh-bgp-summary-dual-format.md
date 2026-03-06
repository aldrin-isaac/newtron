# RCA-042: FRR vtysh "show bgp summary json" Intermittent Flat Format

**Severity**: High
**Platform**: CiscoVS (SONiC 202505 / FRR 10.x); may affect other SONiC platforms
**Status**: Fixed — `checkBGPFromVtysh` now handles both AF-keyed and flat formats

## Symptom

`CheckBGPSessions` intermittently fails with "not found in FRR" for ALL BGP neighbors
on a device, even though BGP sessions are established and the data plane is working.
The failure is non-deterministic: the same check passes on the next call seconds later.

## Root Cause — Two JSON Formats from the Same Command

FRR's `show bgp summary json` returns two structurally different JSON formats
intermittently on CiscoVS. The format alternates within seconds on the same device:

**AF-keyed format** (normal, 2 top-level keys):
```json
{
  "ipv4Unicast": {"peers": {"10.0.0.2": {"state": "Established"}, ...}},
  "l2VpnEvpn":   {"peers": {"10.0.0.2": {"state": "Established"}, ...}}
}
```

**Flat format** (intermittent, 20+ top-level keys):
```json
{
  "routerId": "10.0.0.1",
  "as": 65001,
  "peers": {"10.0.0.2": {"state": "Established"}, ...},
  "totalPeers": 2,
  ...
}
```

In the flat format, `peers` is a top-level key rather than a field nested inside an
AF object. The flat format appears during FRR initialization and intermittently
afterward — same device, same command, alternating formats within seconds.

The original parser iterated top-level keys and tried to unmarshal each value as
`{"peers": {...}}`. For the AF format, each AF value contains `peers` as a nested
field → unmarshal succeeded. For the flat format, `peers` is itself a top-level key,
not nested inside any AF value → no AF unmarshal matched → zero peers found →
all neighbors reported as "not found in FRR".

## Evidence

Server logs showing alternating formats on switch1 within a 30-second window:

```
08:39:25 switch1: summary keys=2  peerStates=map[10.0.0.2:Connect 10.1.0.1:Connect]
08:39:30 switch1: summary keys=20 peerStates=map[] raw_len=1410
08:39:36 switch1: summary keys=2  peerStates=map[10.0.0.2:Connect 10.1.0.1:Connect]
08:39:41 switch1: summary keys=20 peerStates=map[] raw_len=1504
08:39:52 switch1: summary keys=2  peerStates=map[10.0.0.2:Established 10.1.0.1:Established]
```

Calls 6 seconds apart on the same device returned different formats. The `keys=20`
entries correspond to the flat format (routerId, as, peers, totalPeers, … ); the
`keys=2` entries correspond to the AF-keyed format (ipv4Unicast, l2VpnEvpn).
`peerStates=map[]` on the flat-format lines confirms the parser found zero peers
despite the `peers` key being present in the JSON.

## Fix

`checkBGPFromVtysh` in `pkg/newtron/network/node/health_ops.go` now handles both
formats in a single pass:

1. After unmarshaling the top-level object into `map[string]json.RawMessage`, check
   for a top-level `peers` key. If present, unmarshal it directly as the peer map
   (flat format path).
2. Then iterate all top-level values and try to unmarshal each as an AF object
   containing a nested `peers` field (AF-keyed format path).

Both paths merge into the same `peerStates` map. Either format, or both together,
can contribute peers. The check succeeds as long as any path finds the expected peers.

## Impact

`CheckBGPSessions` now returns correct results regardless of which format FRR
returns. The 2node-service verify-health scenario passes 6/6 consistently (previously
failed intermittently on either switch when the flat format was returned during the
health check window).

## Related

- `pkg/newtron/network/node/health_ops.go` — `checkBGPFromVtysh()`
- FRR issue: no upstream tracking issue identified; behavior appears specific to
  CiscoVS / Silicon One initialization timing
- RCA-041: intfmgrd VRF_TABLE race (separate CiscoVS timing issue)
