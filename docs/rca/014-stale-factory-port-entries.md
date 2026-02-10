# RCA-014: Stale factory PORT entries survive boot patches

## Symptom

After boot patches wrote correct PORT entries (e.g., `PORT|Ethernet0`,
`PORT|Ethernet1`), the device still had leftover factory entries
(`PORT|Ethernet4`, `PORT|Ethernet8`, etc.) in CONFIG_DB. These stale entries
caused orchagent to attempt programming VPP interfaces that didn't exist,
resulting in interface mapping confusion.

## Root Cause

The boot patch `port_entries.tmpl` only created new PORT entries via HSET but
did not delete existing entries first. The SONiC VPP factory image ships with
default PORT entries using stride-4 naming (Ethernet0, Ethernet4, Ethernet8,
..., Ethernet28). When the boot patch wrote Ethernet0 and Ethernet1, the
factory's Ethernet4–Ethernet28 entries remained.

## Impact

- VPP had stale PORT definitions for interfaces with no backing DPDK device
- orchagent logged errors trying to program non-existent interfaces
- Related to RCA-003 (stub PORT entries crash orchagent) — stale factory
  entries are functionally equivalent to stub entries

## Fix

Added a Lua EVAL command at the top of `port_entries.tmpl` to atomically
delete all existing PORT entries before creating the correct ones:

```
EVAL "for _,k in ipairs(redis.call('KEYS','PORT|*')) do redis.call('DEL',k) end" 0
{{- range $i, $_ := .PCIAddrs}}
HSET "PORT|Ethernet{{mul $i $.PortStride}}" admin_status up ...
{{- end}}
```

This ensures only the ports matching the actual QEMU NIC configuration exist.

## Lesson

Boot patches that write CONFIG_DB entries must always clean up stale state
first. Never assume the database starts empty — factory images and previous
boot cycles leave entries behind. Use atomic cleanup (Lua EVAL with KEYS+DEL)
before writing new entries to ensure a consistent state.
