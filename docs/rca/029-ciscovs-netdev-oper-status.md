# RCA-029: CiscoVS STATE_DB PORT_TABLE Uses `netdev_oper_status` Not `oper_status`

**Status**: Workaround applied (YAML scenarios updated)

**Note (Feb 2026):** The `2node-incremental` suite has been replaced by `2node-primitive` (21 scenarios, all passing on CiscoVS). References to `2node-incremental` in this document refer to the predecessor suite.

**Component**: SONiC STATE_DB / CiscoVS platform
**Affected**: Any newtest scenario using `verify-state-db` on PORT_TABLE
**Discovered**: 2026-02-19

---

## Symptom

`verify-state-db` step on `PORT_TABLE|EthernetN` with field `oper_status: up` fails on CiscoVS:

```
verify-oper-up FAIL: fields not matched
  expected: oper_status=up
  actual:   {netdev_oper_status: up, speed: 100000, ...}
```

The field `oper_status` does not exist in CiscoVS PORT_TABLE entries.

---

## Root Cause

SONiC's `portmgrd` daemon writes port operational state to STATE_DB under `PORT_TABLE|EthernetN`.
The field name varies by platform:

| Platform | Field Name | Notes |
|----------|-----------|-------|
| Mellanox / Broadcom (standard) | `oper_status` | Standard SONiC field |
| CiscoVS (Silicon One NGDP) | `netdev_oper_status` | Cisco-specific portsyncd path |

CiscoVS's Silicon One SAI uses a different port state reporting path through `portsyncd`, which
writes `netdev_oper_status` instead of the standard `oper_status`. The field reflects the kernel
netdev operational state rather than the hardware SAI port state.

Observed STATE_DB entry on CiscoVS:

```
127.0.0.1:6379[6]> HGETALL PORT_TABLE|Ethernet1
 1) "netdev_oper_status"
 2) "up"
 3) "speed"
 4) "100000"
```

---

## Impact

Any newtest scenario that checks `oper_status: up/down` in STATE_DB PORT_TABLE will fail on
CiscoVS. Affected scenarios:

- `04-interface-set.yaml`: `verify-oper-down`, `verify-oper-up`
- `32-acl-lifecycle.yaml`: `verify-port-state-after-bind`

---

## Fix

Updated all STATE_DB PORT_TABLE field checks in 2node-primitive scenarios to use
`netdev_oper_status`:

```yaml
# Before
expect:
  fields:
    oper_status: up

# After
expect:
  fields:
    netdev_oper_status: up
```

---

## Long-Term Resolution

The `verify-state-db` action should support platform-aware field aliases. A mapping like:

```yaml
# In platform spec
state_db_aliases:
  PORT_TABLE:
    oper_status: netdev_oper_status
```

would allow scenarios to use `oper_status` and have it automatically translated for CiscoVS.
Alternatively, newtron's `GetPortState` accessor could normalize the field name across platforms.

Until then, CiscoVS-targeted scenarios must use `netdev_oper_status`.

---

## References

- SONiC portmgrd source: `sonic-swss/cfgmgr/portmgr.cpp`
- CiscoVS portsyncd: custom path via Silicon One NGDP event subscription
