# RCA-009: SONiC-VPP ACL rules render incorrect ICMP matching

## Symptom

ICMP ping between VPP devices was blocked by ACL rules that were supposed to
permit ICMP. BGP ACL rules only matched IPv6, not IPv4. ACLs that worked on
SONiC-VS failed on SONiC-VPP.

## Root Cause

SONiC-VPP's ACL rendering has two bugs:

### Bug 1: ICMP rule renders with explicit sport/dport 0

When an ACL rule specifies `IP_PROTOCOL: 1` (ICMP) without source/destination
port fields, VPP's ACL programming renders it with `sport 0 dport 0`. In ICMP,
the type and code fields occupy the positions that TCP/UDP use for ports. So
`sport 0 dport 0` translates to "ICMP type 0, code 0" — which is **echo reply**
only. ICMP echo request (type 8) does not match, so pings are blocked in the
request direction.

### Bug 2: BGP rule renders as IPv6 only

ACL rules for BGP (TCP port 179) without an explicit IP version specification
were rendered only for IPv6 by VPP's ACL layer, missing IPv4 BGP sessions
entirely.

## Impact

- Data plane verification (ping) blocked
- BGP sessions potentially affected (though eBGP underlay worked because it
  was established before ACLs were applied)

## Current Status

**Not fixed.** ACLs need further investigation before use in VPP environments.
The workaround is to not apply ACLs on SONiC-VPP devices during testing.

## Lesson

SONiC-VPP's ACL implementation differs from SONiC-VS in non-obvious ways.
The same ACL CONFIG_DB entries produce different behavior depending on the
dataplane. When supporting multiple SONiC platforms, ACL rules must be
validated per-platform — what works on VS may silently misbehave on VPP.
Consider adding platform-specific ACL test cases to the newtrun suite.
