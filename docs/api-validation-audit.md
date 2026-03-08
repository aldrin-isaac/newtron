# API Input Validation Audit

Date: 2026-03-07

## Summary

The newtron-server API handlers have significant validation gaps. Some handlers
perform basic checks, but many accept user input with minimal or no validation
before passing it to the ops layer. Invalid values that reach CONFIG_DB can cause
SONiC daemons to crash, ignore entries silently, or produce undefined behavior.

## What IS Validated

| Field | Range/Format | Location | Notes |
|-------|-------------|----------|-------|
| VLAN ID | 1-4094 | `vlan_ops.go` precondition | Not checked in handler |
| IP CIDR | valid IPv4 CIDR | `interface_ops.go` (SetIP path) | `util.IsValidIPv4CIDR()` |
| BGP neighbor IP | valid IPv4 | `bgp_ops.go:313` | `util.IsValidIPv4()` |
| MTU | 68-9216 | `interface_ops.go` | `util.ValidateMTU()` |
| Speed | whitelist | `interface_ops.go` | 1G/10G/25G/40G/50G/100G/200G/400G |
| Admin status | "up" / "down" | `interface_ops.go` | |
| ACL direction | "ingress" / "egress" | `interface_ops.go` | |

## What is NOT Validated

### Priority 1 — Security / Stability

**ASN (BGP peer AS number)**
- Constraint: 1-4294967295 (32-bit)
- Current: no validation anywhere
- Affected: `CreateBGPNeighborConfig`, `AddLoopbackBGPNeighbor`, `handleAddBGPNeighborNode`
- Risk: ASN 0 or negative values cause FRR to reject the config or crash

**ACL rule fields**
- Priority: should be 0-65535 (no validation)
- Action: should be PERMIT/DENY/DROP (no validation)
- Protocol: should be TCP/UDP/ICMP/etc. (no validation)
- Port numbers (src/dst): should be 0-65535 (no validation)
- Source/dest IP: should be valid CIDR (no validation)
- Affected: `handleAddACLRule`, `acl_ops.go`
- Risk: invalid values written to ACL_RULE table; orchagent ignores or crashes

### Priority 2 — Data Integrity

**VRF name**
- Constraint: `^[a-zA-Z0-9_-]+$`, max ~16 chars, "default" reserved
- Current: no format validation at handler or ops level
- Affected: `handleCreateVRF`, `handleAddVRFInterface`, `handleBindIPVPN`
- Risk: names with spaces/special chars create unparseable CONFIG_DB keys

**Static route fields**
- Prefix: should be valid CIDR (no validation)
- Next-hop: should be valid IPv4 (no validation)
- Metric: should be 0-4095 (no validation, can be negative)
- Affected: `handleAddStaticRoute`
- Risk: invalid routes written to STATIC_ROUTE table

**Required field presence**
- `SetIP`: ip field can be empty string
- `SetVRF`: vrf field can be empty string
- `AddStaticRoute`: prefix, nexthop can be empty
- `AddBGPNeighbor`: remote_as can be 0, neighbor_ip can be empty
- Risk: empty/zero values pass through to CONFIG_DB

### Priority 3 — Completeness

**PortChannel**
- Min-links range (typically 1-8): no validation
- Member interface names: no format validation

**Interface names**
- Should match SONiC patterns: Ethernet, Loopback, Vlan, PortChannel
- No validation at handler level (ops layer does existence checks)

**VLAN ID at handler level**
- Validated at ops layer (1-4094) but not at API boundary
- Handler accepts any int, returns 500 instead of 400 for out-of-range

## Handler-by-Handler Gaps

### handler_interface.go

| Handler | Fields Accepted | Validation Done | Missing |
|---------|----------------|-----------------|---------|
| `handleApplyService` | service, ip_address, vlan, peer_as, params | service != "" | peer_as range, ip format |
| `handleSetIP` | ip | JSON decode | ip required, CIDR format |
| `handleRemoveIP` | ip | JSON decode | ip required, format |
| `handleSetVRF` | vrf | JSON decode | vrf required, format |
| `handleBindACL` | acl, direction | JSON decode | acl required |
| `handleInterfaceSet` | property, value | JSON decode | property whitelist |
| `handleApplyQoS` | policy | JSON decode | policy required |

### handler_node.go

| Handler | Fields Accepted | Validation Done | Missing |
|---------|----------------|-----------------|---------|
| `handleCreateVLAN` | id, description | JSON decode | id range 1-4094 |
| `handleCreateVRF` | name | JSON decode | name required, format |
| `handleCreateACL` | name, type, stage, ports, description | JSON decode | all field validation |
| `handleAddACLRule` | priority, action, protocol, src/dst ip/port | JSON decode | all field validation |
| `handleAddBGPNeighbor` | vrf, interface, remote_as, neighbor_ip, ... | JSON decode | ASN range, IP format |
| `handleAddStaticRoute` | prefix, nexthop, metric | JSON decode | CIDR, IP, metric range |
| `handleConfigureSVI` | vlan_id, ip, vrf | JSON decode | vlan range, IP format |
| `handleAddVLANMember` | interface, tagged | JSON decode | interface format |
| `handleCreatePortChannel` | name, members, min_links, mtu | JSON decode | min_links range |

## SONiC Constraint Reference

| Constraint | Min | Max | Format | Validated? |
|-----------|-----|-----|--------|------------|
| VLAN ID | 1 | 4094 | integer | ops only |
| ASN (32-bit) | 1 | 4294967295 | integer | NO |
| VRF name | — | ~16 chars | `^[a-zA-Z0-9_-]+$` | NO |
| IPv4 address | — | — | dotted quad | some paths |
| IPv4 CIDR | — | — | addr/prefix | some paths |
| ACL priority | 0 | 65535 | integer | NO |
| TCP/UDP port | 0 | 65535 | integer | NO |
| MTU | 68 | 9216 | integer | ops only |
| Static route metric | 0 | 4095 | integer | NO |
| PortChannel min-links | 1 | 8 | integer | NO |
| Speed | — | — | enum | ops only |

## Approach — Implemented

### ChangeSet-level schema validation (IMPLEMENTED)

A static CONFIG_DB schema table in `pkg/newtron/device/sonic/schema.go` defines
per-table, per-field constraints derived from SONiC YANG models and newtron ops
file usage. `ChangeSet.Validate()` checks the entire ChangeSet against this
schema before any writes, preventing both invalid data and partial applies.

- **Fail closed**: unknown tables and unknown fields cause validation errors
- **Delete safety**: deletes validate key format only, skip field validation
- **Key-only entries**: empty field maps are allowed (e.g., INTERFACE|Ethernet0)
- **Called from Apply()**: validation runs automatically before any Redis writes
- **Also callable independently**: for dry-run / preview validation

Files:
- `pkg/newtron/device/sonic/schema.go` — FieldType, FieldConstraint, TableSchema, Schema map
- `pkg/newtron/device/sonic/schema_test.go` — comprehensive tests
- `pkg/newtron/network/node/changeset.go` — Validate() method, called from Apply()

### Handler-level validation (FUTURE)

Per CLAUDE.md: "Only validate at system boundaries (user input, external APIs)."

1. Add utility validators in `pkg/util/`: `ValidateASN`, `ValidateVRFName`,
   `ValidateCIDR`, `ValidateACLPriority`, `ValidatePortNumber`
2. Validate at API handler level (system boundary) — return 400 Bad Request
   with clear error messages for invalid input
3. Keep existing ops-level precondition checks as defense-in-depth
4. Handler validation catches format/range errors early; ops-level validation
   catches business-logic errors (existence checks, conflict detection)
