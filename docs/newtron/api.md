# newtron HTTP API Reference

The newtron HTTP server (`newtron-server`) is the canonical access point for all
network automation operations. The CLI (`newtron`) and test framework (`newtrun`)
are both HTTP clients of this server. This document is the complete API reference:
every endpoint, every request/response type, every status code.

**Audience:** Developers writing clients that consume the newtron API — whether
building tooling, integrating with CI/CD, or extending the CLI.

**Relationship to other docs:**
- [HLD](hld.md) — architecture, actor model, verification primitives (the *why*)
- [LLD](lld.md) — type definitions, package structure, code mechanics (the *how*)
- [HOWTO](howto.md) — operational procedures using the CLI (the *when*)
- This document — HTTP routes, request/response formats, behavioral contracts (the *what*)

---

## Table of Contents

1. [Conventions](#1-conventions)
2. [Typical Workflow](#2-typical-workflow)
3. [Server Management](#3-server-management)
4. [Network Spec Reads](#4-network-spec-reads)
5. [Network Spec Writes](#5-network-spec-writes)
6. [Provisioning](#6-provisioning)
7. [Node Read Operations](#7-node-read-operations)
8. [Node Write Operations](#8-node-write-operations)
9. [Node Lifecycle Operations](#9-node-lifecycle-operations)
10. [Node Diagnostics](#10-node-diagnostics)
11. [Intent Operations](#11-intent-operations)
12. [Interface Operations](#12-interface-operations)
13. [Types Reference](#13-types-reference)
14. [Error Reference](#14-error-reference)
15. [Server Configuration](#15-server-configuration)

### Endpoint Quick Reference

All paths are relative to `http://<host>:<port>/newtron/v1/`. Path-suffix tables below omit the version prefix; full URLs include it. `{n}` = `{netID}`, `{d}` = `{device}`, `{i}` = `{name}` (interface).

**Server & Specs** (S3-S5)

| Method | Path | What it does |
|--------|------|--------------|
| POST | `/networks` | Register a network |
| GET | `/networks` | List networks |
| GET | `/schema` | List every spec authoring kind with label/description |
| GET | `/schema/{kind}` | Field metadata for one kind (label, tooltip, type, required, enum, ref) |
| POST | `/networks/{n}/unregister` | Unregister a network |
| POST | `/networks/{n}/reload` | Reload specs from disk |
| GET | `/networks/{n}/services` | List services (also: `/ipvpns`, `/macvpns`, `/qos-policies`, `/filters`, `/platforms`, `/route-policies`, `/prefix-lists`) |
| GET | `/networks/{n}/services/{name}` | Show service (also: ipvpns, macvpns, qos-policies, filters, platforms, route-policies, prefix-lists) |
| GET | `/networks/{n}/services/{name}/projection` | Per-Node projection slices the service contributes (replay-diff) |
| GET | `/networks/{n}/spec-instances` | Flat cross-scope inventory of every spec (network/zone/node), tagged with scope + scope_instance |
| GET | `/networks/{n}/nodes` | List node spec names |
| GET | `/networks/{n}/nodes/{name}` | Show node spec |
| GET | `/networks/{n}/zones` | List zone names |
| GET | `/networks/{n}/zones/{name}` | Show zone |
| GET | `/networks/{n}/topology` | Full topology spec (devices, links, metadata) |
| GET | `/networks/{n}/topology/nodes` | List topology device names |
| GET | `/networks/{n}/authorization` | Read user_groups + permissions + super_users from network.json |
| GET | `/networks/{n}/hosts/{name}` | Get host profile |
| GET | `/networks/{n}/features` | List features (also: `/{name}/dependencies`, `/{name}/unsupported-due-to`) |
| GET | `/networks/{n}/platforms/{name}/supports/{feature}` | Check platform feature support |
| POST | `/networks/{n}/create-service` | Create service (also: create-ipvpn, create-macvpn, etc.) |
| POST | `/networks/{n}/delete-service` | Delete service (also: delete-ipvpn, delete-macvpn, etc.) |
| POST | `/networks/{n}/update-service` | Replace service in place — full-replacement (also: update-ipvpn, update-macvpn, update-qos-policy, update-filter, update-prefix-list, update-route-policy, update-node, update-zone) |
| POST | `/networks/{n}/create-node` | Create node spec |
| POST | `/networks/{n}/delete-node` | Delete node spec |
| POST | `/networks/{n}/create-zone` | Create zone |
| POST | `/networks/{n}/delete-zone` | Delete zone |
| POST | `/networks/{n}/create-platform` | Create platform definition |
| POST | `/networks/{n}/update-platform` | Replace platform in place — full-replacement |
| POST | `/networks/{n}/delete-platform` | Delete platform (409 if any node references it) |
| POST | `/networks/{n}/add-qos-queue` | Add queue to QoS policy |
| POST | `/networks/{n}/update-qos-queue` | Update queue in QoS policy (incl. slot rotation) |
| POST | `/networks/{n}/remove-qos-queue` | Remove queue from QoS policy |
| POST | `/networks/{n}/add-filter-rule` | Add rule to filter |
| POST | `/networks/{n}/update-filter-rule` | Update rule in filter (incl. renumber) |
| POST | `/networks/{n}/remove-filter-rule` | Remove rule from filter |
| POST | `/networks/{n}/add-prefix-list-entry` | Add entry to prefix list |
| POST | `/networks/{n}/remove-prefix-list-entry` | Remove entry from prefix list |
| POST | `/networks/{n}/add-route-policy-rule` | Add rule to route policy |
| POST | `/networks/{n}/update-route-policy-rule` | Update rule in route policy (incl. renumber) |
| POST | `/networks/{n}/remove-route-policy-rule` | Remove rule from route policy |

**Provisioning** (S6)

| Method | Path | What it does |
|--------|------|--------------|
| POST | `/networks/{n}/nodes/{d}/init-device` | Initialize device (clean factory config) |

Spec-to-device delivery is via `POST /newtron/v1/networks/{n}/nodes/{d}/intent/reconcile?mode=topology` (S11).

**Device Reads** (S7) -- all `GET /newtron/v1/networks/{n}/nodes/{d}/...`

| Path suffix | Returns |
|-------------|---------|
| `/info` | Device overview |
| `/interfaces` | Interface list |
| `/interfaces/{i}` | Interface detail |
| `/interfaces/{i}/binding` | Service binding |
| `/vlans` | VLAN list |
| `/vlans/{id}` | VLAN detail |
| `/vrfs` | VRF list |
| `/vrfs/{name}` | VRF detail |
| `/acls` | ACL list |
| `/acls/{name}` | ACL detail |
| `/bgp/status` | BGP status + neighbors |
| `/bgp/check` | BGP session check |
| `/evpn/status` | EVPN overlay status |
| `/health` | Health report |
| `/lags`, `/lags/{name}` | LAG list / detail |
| `/neighbors` | BGP sessions (alias for `/bgp/check`) |
| `/routes/{vrf}/{prefix...}` | APP_DB route lookup |
| `/routes-asic/{prefix...}` | ASIC_DB route lookup |
| `/intent/projection` | Per-Node projection (RawConfigDB) from intent replay |
| `POST /intent/projection-diff` | Pre-commit diff for a hypothetical operation set (before/after/diff) |
| `/intent/tree` | Intent DAG tree view |

**Device Writes** (S8) -- `POST` under `/newtron/v1/networks/{n}/nodes/{d}/...`

| Path suffix | What it does |
|-------------|--------------|
| `/setup-device` | Unified baseline setup (metadata + loopback + BGP + VTEP + RR) |
| `/create-vlan`, `/delete-vlan` | Create/delete VLAN |
| `/configure-irb`, `/unconfigure-irb` | Configure/unconfigure IRB (SVI) |
| `/create-vrf`, `/delete-vrf` | Create/delete VRF |
| `/bind-ipvpn`, `/unbind-ipvpn` | Bind/unbind IP-VPN to VRF |
| `/bind-macvpn`, `/unbind-macvpn` | Bind/unbind MAC-VPN (node-level, VLAN to L2VNI) |
| `/add-static-route`, `/remove-static-route` | Add/remove static route |
| `/create-acl`, `/delete-acl` | Create/delete ACL table |
| `/add-acl-rule`, `/remove-acl-rule` | Add/remove ACL rule |
| `/create-portchannel`, `/delete-portchannel` | Create/delete PortChannel |
| `/add-portchannel-member`, `/remove-portchannel-member` | Add/remove PortChannel member |
| `/add-bgp-evpn-peer`, `/remove-bgp-evpn-peer` | Add/remove EVPN overlay peer |

**Intent Operations** (S11)

| Method | Path suffix | What it does |
|--------|-------------|--------------|
| GET | `/intent/projection` | Per-Node projection (RawConfigDB) from intent replay |
| `POST /intent/projection-diff` | Pre-commit diff for a hypothetical operation set (before/after/diff) |
| GET | `/intent/tree` | Intent DAG tree view |
| GET | `/intent/drift` | Drift between projection (expected) and CONFIG_DB (actual) |
| GET | `/intent/topology-drift` | Drift between fresh topology.json projection and CONFIG_DB ([details](#topology-drift)) |
| GET | `/status` | Cheap per-device badge: online + intent drift + has_unsaved_intents ([details](#device-status)) |
| POST | `/intent/reconcile` | Deliver projection to device, eliminating drift |
| POST | `/intent/save` | Persist intent DB back to topology.json |
| POST | `/intent/reload` | Rebuild intent DB from topology.json |
| POST | `/intent/clear` | Reset node to ports-only state |

**Lifecycle & Diagnostics** (S9-S10) -- all `POST` unless noted

| Path suffix | What it does |
|-------------|--------------|
| `/reload-config` | Reload CONFIG_DB from disk |
| `/save-config` | Save CONFIG_DB to disk |
| `/restart-daemon` | Restart a SONiC daemon |
| `/ssh-command` | Execute SSH command |
| `GET /configdb` | Full CONFIG_DB snapshot (RawConfigDB); `?owned_only=false` for all tables |
| `GET /configdb/{table}` | List CONFIG_DB keys |
| `GET /configdb/{table}/{key}` | Read CONFIG_DB entry |
| `GET /configdb/{table}/{key}/exists` | Check CONFIG_DB entry exists |
| `GET /statedb/{table}/{key}` | Read STATE_DB entry |

**Interface Operations** (S12) -- all `POST /newtron/v1/networks/{n}/nodes/{d}/interfaces/{i}/...`

| Path suffix | What it does |
|-------------|--------------|
| `/apply-service`, `/remove-service`, `/refresh-service` | Service lifecycle |
| `/configure-interface`, `/unconfigure-interface` | Configure/unconfigure interface (trunk-tagged: additive per-VLAN intent, #224) |
| `/remove-trunk-vlan` | Atomic single-VLAN strip from a trunk port (#224) |
| `/bind-acl`, `/unbind-acl` | ACL binding |
| `/add-bgp-peer`, `/remove-bgp-peer` | BGP peer |
| `/bind-qos`, `/unbind-qos` | QoS policy |
| `/set-property`, `/clear-property` | Set/clear port property |

---

## 1. Conventions

Every HTTP interaction with newtron-server follows these conventions.

### Response Envelope

All responses use the `APIResponse` envelope:

```json
{"data": <payload>, "error": ""}
```

On success, `data` contains the result and `error` is omitted. On failure, `error`
contains the message and `data` is omitted:

```json
{"error": "network 'prod' not registered"}
```

### Content Type

All requests and responses use `Content-Type: application/json`. Request bodies
must be valid JSON. Endpoints that take no body accept an empty body or no body.

### HTTP Status Codes

| Code | Meaning | When |
|------|---------|------|
| 200 | Success | Reads, updates, deletes |
| 201 | Created | Resource creation (VLAN, VRF, ACL, service spec, etc.) |
| 400 | Bad Request | Invalid JSON, missing required fields, invalid parameter values |
| 404 | Not Found | Network not registered, device/resource not found |
| 409 | Conflict | Network already registered, post-Apply verification failed, conflicting reference on delete (cascade-refusal) |
| 500 | Internal Error | Unexpected server errors, SSH/Redis failures |
| 504 | Gateway Timeout | Request context deadline exceeded (device unreachable) |

The mapping from Go error types to HTTP status codes:

| Error Type | HTTP Status |
|-----------|-------------|
| `notRegisteredError` | 404 |
| `alreadyRegisteredError` | 409 |
| `NotFoundError` | 404 |
| `ValidationError` | 400 |
| `VerificationFailedError` | 409 |
| `context.DeadlineExceeded` | 504 |
| All other errors | 500 |

### Common Query Parameters

Two query parameters control write behavior on endpoints that modify device
CONFIG_DB (node write operations and interface operations that use the
Lock -> fn -> Commit -> Save cycle):

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `dry_run` | string | `"false"` | When `"true"`, builds the ChangeSet but does not commit to Redis. The response `preview` field shows what would change. |
| `no_save` | string | `"false"` | When `"true"`, commits to Redis but skips `config save` (changes persist in running config only, lost on reboot). |
| `persist` | string | `""` | When `"topology"`, the successful write is also persisted to `topology.json` via `SaveDeviceIntents` before the response returns. Atomic write+persist (issue #75C). No-op when the handler didn't mutate the intent tree (read-only paths, `/intent/save` after it clears the unsaved flag). See "Atomic write+persist" below. |

These parameters apply to endpoints documented with "**Query parameters:** `dry_run`, `no_save`" below. Read-only endpoints and lifecycle operations (reload-config, save-config, restart-daemon, ssh-command) ignore them.

#### Atomic write+persist (`?persist=topology`)

The operator's mental model for "Apply" is **(1) persist the change in the
topology AND (2) apply it to the device when online**. Without
`?persist=topology` that's two round trips: the per-action POST followed
by `POST /intent/save`. With `?persist=topology`, the server runs the same
`SaveDeviceIntents` code path inline at the end of any successful
mutation, while still holding the per-device actor lock — no window of
"device updated but topology.json doesn't know yet."

Applies to handlers that mutate the intent tree through the unified
`execute()` entry point (every `/nodes/{device}/...` and
`/nodes/{device}/interfaces/{name}/...` mutating write). The hook is
data-driven: it fires when the request both opts in via
`?persist=topology` AND the handler dirtied the intent tree
(`Node.HasUnsavedIntents()`). That gate makes three categories no-ops:

- **Read-only handlers** (`/intent/drift`, `/intent/projection`, `/info`, …) never set the dirty flag.
- **`/intent/save`** already persists and clears the flag inside its own closure, so the hook sees a clean tree.
- **Mutating handlers without the query** — the flag may be dirty but the request didn't opt in.

Network spec write endpoints (S5) also accept `dry_run` -- when `"true"`, the spec
is validated but not persisted to disk.

### URL Path Style

Two rules describe every path in this reference:

- **Collection nouns are plural.** `/networks`, `/networks/{n}/services`, `/networks/{n}/nodes/{d}/interfaces`, `/networks/{n}/nodes/{d}/routes/{vrf}/{prefix...}`. Both list (`GET /noun`) and single-resource (`GET /noun/{id}`) paths share the plural form, matching the JSON spec keys (`services: {...}`, `zones: {...}`) and Go field names (`Services`, `Zones`).
- **Action verbs and singletons stay singular.** Action paths are verb-noun forms — `create-service`, `delete-vlan`, `apply-service`, `bind-acl`, `setup-device`, `restart-daemon`. Status/view paths name a singleton — `/health`, `/info`, `/status`, `/bgp/status`, `/intent/projection`, `/intent/tree`, `/intent/drift`, `/intent/reconcile`. The one spec-view singleton is `/networks/{n}/topology` (a network has one topology). Database names — `/configdb`, `/statedb` — stay singular (each is one DB).

This split is what distinguishes a noun a consumer can list ("there are zero or more *services* on this network") from a verb the server performs ("apply *this* service to *this* interface"). When in doubt, the route table in `pkg/newtron/api/handler.go` is authoritative.

### Path Parameters

**Interface names** containing slashes (e.g., `Ethernet0/1`) must be URL-encoded:
`Ethernet0%2F1`. The server decodes `%2F` back to `/`.

**Route prefixes** use Go 1.22's `{prefix...}` catch-all pattern, which captures
the remainder of the path including slashes. A prefix like `10.0.0.0/24` is passed
as a literal path segment: `/routes/default/10.0.0.0/24`.

**VLAN IDs** and **queue IDs** in path parameters are parsed as integers. Invalid
integers return 400.

### Authentication

The server has no authentication middleware. It is designed for trusted-network
deployment -- the management network where SONiC devices and automation tools run.
Access control is enforced at the network level (firewall rules, SSH tunnels), not
at the application level.

### Request Timeout

A 5-minute timeout middleware wraps all requests. Operations that exceed this
duration return 504 Gateway Timeout.

### Connection Caching

The server caches SSH connections to devices between requests. After a configurable
idle timeout (default 5 minutes), unused connections are automatically closed. Each
request still refreshes CONFIG_DB from Redis before operating -- only the SSH tunnel
is reused. See [S15 Server Configuration](#15-server-configuration) for tuning.

### Example Request

A complete curl example showing the request/response cycle:

```bash
curl -s -X POST http://localhost:18080/newtron/v1/networks/default/node/switch1/create-vlan \
  -H "Content-Type: application/json" \
  -d '{"id": 100, "description": "Customer VLAN"}' | jq .
```

```json
{
  "data": {
    "change_count": 1,
    "applied": true,
    "verified": true,
    "saved": true,
    "verification": {"passed": 1, "failed": 0}
  }
}
```

On error:

```bash
curl -s -X POST http://localhost:18080/newtron/v1/networks/default/node/switch1/create-vlan \
  -H "Content-Type: application/json" \
  -d '{}' | jq .
```

```json
{
  "error": "validation error: id: VLAN ID required"
}
```

---

## 2. Typical Workflow

This section shows the sequence of HTTP calls for the most common use case:
bringing up a network from scratch and applying services. Each step references
the detailed endpoint documentation in later sections.

### 1. Start the server and register a network

```bash
# Start the server with a network directory
newtron-server -spec-dir /etc/newtron -net-id default

# Or register dynamically via the API
curl -X POST http://localhost:18080/network \
  -H "Content-Type: application/json" \
  -d '{"id": "default", "dir": "/etc/newtron"}'
```

See [S3 Server Management](#3-server-management).

### 2. Provision devices from the topology

```bash
# Per-device: clean factory CONFIG_DB, then load topology spec and deliver
curl -X POST http://localhost:18080/newtron/v1/networks/default/node/switch1/init-device
curl -X POST 'http://localhost:18080/newtron/v1/networks/default/nodes/switch1/intent/reconcile?mode=topology'
```

This is the canonical "spec → device" path: init-device clears factory entries,
intent/reconcile in topology mode loads the spec into the projection and writes
it to the device. The intent/reconcile pipeline IS the provisioning pipeline.
See [S6 Provisioning](#6-provisioning) and [S11](#11-intent-operations).

### 3. Verify health after provisioning

```bash
# Check that BGP sessions came up
curl http://localhost:18080/newtron/v1/networks/default/node/switch1/bgp/check

# Run full health check
curl http://localhost:18080/newtron/v1/networks/default/node/switch1/health
```

See [S7 Node Read Operations](#7-node-read-operations).

### 4. Apply services to interfaces

```bash
# Apply a service to an interface
curl -X POST http://localhost:18080/newtron/v1/networks/default/nodes/switch1/interfaces/Ethernet0/apply-service \
  -H "Content-Type: application/json" \
  -d '{"service": "customer-l3", "ip_address": "10.1.1.1/30"}'
```

Services are the primary operational unit. `apply-service` creates all required
CONFIG_DB infrastructure (VLANs, VRFs, VNI mappings, ACLs, QoS) automatically.
See [S12 Interface Operations](#12-interface-operations).

### 5. Verify the applied configuration

```bash
# Post-facto: confirm projection (intent replay) matches device CONFIG_DB.
# Empty drift array ≡ every newtron write is actualized on the device.
curl http://localhost:18080/newtron/v1/networks/default/nodes/switch1/intent/drift

# Check a specific route in the forwarding table
curl http://localhost:18080/newtron/v1/networks/default/node/switch1/route/default/10.1.1.0/30
```

Per-write verification (did THIS specific write land?) is reported inline on
the originating `WriteResult.Verification` field, or surfaced as the 409 Data
envelope on a `VerificationFailedError`. See [S11](#11-intent-operations)
and [S7 Node Read Operations](#7-node-read-operations).

### 6. Day-2 operations

```bash
# Preview a change without applying (dry-run)
curl -X POST 'http://localhost:18080/newtron/v1/networks/default/node/switch1/create-vlan?dry_run=true' \
  -H "Content-Type: application/json" \
  -d '{"id": 200, "description": "New VLAN"}'

# Refresh a service after spec changes
curl -X POST http://localhost:18080/newtron/v1/networks/default/nodes/switch1/interfaces/Ethernet0/refresh-service

# Remove a service
curl -X POST http://localhost:18080/newtron/v1/networks/default/nodes/switch1/interfaces/Ethernet0/remove-service
```

### Batching multiple operations

For atomic delivery of multiple operations — all succeed or none take effect —
use `intent/projection-diff` for pre-commit preview and `intent/reconcile` for
delivery. Each individual write endpoint already uses one Lock → operations →
Commit → Save → Unlock cycle internally; the intent pipeline composes those
cycles when reconciling a whole projection.

For ad-hoc individual changes (add a VLAN, check status, refresh one service),
use the dedicated write endpoints — they're simpler and the response is the
same `WriteResult`.

---

## 3. Server Management

These endpoints register and unregister networks. A network must be registered
before any spec reads, device operations, or provisioning can occur. Registration
loads the network directory (network.json, nodes, service definitions) into
memory.

### Schema metadata endpoints

Two read-only endpoints expose human-facing presentation metadata (label, tooltip,
type hint, required-ness, enum values, refs to other kinds) for every spec authoring
type. UIs consume these to render forms whose vocabulary stays consistent across
newtcon, the CLI's HTML preview, and any future authoring surface.

The metadata is derived at boot from struct tags on the spec types themselves —
the field definition is the single source of truth, so labels cannot drift from
the schema they describe.

These endpoints sit at the root of `/newtron/v1/` (not under `/networks/{netID}/`)
because the metadata is global to the newtron install, not per-network.

#### GET /newtron/v1/schema

List every registered spec authoring kind, with its label and description so a UI
can render a "pick the type to author" picker without fetching each kind
individually.

**Response (200):**

```json
{
  "data": {
    "kinds": [
      {
        "kind": "ServiceSpec",
        "label": "Service",
        "description": "A reusable template that binds VPN references, routing, filters, and QoS — applied to interfaces."
      },
      {
        "kind": "QoSPolicy",
        "label": "QoS Policy",
        "description": "A declarative queue policy — strict / DWRR scheduling, DSCP mapping, optional ECN."
      }
    ]
  }
}
```

The `kinds` array is alphabetically ordered by `kind` — UIs sort against the
returned slice rather than re-sorting under their own rules.

#### GET /newtron/v1/schema/{kind}

Return the metadata document for one kind. The `kind` path component is the Go
type name (e.g. `ServiceSpec`, not `Service`).

**Response (200):**

```json
{
  "data": {
    "kind": "ServiceSpec",
    "label": "Service",
    "description": "A reusable template that binds VPN references, routing, filters, and QoS — applied to interfaces.",
    "identifier": "name",
    "paths": {
      "list":   "/newtron/v1/networks/{netID}/services",
      "show":   "/newtron/v1/networks/{netID}/services/{name}",
      "create": "/newtron/v1/networks/{netID}/create-service",
      "update": "/newtron/v1/networks/{netID}/update-service",
      "delete": "/newtron/v1/networks/{netID}/delete-service"
    },
    "fields": [
      {
        "name": "name",
        "label": "Name",
        "description": "Unique identifier within this kind. Letters, digits, underscore, and hyphen only. Immutable after creation.",
        "type": "string",
        "required": true,
        "pattern": "^[A-Za-z0-9_-]+$",
        "immutable": true
      },
      {
        "name": "service_type",
        "label": "Service Type",
        "type": "enum",
        "required": true,
        "enum": ["evpn-irb", "evpn-bridged", "evpn-routed", "irb", "bridged", "routed"]
      },
      {
        "name": "ipvpn",
        "label": "IP-VPN",
        "type": "ref",
        "required": false,
        "ref_kind": "IPVPNSpec"
      }
    ]
  }
}
```

**SchemaMeta shape**:

| Field | Type | Description |
|-------|------|-------------|
| `kind` | string | Canonical kind name (Go type name) |
| `label` | string | Human label for the kind |
| `description` | string | Tooltip for the kind |
| `fields` | FieldMeta[] | Per-field metadata (see next table) |
| `identifier` | string | Field name that addresses one row — `name` for top-level kinds, `seq` / `queue_id` / `prefix` for sub-rules |
| `parent_ref` | string | Sub-rules only: wire field name carrying the parent's name in the request body (e.g. `filter` for FilterRule) |
| `paths` | SchemaPaths | HTTP path templates for the kind's CRUD verbs (see SchemaPaths) |

**SchemaPaths shape**:

| Field | Description |
|-------|-------------|
| `list` | GET — enumerate names for this kind |
| `show` | GET — fetch one named instance |
| `create` | POST — create |
| `update` | POST — replace fields in place |
| `delete` | POST — remove |

Every path is a template with `{netID}` and (for `show`) `{name}` placeholders the
UI substitutes at request time. Empty paths mean the verb doesn't exist for this
kind:

- **Read-only kinds** (PlatformSpec): `list` + `show` populated; `create` /
  `update` / `delete` absent.
- **Sub-rule kinds** (FilterRule, QoSQueue, RoutePolicyRule, PrefixListEntry):
  no `list` / `show` (sub-rules aren't top-level addressable); `create` / `update`
  / `delete` carry the `add-X` / `update-X` / `remove-X` verbs.
- **PrefixListEntry**: no `update` (per §47 the prefix IS the entry — no other
  mutable fields).
- **Embedded-only kinds** (RoutingSpec, RoutePolicySet, EVPNConfig): the `paths`
  object is omitted entirely.

**FieldMeta shape**:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Wire name (matches the `json:` tag on the spec type) |
| `label` | string | Human-readable form-field label |
| `description` | string | Tooltip / extended help text (omitted if not set) |
| `type` | string | `string` \| `int` \| `float` \| `bool` \| `enum` \| `array` \| `map` \| `object` \| `ref` |
| `required` | bool | False if the JSON tag has `,omitempty` or the Go type is a pointer |
| `enum` | string[] | For `type: enum` — the allowed values in canonical order |
| `ref_kind` | string | For `type: ref` — the kind this field references (UI renders a dropdown) |
| `item_type` | string | For `type: array` or `map` of primitives — the element type |
| `item_kind` | string | For `type: array` or `map` of objects — the element kind name |
| `pattern` | string | Regex the value must match (UI client-side validation) |
| `min` | int | Inclusive lower bound for `type: int` |
| `max` | int | Inclusive upper bound for `type: int` |
| `format` | string | Semantic hint — `cidr`, `ipv4`, `ipv6`, `mac`, `asn` (UI picks a format-specific input widget) |
| `immutable` | bool | Value is fixed at create time — UI suppresses the edit affordance in update-mode forms |
| `required_when` | object | Conditional-required predicate — see "Conditional required" below |

**Synthetic identifier fields**: top-level kinds (`ServiceSpec`, `IPVPNSpec`, …)
get a synthetic `name` field prepended to `fields` because the name lives in the
create-X request body, not on the spec struct. `QoSQueue` gets a synthetic
`queue_id` field for the same reason (the slot index is implicit in the
`QoSPolicy.Queues` array position).

**Conditional required (`required_when`)**: a structured predicate the UI
evaluates against the form's sibling field values. When the predicate is true,
the field is required even though the static `required` is `false`. Use it for
the common pattern where one enum value drives whether another field is
required — `ServiceSpec.ipvpn` is required when `service_type` is `evpn-irb` or
`evpn-routed`, and similarly for `macvpn`.

```json
{
  "name": "ipvpn",
  "type": "ref",
  "required": false,
  "required_when": {"field": "service_type", "in": ["evpn-irb", "evpn-routed"]},
  "ref_kind": "IPVPNSpec"
}
```

The shape is structured, not a DSL string — UIs walk the JSON tree directly:

| Shape | Fields | Meaning |
|-------|--------|---------|
| **Atomic** | `field` + exactly one of `equals` / `not_equals` / `in` / `not_in` | Compare the named sibling's current value against the operand |
| **Combinator** | exactly one of `all_of` / `any_of` (array of nested conditions) | Conjunction / disjunction of sub-conditions |

Atomic and combinator shapes are mutually exclusive per node — a single
`required_when` object never carries both kinds of fields.

Semantics:

- **Scope is sibling fields on the same SchemaMeta.** Nested forms (RoutingSpec
  inside ServiceSpec) evaluate against their own sibling set, not the parent's.
- **`required: true` wins.** When the static `required` is true, `required_when`
  is meaningless — the evaluator only consults it when `required` is false, so
  they never contradict.
- **Unfilled sibling values evaluate against the field's zero value.** A
  `service_type in [...]` predicate is `false` for an unfilled `service_type` —
  required-ness can't trigger on an unspecified state.
- **Server-side enforcement.** The server does NOT evaluate `required_when` at
  request time; the existing 400-on-missing-required behaviour is the back-stop.
  `required_when` is UX so the operator sees the constraint before submitting.
- **Init-time validation.** Newtron walks every registered `required_when` at
  server start and panics on any reference to a field that doesn't exist on the
  kind's sample struct. A typo (`servce_type`) fails server start, not silently
  in the UI.

**Errors:**
- 404: `kind` is not a registered spec type

**i18n**: per-locale label/tooltip overrides stay at the UI layer — the backend
is not in the translation business. A UI that needs localized labels overlays its
own translations on top of the canonical English labels this endpoint returns.

### POST /newtron/v1/networks

Create a network. Operators name the topology by `id`; the server
resolves the on-disk path from its `--networks-base` config
(`filepath.Join(networks-base, id)`). No `dir` field on the wire — the
server owns the layout (§27, §33).

Always idempotent. The same call covers "make a new slot" and "pick
up an existing one" — the status code distinguishes them:

- **201 Created** — the slot was new to the server (just registered).
  Empty disk slot got materialized with three zero-valued spec files
  plus an empty `nodes/` subdirectory; pre-existing disk slot got
  loaded as-is.
- **200 OK** — the id was already registered in memory; the response
  carries the existing `NetworkInfo` so the caller learns the
  resolved path without re-fetching.

There's no "force-create / refuse on collision" mode. The status code
already tells the operator what happened; a UI that wants to surface
"name taken" reads 200 instead of 201.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Network identifier. Must match `^[A-Za-z0-9_-]{1,64}$`. Maps to `<networks-base>/<id>` on disk. |
| `description` | string | no | Free-text description seeded into `topology.json` when the slot is empty. Ignored on existing slots (no rewrite of authored specs). |

**Behavior matrix:**

| Slot state                              | Outcome                            |
|-----------------------------------------|------------------------------------|
| Already registered (in memory)          | 200, existing `NetworkInfo`        |
| Disk slot has valid specs               | 201, register existing             |
| Disk slot empty / missing               | 201, create empty specs + register |
| Disk slot has invalid specs             | 500, spec load error               |

**Response body** (201 or 200): the canonical `NetworkInfo` (same
shape as `GET /networks`), carrying the resolved `dir` so the caller
learns the path the server picked.

```json
{
  "data": {
    "id": "default",
    "dir": "/etc/newtron/networks/default",
    "has_topology": true,
    "topology": "default",
    "nodes": []
  }
}
```

**Status codes:** 201 created, 200 already exists, 400 missing or
malformed `id`, 500 server has no `--networks-base` configured / spec
load error.

**Examples:**

Create or register-existing (the default operator intent):

```
POST /newtron/v1/networks
{"id": "demo"}
```

With a description seed for a fresh topology:

```
POST /newtron/v1/networks
{"id": "demo", "description": "Demo network"}

→ 201
{
  "data": {
    "id": "demo",
    "dir": "/etc/newtron/networks/demo",
    "has_topology": true,
    "topology": "demo",
    "nodes": []
  }
}
```

### GET /newtron/v1/networks

List all registered networks.

**Response (200):**

```json
{
  "data": [
    {
      "id": "default",
      "dir": "/etc/newtron",
      "has_topology": true,
      "nodes": ["switch1", "switch2"]
    }
  ]
}
```

The `nodes` field lists device names from the topology file (empty if `has_topology`
is false).

### POST /newtron/v1/networks/{netID}/unregister

Unregister a network. Closes all cached SSH connections for the network.

**Path parameters:**

| Name | Type | Description |
|------|------|-------------|
| `netID` | string | Network identifier |

**Response (200):**

```json
{"data": {"status": "unregistered"}}
```

**Status codes:** 200 success, 500 network not registered or has active node connections

### POST /newtron/v1/networks/{netID}/reload

Reload a network's specs from disk without restarting the server. Stops the existing
networkEntity (draining all NodeActors and SSH connections), reloads specs from the
stored network directory, and creates a fresh networkEntity. SSH connections reconnect
lazily on the next request.

Use this after modifying spec files on disk (manually or via another tool) to pick
up changes without a full server restart.

**Path parameters:**

| Name | Type | Description |
|------|------|-------------|
| `netID` | string | Network identifier |

**Response (200):**

```json
{"data": {"status": "reloaded"}}
```

**Status codes:** 200 success, 404 network not registered, 500 network directory load error

**Example:**

```
POST /newtron/v1/networks/default/reload
```

**Notes:**
- All cached SSH connections are closed. The next device operation will reconnect.
- Spec mutations made via the API (service create, filter add-rule, etc.) are safe --
  they write to disk immediately via atomic temp+rename. Reload re-reads from disk,
  so no API changes are lost.
- The operation is atomic from the caller's perspective: the old actor is stopped and
  the new one created while holding the server's write lock. Concurrent requests will
  queue until reload completes.

---

## 4. Network Spec Reads

These endpoints read from the in-memory network spec -- service definitions, VPN
specs, QoS policies, filters, platforms, nodes, zones, and topology
metadata. They do not connect to any device; they read what was loaded from the
network directory at registration time.

All spec read endpoints require a registered network (`{netID}`). Atomicity is
provided by the engine layer: each Network method acquires a per-key lock internally,
so concurrent reads and writes are safe without any API-layer coordination.

### Spec Resource Endpoints (List / Show)

Ten resource types follow an identical pattern -- list all returns an array, show
one by name returns a single object (or 404 if not found):

| Resource | List endpoint | Show endpoint | Response type |
|----------|--------------|---------------|---------------|
| Services | `GET /newtron/v1/networks/{netID}/services` | `GET .../services/{name}` | [`ServiceDetail`](#servicedetail) |
| IP-VPNs | `GET /newtron/v1/networks/{netID}/ipvpns` | `GET .../ipvpns/{name}` | [`IPVPNDetail`](#ipvpndetail) |
| MAC-VPNs | `GET /newtron/v1/networks/{netID}/macvpns` | `GET .../macvpns/{name}` | [`MACVPNDetail`](#macvpndetail) |
| QoS Policies | `GET /newtron/v1/networks/{netID}/qos-policies` | `GET .../qos-policies/{name}` | [`QoSPolicyDetail`](#qospolicydetail) |
| Filters | `GET /newtron/v1/networks/{netID}/filters` | `GET .../filters/{name}` | [`FilterDetail`](#filterdetail) |
| Platforms | `GET /newtron/v1/networks/{netID}/platforms` | `GET .../platforms/{name}` | [`PlatformDetail`](#platformdetail) |
| Route Policies | `GET /newtron/v1/networks/{netID}/route-policies` | `GET .../route-policies/{name}` | Route policy detail |
| Prefix Lists | `GET /newtron/v1/networks/{netID}/prefix-lists` | `GET .../prefix-lists/{name}` | Prefix list detail |
| Nodes | `GET /newtron/v1/networks/{netID}/nodes` | `GET .../nodes/{name}` | [`NodeSpec`](#nodespec) |
| Zones | `GET /newtron/v1/networks/{netID}/zones` | `GET .../zones/{name}` | [`ZoneDetail`](#zonedetail) |

All response types are defined in [S13 Types Reference](#13-types-reference).

**Example:**

```
GET /newtron/v1/networks/default/service          -> {"data": [ ... array of ServiceDetail ... ]}
GET /newtron/v1/networks/default/service/transit  -> {"data": { ... single ServiceDetail ... }}
GET /newtron/v1/networks/default/service/missing  -> {"error": "not found: service 'missing'"}
```

### Cross-Scope Spec Inventory

```
GET /newtron/v1/networks/{netID}/spec-instances
```

newtron stores specs hierarchically -- the same kind may be defined at the
**network** scope (network.json), at a **zone** (network.json `zones[<name>]`),
and at a **node** (nodes/`<name>`.json), with node overriding zone overriding
network. The per-kind list endpoints above return only the **network** scope.
This endpoint returns one **flat inventory of every spec at every scope**, each
entry tagged with the scope and instance it is defined at -- so a schema-driven
UI can present one flat list filtered by two dropdowns (scope, scope instance)
instead of replicating each kind's schema once per scope. Storage and resolution
stay hierarchical; only this read surface is flattened.

**Response (200):** an array of `SpecInstance`, sorted by `(scope, scope_instance, kind, name)`:

| Field | Type | Description |
|-------|------|-------------|
| `kind` | string | Spec kind: `ServiceSpec`, `IPVPNSpec`, `MACVPNSpec`, `QoSPolicy`, `RoutePolicy`, `FilterSpec`, `PrefixListSpec` |
| `name` | string | Canonical spec name (UPPER_SNAKE; equals the `GET .../services` etc. key) |
| `scope` | string | `network`, `zone`, or `node` |
| `scope_instance` | string | The zone or node name; empty for `network` scope |

```
GET /newtron/v1/networks/default/spec-instances
  -> {"data": [
       {"kind":"PrefixListSpec","name":"BOGONS","scope":"network","scope_instance":""},
       {"kind":"ServiceSpec","name":"TRANSIT","scope":"network","scope_instance":""},
       {"kind":"ServiceSpec","name":"TRANSIT","scope":"node","scope_instance":"leaf1"},
       {"kind":"PrefixListSpec","name":"LOCAL_PL","scope":"zone","scope_instance":"amer"}
     ]}
```

This is a **locating** inventory, not a resolution. It does **not** merge
overrides: a name defined at both `network` and a `node` appears as two separate
entries (as `TRANSIT` does above). It reports *where each definition lives*, not
which one a given node ends up applying after the `node > zone > network` merge.
The endpoint is purely additive; the per-kind list/show endpoints (network scope)
are unchanged.

#### GET /newtron/v1/networks/{netID}/services/{name}/projection

Returns the per-Node projection slices the named service contributes. For each
loaded Node that binds the service via an actuated `apply-service` intent, the
server runs the replay-diff technique (snapshot intent DB → trim the service's
intents → rebuild projection from trimmed set → diff against the full
projection) and returns the resulting `[]sonic.DriftEntry` per Node.

**Response (200):** `ServiceProjectionResult` with:

| Field | Type | Description |
|-------|------|-------------|
| `service` | string | The service name queried |
| `nodes` | ServiceProjectionNode[] | Per-Node slices, alphabetical by Node name. Empty when no loaded Node binds the service. |

`ServiceProjectionNode` carries:

| Field | Type | Description |
|-------|------|-------------|
| `node` | string | The Node name |
| `diff` | sonic.DriftEntry[] | Entries present in the Node's full projection but missing or modified in the trimmed projection. "missing" entries are exclusively the service's contribution; "modified" entries are fields the service overlays on top of other intents' contributions. |

**Example:**

```
GET /newtron/v1/networks/default/service/TRANSIT/projection
{
  "data": {
    "service": "TRANSIT",
    "nodes": [
      {
        "node": "switch1",
        "diff": [
          { "table": "INTERFACE", "key": "Ethernet0|10.1.0.0/31", "type": "missing", "expected": {} },
          { "table": "BGP_NEIGHBOR", "key": "default|10.1.0.1", "type": "missing", "expected": {...} }
        ]
      }
    ]
  }
}
```

Operationalizes operator-philosophy invariant #5 (why-mode is always available)
at the service scope — Provenance answers "what does this service contribute on
each Node?" with substrate-grade per-entry detail rather than a summary. §11 +
§46.

_Lands newtron#6 (Phase 3 — Cluster A.6 / per-service projection slice)._

### Authorization

#### GET /newtron/v1/networks/{netID}/authorization

Returns the network's authorization table — `user_groups`,
`permissions`, and `super_users` exactly as they live in
`network.json`. One round trip exposes everything an operator
would see hand-editing the spec file; an inspector mounted on
this endpoint reads byte-for-byte like the source.

**Response (200):** `AuthorizationDetail` with:

| Field | Type | Description |
|-------|------|-------------|
| `user_groups` | `{[group: string]: string[]}` | Each entry maps a group name to the list of usernames in that group. Empty `{}` when no groups are defined. |
| `permissions` | `{[permission: string]: PermissionGrant[]}` | Each entry maps a permission key (`spec.author`, `node.vlan.create`, etc.) to the grants that confer it. Grants encode in the same wire form the spec file accepts: shorthand `["group", ...]` when every grant has an empty `where`, typed `[{"groups": [...], "where": {...}}]` when any grant carries a scope. Empty `{}` when no permissions are configured. |
| `super_users` | `string[]` | Usernames that bypass every permission check ([auth-design.md §L3](auth-design.md)). Empty `[]` when none are configured. |

The wire shape is the canonical spec shape (DESIGN_PRINCIPLES_NEWTRON
§46): an operator can copy a `permissions` block straight from a
response into `network.json` and the loader will accept it
unchanged.

**Example:**

```
GET /newtron/v1/networks/default/authorization
{
  "data": {
    "user_groups": {
      "net-admins":  ["alice"],
      "edge-admins": ["bob"]
    },
    "permissions": {
      "spec.author": ["net-admins"],
      "node.vlan.create": [
        {"groups": ["edge-admins"], "where": {"device": "switch1"}}
      ]
    },
    "super_users": ["root"]
  }
}
```

**Errors:** 404 when `{netID}` is not a registered network.

This endpoint is **engage-when-configured** by `auth.read`: when
no `auth.read` entry is in the grant table, it stays ungated
(preserves the original behavior the inspector shipped with). The
moment an operator adds the first `auth.read` entry, the gate
engages and fail-closes on any caller not matched by a grant.
Super-users continue to bypass. See [auth-design.md §L3](auth-design.md)
and [authorization-howto.md §"Reading the grant table"](authorization-howto.md).

_Lands newtron#150 (initial) + newtron#187 (gate)._

### Audit log

Three read endpoints over the audit log file the operator configured
via `--audit-log` on `cmd/newt-server`. All are gated by
`audit.read` under the same engage-when-configured pattern as
`auth.read` — no entry in the grant table means ungated; the first
entry engages the gate. `audit.read` is filed under newtron#196.

When `--audit-log` is unset on `cmd/newt-server`, all three endpoints
return 404 — there is no audit log to inspect.

**Envelope vs. content.** An audit event records both *that* something
happened (who/when/verb/outcome) and *what* it did. The content is two
fields the middleware captures from the live request/response:

- `changes` — the CONFIG_DB / intent rows the operation added, removed, or
  updated (the same `sonic.ConfigChange` shape device writes return). Empty
  for spec-authoring and read/no-op operations, which produce no device
  change. Carried on both the list and the detail endpoint. Each change
  carries `fields` (the after-state) and, for a CONFIG_DB row, `from` (the
  before-state — the values it overwrote or deleted), making the row reversible
  without re-reading the device. `from` is omitted on a pure add and on
  newtron's own `NEWTRON_INTENT` / `NEWTRON_HISTORY` substrate rows (those are
  reversed by replaying the inverse operation, not by raw row writes).
- `request_body` — the JSON the caller submitted, with secret-bearing fields
  (`ssh_pass`, `password`, `secret`, `token`, …) redacted to `***redacted***`.
  A `${secret:KEY}` reference is preserved — it is a pointer, not a secret.
  Carried **only by the per-event detail endpoint** (below); the paged list
  omits it so list responses stay lean.

#### GET /newtron/v1/networks/{netID}/audit/events

Paged, filtered read of audit events. Query-string parameters map
1:1 to the in-memory `audit.Filter` shape — every dimension is
optional and missing means "no constraint."

**Query parameters (all optional):**

| Param | Type | Notes |
|---|---|---|
| `device` | string | equality match against `event.device` |
| `user` | string | equality match against `event.user` |
| `operation` | string | equality match against `event.operation` (typically the HTTP verb + path) |
| `service` | string | equality match against `event.service` |
| `interface` | string | equality match against `event.interface` |
| `since` | RFC3339 timestamp | lower bound (inclusive) on `event.timestamp` |
| `until` | RFC3339 timestamp | upper bound (inclusive) on `event.timestamp` |
| `success` | `true` or `false` | `true` returns only successful events; `false` returns only failures |
| `limit` | integer (default 100, max 1000) | page size |
| `offset` | integer (default 0) | offset into the filter's full match set |
| `order` | `desc` (default) or `asc` | `desc` returns newest events first (offset 0 is the most recent activity, paging walks back into history); `asc` returns chronological (hash-chain build) order |

Ordering is applied before `offset`/`limit`, so paging starts from the
chosen end. `total` and `next_offset` are order-independent.

Malformed values (non-RFC3339 timestamp, non-numeric `limit`,
unrecognized `success`, `order` other than `asc`/`desc`) surface as 400
with an actionable phrase identifying the field.

**Response (200):** `AuditEventPage` with:

| Field | Type | Description |
|---|---|---|
| `events` | `AuditEvent[]` | The page itself, in append order from the log. |
| `total` | integer | Total number of events matching the filter without paging — the client uses this to render "N of M" and decide whether to fetch another page. |
| `next_offset` | integer or null | When non-null, calling the endpoint again with `?offset=<next_offset>` returns the next page. When null, the current page exhausted the filter — no more pages. |

The `AuditEvent` shape is documented in §13 Types Reference. List
rows omit `request_body` (it is served only by the detail endpoint
below); `changes` is present when the operation produced a device
change.

**Errors:** 404 when `{netID}` is not registered or when
`--audit-log` is unset on the server; 400 on a malformed filter
parameter; 403 when the `audit.read` gate is engaged and the
caller has no matching grant.

_Lands newtron#196._

#### GET /newtron/v1/networks/{netID}/audit/events/{eventID}

Per-event detail view. `{eventID}` is the hash-chain `id` carried on each
event in the list response. Returns the single matching `AuditEvent`
including `request_body` — the field the list omits — so a UI can render
"what this one operation submitted and changed" on a clicked row without
bloating the paged list with every body.

The list answers "what happened"; this answers "what did this operation
submit and change". Scanning the append-only log for one `id` is cheap on
typical log sizes, and a detail fetch is one-per-click, not a polling loop.

**Response (200):** a single `AuditEvent` (§13 Types Reference) carrying
`changes` and the redacted `request_body`.

**Errors:** 404 when `{netID}` is not registered, when `--audit-log` is
unset, or when no event carries the given `id`; 403 when the `audit.read`
gate is engaged and the caller has no matching grant.

The CLI counterpart is `bin/newtron audit show <event-id>`.

#### GET /newtron/v1/networks/{netID}/audit/integrity

Walks the audit log's hash chain end to end (L6) and returns a
structured tamper-evidence result. Pure read; never mutates the
log. Cheap on typical log sizes (entries are JSON-lines; walking
is O(n) in entry count).

**Response (200):** `AuditIntegrityResult` with:

| Field | Type | Description |
|---|---|---|
| `chain_head_hash` | string | The running hash-chain head after walking every entry. Stable across calls when the log is unmodified; an operator can record this and re-check later as a cheap tripwire. |
| `entry_count` | integer | Count of integrity-enabled entries scanned. Pre-L6 entries (empty ID) are tolerated and counted but not chained. |
| `break_at` | integer | Line number of the first entry whose chain link didn't verify, or 0 if the chain is clean end to end. |
| `break_reason` | string | `"prev_hash mismatch"` or `"id mismatch"` describing the failure at `break_at`, or empty for a clean chain. |
| `verified_at` | RFC3339 timestamp | Server-side timestamp of this verification. Callers can cache the result client-side keyed on this value. |

**Errors:** 404 when `{netID}` is not registered or when
`--audit-log` is unset; 403 when the `audit.read` gate is engaged
and the caller has no matching grant.

A clean chain has `break_at == 0` and `break_reason == ""`. Any
non-zero `break_at` indicates tamper — the line at `break_at` was
inserted, removed, reordered, or modified after the fact. Operators
inspect the surrounding entries in the on-disk log to learn what
changed.

_Lands newtron#196._

### Topology

#### GET /newtron/v1/networks/{netID}/topology

Returns the full topology as `TopologyView` — the same JSON shape as the
on-disk `TopologySpecFile` (devices, links, newtlab metadata), with each step
additionally carrying **server-derived `spec_kind` / `spec_name`** (via the
same `DeriveSpecRef` as `/intent/tree` — `service`/`ipvpn`/`macvpn`/`qos`, and
`filter` for service-derived ACLs; `omitempty` for primitives).

Unlike `/intent/tree` (per-device, requires a deployed lab), this is **one call
for the whole network and works before any lab is deployed** — it's a spec-file
read. It's the source for "where is spec X applied?" reverse-index views:
`spec_name` is the **canonical** spec key (see `/intent/tree` above), so a client
matches it against the `GET /services` / `/ipvpns` key directly. The derived
fields are computed at serve time (never stale), are **not** persisted to
`topology.json` (its `spec.TopologyStep` stays `url`/`params`), and are
output-only — they don't round-trip into `/intent/save`.

**Response (200):** `TopologyView` with `version`, `description`,
`devices` (map of name → `{ steps[], ports }`, each step `{ url, params,
spec_kind?, spec_name? }`), and `links` (array; omitted when empty).

**Errors:** 404 when no `topology.json` was loaded for the network.

_Lands newtron#14 (Cluster C — topology spec substrate, §46)._

#### POST /newtron/v1/networks/{netID}/topology/create-node

Adds a device entry to `topology.json`. the matching node-spec file
(`nodes/{name}.json`) must already exist; if absent, the call returns 400
with the resolution path included.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Topology device name; must match a node-spec filename. |
| `device` | TopologyNode | yes | Typed entry: `ports` (map of port name → `PortConfig`: `admin_status`/`mtu`/`speed`/`description` — form at `GET /schema/PortConfig`) and optional `steps[]` (intent operations to replay when the node is built). May be empty for a bare declaration; subsequent operations + `intent save --topology` populate `steps[]`. |

**Response (201):** the persisted `TopologyNode`.

**Errors:** 409 with `*ConflictError` if a device with this name already
exists; 400 if the node-spec file is missing or the body is invalid.

#### DELETE /newtron/v1/networks/{netID}/topology/nodes/{name}

Removes a device entry from `topology.json`. Default behavior **refuses**
when any link still references the device — operator must delete those
links first, or pass `?force=true` to cascade-delete the referring links
along with the device (DESIGN_PRINCIPLES §15: cascade is explicit, never
implicit). Closes any api-layer NodeActor cache for this name.

**Path params:** `name` (the topology device name).

**Query params:** `force` (`true` to cascade through referring links).

**Response (200):** `{"deleted": "<name>"}`.

**Errors:** 404 when the name doesn't exist; 409 with `*ConflictError` (and
`References` listing the referring links) when `force` is absent and links
remain wired to the device.

#### PUT /newtron/v1/networks/{netID}/topology/nodes/{name}

Replaces the device entry at `name` with the body (full-replacement
semantics — no partial patch). Closes the api-layer NodeActor cache so the
next request rebuilds from the new spec.

**Path params:** `name`.

**Request body:** `TopologyNode` (the full new entry).

**Response (200):** the new `TopologyNode`.

**Errors:** 404 when the name doesn't exist; 400 if node-spec missing or body
invalid.

#### POST /newtron/v1/networks/{netID}/topology/create-link

Adds a link to `topology.json`. Refuses when either endpoint is already
wired to another link (a port participates in at most one link). Validates
that both endpoint devices exist in topology AND that each interface is
declared on its device's `Ports` map.

**Request body:** `TopologyLink` (`{a: "device:interface", z: "device:interface"}`).

**Response (201):** the persisted `TopologyLink`.

**Errors:** 409 with `*ConflictError` when an endpoint is already wired;
400 when an endpoint device or interface is unknown.

#### DELETE /newtron/v1/networks/{netID}/topology/links/{device}/{interface}

Removes the link containing the given `{device, interface}` endpoint.
Single-endpoint identification: a port participates in at most one link, so
one endpoint uniquely identifies the link.

**Path params:** `device`, `interface`.

**Response (200):** `{"deleted": "<device>:<interface>"}`.

**Errors:** 404 when no link contains the endpoint.

_All five CRUD endpoints land newtron#15 + #16 (Phase 5 — topology spec
substrate CRUD). §7 + §15 + §27 + §46._

#### GET /newtron/v1/networks/{netID}/topology/nodes

List device names from the topology file.

**Response (200):** Array of strings (device names)

**Example response:**

```json
{"data": ["switch1", "switch2"]}
```

### Hosts

#### GET /newtron/v1/networks/{netID}/hosts/{name}

Get the host profile for a virtual host device. Returns 404 for switch devices
(even if they exist in the topology) -- the client uses 200 vs 404 from this
endpoint to classify devices as hosts vs switches.

**Response (200):** `HostProfile` (see [S13](#hostprofile))

**Status codes:** 200 success, 404 not a host device or not found

### Features

#### GET /newtron/v1/networks/{netID}/features

List all features and their support status.

**Response (200):** Feature map

#### GET /newtron/v1/networks/{netID}/features/{name}/dependency

Get the dependency list for a feature.

**Path parameters:** `name` -- feature name

**Response (200):** Array of dependency strings

#### GET /newtron/v1/networks/{netID}/features/{name}/unsupported-due-to

Get the features that cause a given feature to be unsupported.

**Response (200):** Array of blocking feature strings

#### GET /newtron/v1/networks/{netID}/platforms/{name}/supports/{feature}

Check whether a platform supports a specific feature.

**Path parameters:** `name` -- platform name, `feature` -- feature name

**Response (200):**

```json
{"data": {"supported": true}}
```

---

## 5. Network Spec Writes

These endpoints create and delete spec definitions (services, VPNs, QoS policies,
filters, nodes, zones, prefix lists, route policies). They modify the
in-memory spec and persist changes to the network directory on disk. Like spec reads,
atomicity is provided by the engine layer: each Network method acquires its key's
lock internally before composing or persisting the spec change.

All spec write endpoints use RPC-style naming: `POST .../create-X` and
`POST .../delete-X`. They accept the `dry_run` query parameter. When `dry_run=true`,
the spec is validated but not persisted.

#### Referential integrity (both directions, all kinds)

Cross-spec references are checked uniformly — the relationships are read from the
`ref:` schema tags, so every kind and every reference is covered without
per-endpoint logic:

- **Create / update — forward check.** A spec may only reference specs that
  exist. Creating or updating a spec whose references don't resolve (e.g. a
  service naming an IP-VPN, filter, QoS policy, route-policy, or prefix-list
  that isn't defined) is rejected with **400** and lists the unresolved
  references. Create dependencies before the specs that reference them.
- **Delete — reverse check (spec references).** A spec may not be deleted while
  another spec references it. The delete is refused with **409** (`ConflictError`)
  listing the referrers (e.g. *"PrefixListSpec 'BOGONS' has 2 references:
  ServiceSpec 'EDGE' (import_prefix_list), FilterSpec 'MGMT' (src_prefix_list)"*).
  Remove the references first; **there is no force-cascade for spec references**
  (newtron will not auto-delete a consuming spec). `force_available` is **false**.

- **Delete — active-binding check (topology steps).** A `service`, `ipvpn`,
  `macvpn`, `qos`, or `filter` spec may not be deleted while it is still applied
  on an interface — i.e. an `apply-service` / `bind-ipvpn` / `bind-macvpn` /
  `bind-qos` / `create-acl` step in some device's topology references it. This is
  a distinct dimension from the spec-reference graph above: a service applied on
  six interfaces may be referenced by no other spec yet still be bound on the
  wire. The delete is refused with **409** (`ConflictError`) whose `references`
  enumerate every binding as `device:interface` (e.g.
  *"ServiceSpec 'TRANSIT' has 2 references: switch1:Ethernet0, switch2:Ethernet0"*).
  `force_available` is **true**: pass `?force=true` to cascade-remove the binding
  steps from topology.json as part of the delete. Force removes only the topology
  record — on a live device the applied CONFIG_DB persists until reconciled, so
  un-apply on the device first (`remove-service`) to avoid drift. Both checks run
  on every spec delete; the binding check fires first.

  The 409 carries the conflict's **structured shape** in the envelope `data`
  (§46), so clients branch on the payload rather than parsing the message:

  ```json
  { "data": { "resource": "PrefixListSpec", "name": "BOGONS",
              "references": ["ServiceSpec 'EDGE' (import_prefix_list)", "FilterSpec 'MGMT' (src_prefix_list)"],
              "force_available": false },
    "error": "PrefixListSpec 'BOGONS' has 2 references: …" }
  ```

  `force_available` is **false** for spec-reference conflicts and existence
  collisions, and **true** for the deletes that actually cascade (active spec
  bindings, profile, topology-device) — so a UI offers a "force delete"
  affordance only when the payload says so. The message string mirrors this: it
  appends *"— pass force=true to cascade"* only when `force_available` is true.
  Every `ConflictError`-bearing 409 (spec, profile, zone, topology) uses this one
  shape.

#### Scoped writes (network / zone overrides)

Spec writes are scope-aware — the "flat at the boundary, hierarchical
underneath" surface (see [§4 Cross-Scope Spec Inventory](#cross-scope-spec-inventory)).
Every `create-`/`update-`/`delete-` body accepts two optional fields:

| Field | Type | Description |
|-------|------|-------------|
| `scope` | string | `network` (default), `zone`, or `node` |
| `scope_instance` | string | the zone or node name; required when `scope` is `zone`/`node`, empty for `network` |

Both fields are **declared in the schema** (`GET /schema/{kind}`) for every
overridable kind and its sub-rule kinds:

- `scope` — `type:enum` (`network,zone,node`) with `default:"network"`.
- `scope_instance` — `type:ref`, gated by `applies_when`/`required_when`
  `{field:"scope", not_equals:"network"}`, and a **sibling-conditional**
  reference via `ref_when`: it resolves to `ZoneSpec` when `scope=zone` and
  `NodeSpec` when `scope=node`, so the UI offers a dropdown of the right
  instances (zone names / node names) rather than free text.

  ```jsonc
  { "name": "scope_instance", "type": "ref",
    "applies_when":  { "field": "scope", "not_equals": "network" },
    "required_when": { "field": "scope", "not_equals": "network" },
    "ref_when": [
      { "when": { "field": "scope", "equals": "zone" }, "ref_kind": "ZoneSpec" },
      { "when": { "field": "scope", "equals": "node" }, "ref_kind": "NodeSpec" }
    ] }
  ```

A schema-driven form therefore renders the scope dropdown + a conditional,
correctly-populated instance dropdown automatically. The fields are not on the
spec structs — `scope` is write-location metadata, not spec content — they are
injected at the schema layer for these kinds.

**Absent `scope` means `network` — existing callers are unaffected.** A scoped
write authors an *override* of a network-scope definition; storage stays
hierarchical (network → zone → node, node wins), only the write surface is flat.

These two fields are accepted by **every** write verb in this section — the
`create-`/`update-`/`delete-` verbs for all overridable kinds **and** the
sub-rule verbs (`add`/`update`/`remove`-`filter-rule`, `-qos-queue`,
`-route-policy-rule`, `-prefix-list-entry`). The per-endpoint request-body tables
below list only each verb's kind-specific fields and omit `scope`/`scope_instance`
for brevity; they apply uniformly per this section.

**Network-floor invariant.** A resource may exist at zone/node scope only if it
also exists at network scope. This keeps resolution total (every node resolves at
least the network base — no dangling references from any perspective) and means
forward referential integrity is unchanged: a reference resolves iff it resolves
at network. The invariant is enforced server-side:

- **Creating a scoped override with no network base → 400** (the override
  "references" a required network base that is absent). So the UI offers
  "override" only on resources the inventory already shows at network scope — no
  bespoke client rule needed, just a filter on `/spec-instances`.
- **Deleting a scoped override is always safe** — consumers fall back to the
  network base.
- **Deleting the network base is refused (409)** while anything references it at
  *any* scope, or while any override still sits below it. Delete bottom-up:
  remove the overrides (and references) first.

**Scope coverage:** all spec kinds — `service`, `ipvpn`, `macvpn`, `prefix-list`,
and the rule-bearing `filter`, `qos-policy`, `route-policy` (including their
sub-rule endpoints `add`/`update`/`remove-filter-rule`, `…-qos-queue`,
`…-route-policy-rule`, and `add`/`remove-prefix-list-entry`) — at `network`,
`zone`, and `node` scope (`scope_instance`
is the device/profile name for node scope). A sub-rule write targets the
filter/policy **at that scope**: e.g. `add-filter-rule` with `scope:zone` adds the
rule to the zone's filter override (which must already exist at that scope), not
the network base. A node-scope write persists to `nodes/<name>.json` and never
rewrites a secret-resolved value — the profile is round-tripped through disk so
`${secret:…}` references stay intact.

**Deleting a scope container.** A zone or node profile that still holds spec
overrides — or that something else references — cannot be deleted out from under
them (§15):

- `delete-zone` is refused (**409**) while a profile is assigned to the zone
  (`zone` field) or the zone still holds spec overrides; the response lists every
  dependant. Remove them first.
- `delete-node` is refused (**409**) while the profile holds node-scope spec
  overrides (or, as before, while a topology device references it). `force=true`
  proceeds — the overrides live in the profile file and are removed with it.

### Services

#### POST /newtron/v1/networks/{netID}/create-service

Create a new service definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Service name |
| `type` | string | yes | One of: `routed`, `bridged`, `irb`, `evpn-routed`, `evpn-bridged`, `evpn-irb` |
| `ipvpn` | string | no | IP-VPN reference (required for `evpn-routed`, `evpn-irb`) |
| `macvpn` | string | no | MAC-VPN reference (required for `evpn-bridged`, `evpn-irb`) |
| `vrf_type` | string | no | VRF type (`"shared"` or `"per-interface"`) |
| `qos_policy` | string | no | QoS policy reference |
| `ingress_filter` | string | no | Ingress filter reference |
| `egress_filter` | string | no | Egress filter reference |
| `description` | string | no | Human-readable description |

**Response (201):**

```json
{"data": {"name": "customer-l3"}}
```

**Status codes:** 201 created, 400 validation error, 404 network not found

**Example:**

```
POST /newtron/v1/networks/default/create-service
{
  "name": "customer-l3",
  "type": "evpn-routed",
  "ipvpn": "customer-vpn",
  "description": "L3 overlay service with IP-VPN"
}
```

#### POST /newtron/v1/networks/{netID}/delete-service

Delete a service definition.

**Query parameters:** `dry_run`, `force` (cascade-remove active `apply-service` bindings — see [Referential integrity](#referential-integrity-both-directions-all-kinds))

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Service name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

**Status codes:** 200 success, 404 service not found, 409 still applied on interfaces (`ConflictError`; `?force=true` cascades)

#### POST /newtron/v1/networks/{netID}/update-X — full-replacement spec edit (#152)

One verb per spec kind: `update-service`, `update-ipvpn`,
`update-macvpn`, `update-qos-policy`, `update-filter`,
`update-prefix-list`, `update-route-policy`, `update-node`,
`update-zone`. Each accepts the same request body shape as its
`create-X` counterpart and replaces the entry whose `name` field
matches an existing one in place.

**Semantics — full-replacement of the request shape**:
every field in the request body becomes the new content for that
name; omitted fields revert to their JSON-zero value. The
`UpdateTopologyDevice` precedent at
`PUT /networks/{netID}/topology/nodes/{name}` is the same shape;
this PR brings the spec kinds in line with it (issue #152).

**Sub-collection preservation — contract.** For three kinds the
update-X verb **always preserves the existing entry's sub-collection,
regardless of what (if anything) the request body carries for that
field**:

| Verb | Preserved sub-collection field |
|---|---|
| `update-filter` | `rules` |
| `update-route-policy` | `rules` |
| `update-qos-policy` | `queues` |

A request body that includes a `rules` (or `queues`) field is accepted
but the field is **ignored**: the existing entry's sub-collection
remains intact. A request body that omits the field has the same
effect. This is a stable contract, not an implementation detail.

The rationale is structural. The dedicated sub-rule verbs —
`add-filter-rule` / `update-filter-rule` / `remove-filter-rule`,
`add-route-policy-rule` / `update-route-policy-rule` /
`remove-route-policy-rule`, `add-qos-queue` / `update-qos-queue` /
`remove-qos-queue` (#209, #210, #211) — **own the sub-collection
lifecycle**. If `update-filter` replaced the rule list from its body,
those verbs would race with it: an operator editing the filter's
description via `update-filter` would silently wipe rules curated via
the sub-rule verbs in a different session. Preservation is the only
sane semantics for a parent-edit verb in a kind where sub-collections
have their own verbs.

`update-prefix-list` is the deliberate exception. A prefix list's only
content IS its `prefixes` sub-collection — there is no parent metadata
to edit independently. The request body's `prefixes` field therefore
replaces the entry's list directly; the per-entry `add-prefix-list-
entry` / `remove-prefix-list-entry` verbs offer the finer-grained
alternative.

The other 5 top-level update verbs (`update-service`, `update-ipvpn`,
`update-macvpn`, `update-node`, `update-zone`) have no
sub-collection to preserve — every field carried by the request body
becomes the new content directly.

**Prefix-list-entry mutation.** A prefix-list entry has no fields
beyond the CIDR itself (`PrefixLists` is `map[string][]string`), so
the per-entry verbs are append and delete:

- `add-prefix-list-entry` — atomic append.
- `remove-prefix-list-entry` — atomic delete.

The prefix IS the entry's identity; there are no other fields to
update, so the verb that would have changed the prefix was structurally
a swap-named-update. Relocating the entry to a different prefix is
remove + add. For multi-entry mid-life edits (replacing several
prefixes in one shot, reordering, full-list rewrite) under a single
lock, `update-prefix-list` is the right verb — it atomically swaps
the full entry list.

**Auth gate**: `spec.author` with `field = "<kind plural>"` and
`resource = "<name>"`. An operator who can `create-X` or
`delete-X` can also `update-X` (one identity for "may author specs
at this scope" — see `auth-design.md` §L3).

**Request body**: same as the `create-X` counterpart documented
above and below. The `name` field identifies which entry to replace.

**Response (200)**:

```json
{"data": {"name": "<name>"}}
```

**Status codes**: 200 success, 404 entry not found, 400 validation
error, 403 authorization denied.

### IP-VPNs

#### POST /newtron/v1/networks/{netID}/create-ipvpn

Create a new IP-VPN definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | IP-VPN name |
| `l3vni` | integer | yes | L3 VNI number |
| `vrf` | string | no | VRF name (defaults to IP-VPN name if omitted) |
| `route_targets` | string[] | no | Route target list (e.g., `["65000:100"]`) |
| `description` | string | no | Description |

**Response (201):**

```json
{"data": {"name": "customer-vpn"}}
```

#### POST /newtron/v1/networks/{netID}/delete-ipvpn

Delete an IP-VPN definition.

**Query parameters:** `dry_run`, `force` (cascade-remove active `bind-ipvpn` bindings)

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | IP-VPN name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### MAC-VPNs

#### POST /newtron/v1/networks/{netID}/create-macvpn

Create a new MAC-VPN definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | MAC-VPN name |
| `vni` | integer | yes | L2 VNI number |
| `vlan_id` | integer | no | Local bridge domain VLAN ID |
| `anycast_ip` | string | no | Anycast gateway IP (CIDR, e.g., `"10.1.1.1/24"`) |
| `anycast_mac` | string | no | Anycast gateway MAC |
| `route_targets` | string[] | no | Route target list |
| `arp_suppression` | boolean | no | Enable ARP suppression |
| `description` | string | no | Description |

**Response (201):**

```json
{"data": {"name": "l2-segment"}}
```

#### POST /newtron/v1/networks/{netID}/delete-macvpn

Delete a MAC-VPN definition.

**Query parameters:** `dry_run`, `force` (cascade-remove active `bind-macvpn` bindings)

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | MAC-VPN name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### QoS Policies

#### POST /newtron/v1/networks/{netID}/create-qos-policy

Create a new (empty) QoS policy definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Policy name |
| `description` | string | no | Description |

**Response (201):**

```json
{"data": {"name": "standard-qos"}}
```

#### POST /newtron/v1/networks/{netID}/delete-qos-policy

Delete a QoS policy definition.

**Query parameters:** `dry_run`, `force` (cascade-remove active `bind-qos` bindings)

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Policy name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

#### POST /newtron/v1/networks/{netID}/add-qos-queue

Add a queue to a QoS policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Policy name |
| `queue_id` | integer | yes | Queue number |
| `name` | string | yes | Queue name |
| `type` | string | yes | Queue type (e.g., `"strict"`, `"wrr"`) |
| `weight` | integer | no | Weight for WRR scheduling |
| `dscp` | integer[] | no | DSCP values mapped to this queue |
| `ecn` | boolean | no | Enable ECN |

**Response (201):**

```json
{"data": {"queue_id": 0}}
```

#### POST /newtron/v1/networks/{netID}/update-qos-queue

Update an existing queue in a QoS policy. `queue_id` identifies the queue; `new_queue_id` is optional — when present, the queue rotates to that slot (0–7). Mirrors `update-filter-rule`'s semantics. Issue #211.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Policy name |
| `queue_id` | integer | yes | Existing queue ID (0–7) |
| `new_queue_id` | integer | no | New queue ID — present only when rotating slot |
| `name` | string | yes | Queue name |
| `type` | string | yes | `"strict"` or `"dwrr"` |
| `weight` | integer | no | DWRR weight |
| `dscp` | array<integer> | no | DSCP values mapped to this queue |
| `ecn` | boolean | no | Enable ECN/WRED |

**Response (200):**

```json
{"data": {"queue_id": 4}}
```

**Errors:**
- 400: queue at `queue_id` not found; or `new_queue_id` already occupied; or either ID outside 0–7
- 403: caller lacks `PermSpecAuthor` on `qos_policies/{policy}`

#### POST /newtron/v1/networks/{netID}/remove-qos-queue

Remove a queue from a QoS policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Policy name |
| `queue_id` | integer | yes | Queue ID to remove |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### Filters

#### POST /newtron/v1/networks/{netID}/create-filter

Create a new (empty) filter definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Filter name |
| `type` | string | yes | Filter type (e.g., `"L3"`, `"L3V6"`) |
| `description` | string | no | Description |

**Response (201):**

```json
{"data": {"name": "customer-acl"}}
```

#### POST /newtron/v1/networks/{netID}/delete-filter

Delete a filter definition.

**Query parameters:** `dry_run`, `force` (cascade-remove active `create-acl` bindings)

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Filter name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

#### POST /newtron/v1/networks/{netID}/add-filter-rule

Add a rule to a filter.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filter` | string | yes | Filter name |
| `sequence` | integer | yes | Rule sequence number |
| `action` | string | yes | `"permit"` or `"deny"` |
| `src_ip` | string | no | Source IP/prefix |
| `dst_ip` | string | no | Destination IP/prefix |
| `src_prefix_list` | string | no | Source prefix list reference |
| `dst_prefix_list` | string | no | Destination prefix list reference |
| `protocol` | string | no | IP protocol (e.g., `"tcp"`, `"udp"`, `"6"`) |
| `src_port` | string | no | Source port or range |
| `dst_port` | string | no | Destination port or range |
| `dscp` | string | no | DSCP match value |
| `cos` | string | no | CoS match value |
| `log` | boolean | no | Enable logging for matched packets |

**Response (201):**

```json
{"data": {"seq": 10}}
```

#### POST /newtron/v1/networks/{netID}/update-filter-rule

Update an existing rule in a filter. `seq` identifies the rule; `new_seq` is optional — when present, the rule's sequence rotates to that value (renumber). Remaining fields replace the rule's current values.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filter` | string | yes | Filter name |
| `seq` | integer | yes | Sequence number of the existing rule |
| `new_seq` | integer | no | New sequence number — present only when renumbering |
| `action` | string | yes | `"permit"` or `"deny"` |
| `src_ip` | string | no | Source IP/prefix |
| `dst_ip` | string | no | Destination IP/prefix |
| `src_prefix_list` | string | no | Source prefix list reference |
| `dst_prefix_list` | string | no | Destination prefix list reference |
| `protocol` | string | no | IP protocol (e.g., `"tcp"`, `"udp"`, `"6"`) |
| `src_port` | string | no | Source port or range |
| `dst_port` | string | no | Destination port or range |
| `dscp` | string | no | DSCP match value |
| `cos` | string | no | CoS match value |

**Response (200):**

```json
{"data": {"seq": 5}}
```

The response's `seq` is the resulting sequence — equals `new_seq` when present, equals `seq` otherwise.

**Errors:**
- 400: rule at `seq` does not exist in the filter; or `new_seq` collides with another rule's sequence
- 403: caller lacks `PermSpecAuthor` on `filters/{filter}`

#### POST /newtron/v1/networks/{netID}/remove-filter-rule

Remove a rule from a filter.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filter` | string | yes | Filter name |
| `seq` | integer | yes | Sequence number to remove |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### Prefix Lists

#### POST /newtron/v1/networks/{netID}/create-prefix-list

Create a new prefix list.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Prefix list name |

**Response (201):**

```json
{"data": {"name": "customer-prefixes"}}
```

#### POST /newtron/v1/networks/{netID}/delete-prefix-list

Delete a prefix list.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Prefix list name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

#### POST /newtron/v1/networks/{netID}/add-prefix-list-entry

Add an entry to a prefix list.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `prefix_list` | string | yes | Prefix list name |
| `prefix` | string | yes | IP prefix (e.g., `"10.0.0.0/8"`) |

**Response (201):**

```json
{"data": {"prefix": "10.0.0.0/8"}}
```

#### POST /newtron/v1/networks/{netID}/remove-prefix-list-entry

Remove an entry from a prefix list.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `prefix_list` | string | yes | Prefix list name |
| `prefix` | string | yes | Prefix to remove |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### Route Policies

#### POST /newtron/v1/networks/{netID}/create-route-policy

Create a new route policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Route policy name |

**Response (201):**

```json
{"data": {"name": "import-policy"}}
```

#### POST /newtron/v1/networks/{netID}/delete-route-policy

Delete a route policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Route policy name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

#### POST /newtron/v1/networks/{netID}/add-route-policy-rule

Add a rule to a route policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Route policy name |
| `sequence` | integer | yes | Rule sequence number |
| (additional fields) | | | Rule match/action parameters |

**Response (201):**

```json
{"data": {"seq": 10}}
```

#### POST /newtron/v1/networks/{netID}/update-route-policy-rule

Update an existing rule in a route policy. `seq` identifies the rule; `new_seq` is optional — when present, the rule's sequence rotates to that value (renumber). Mirrors `update-filter-rule`'s semantics. Issue #210.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Route policy name |
| `seq` | integer | yes | Sequence number of the existing rule |
| `new_seq` | integer | no | New sequence number — present only when renumbering |
| `action` | string | yes | `"permit"` or `"deny"` |
| `prefix_list` | string | no | Prefix list reference for match |
| `community` | string | no | Community match |
| `set` | object | no | Set-actions (local_pref, community, med) |

**Response (200):**

```json
{"data": {"seq": 5}}
```

**Errors:**
- 400: rule at `seq` does not exist; or `new_seq` collides with another rule
- 403: caller lacks `PermSpecAuthor` on `route_policies/{policy}`

#### POST /newtron/v1/networks/{netID}/remove-route-policy-rule

Remove a rule from a route policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Route policy name |
| `seq` | integer | yes | Sequence number to remove |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### Nodes

A node's spec is stored as an individual JSON file under `nodes/{name}.json` in the
network directory. It defines per-node settings (management IP, loopback, zone,
platform, EVPN peering).

#### POST /newtron/v1/networks/{netID}/create-node

Create a new node spec.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Profile name (becomes `nodes/{name}.json`) |
| `mgmt_ip` | string | yes | Management IP address |
| `loopback_ip` | string | no | Loopback IP address |
| `zone` | string | yes | Zone name (must exist in network.json) |
| `platform` | string | no | Platform name (from platforms.json) |
| `underlay_asn` | integer | no | BGP underlay AS number |
| `ssh_user` | string | no | SSH username |
| `ssh_pass` | string | no | SSH password |
| `ssh_port` | integer | no | SSH port (default 22) |
| `evpn` | object | no | EVPN config: `peers` (array), `route_reflector` (bool), `cluster_id` (string) |

**Response (201):**

```json
{"data": {"name": "switch3"}}
```

**Status codes:** 201 created, 400 validation error, 409 already exists

#### POST /newtron/v1/networks/{netID}/delete-node

Delete a node spec.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Profile name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

**Status codes:** 200 success, 404 not found

### Zones

Zones group devices by location or function and can carry zone-level spec
overrides. They are stored in the `zones` map within `network.json`.

#### POST /newtron/v1/networks/{netID}/create-zone

Create a new zone.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Zone name |

**Response (201):**

```json
{"data": {"name": "dc2"}}
```

**Status codes:** 201 created, 400 validation error, 409 already exists

#### POST /newtron/v1/networks/{netID}/delete-zone

Delete a zone. Returns error if any node spec references this zone.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Zone name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

**Status codes:** 200 success, 404 not found, 409 zone still referenced by profiles

### Platforms (#173)

Closes the residual gap from #152 — platform definitions
(`platforms.json` entries) now have create/update/delete verbs,
matching the pattern the other 9 spec kinds use.

**Wire shape**: the request body embeds `spec.PlatformSpec` fields
at the same JSON level as `name`. Operators can copy a
`platforms.json` entry directly into the request body and the loader
will accept it unchanged (DPN §46 — wire shape mirrors canonical
types).

**`vm_credentials` field**: API-submitted values are stored as
plaintext. The `${secret:KEY}` reference indirection is a load-time
mechanism and is **not** re-resolved on Save. Operators authoring
secret references edit `platforms.json` directly and rely on
`--spec-watch` or `/reload`.

#### POST /newtron/v1/networks/{netID}/create-platform

Add a new platform definition.

**Query parameters:** `dry_run`

**Request body:** `name` (string, required) plus any `PlatformSpec` field — `hwsku`, `port_count`, `default_speed`, `description`, `device_type`, `breakouts`, the `vm_*` family, `dataplane`, `unsupported_features`, etc.

**Response (201):**

```json
{"data": {"name": "my-platform"}}
```

**Status codes:** 201 created, 400 validation error, 409 already exists, 403 authorization denied

#### POST /newtron/v1/networks/{netID}/update-platform

Replace an existing platform in place — full-replacement semantics matching the #152 update-X family. Every field on the request body becomes the new content for that name; omitted fields revert to their JSON-zero value.

**Query parameters:** `dry_run`

**Request body:** same shape as `create-platform`.

**Response (200):**

```json
{"data": {"name": "my-platform"}}
```

**Status codes:** 200 success, 404 platform not found, 400 validation error, 403 authorization denied

#### POST /newtron/v1/networks/{netID}/delete-platform

Delete a platform. Returns 409 with a `*ConflictError` listing referring profiles if any `NodeSpec.Platform` equals this name — the operator must retarget or delete the referring profiles first. There is no `force=true` cascade (a profile's Platform field is mandatory in practice; cascading would orphan the profile).

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Platform name to delete |

**Response (200):**

```json
{"data": {"name": "my-platform"}}
```

**Status codes:** 200 success, 404 platform not found, 409 referenced by profiles, 403 authorization denied

**Auth gate**: `spec.author` with `field = "platforms"` and `resource = "<name>"` for all three verbs.

---

## 6. Provisioning

Provisioning brings a device from clean-factory to fully-configured-per-topology.
It is decomposed into two operations:

1. **`POST /newtron/v1/networks/{n}/nodes/{d}/init-device`** — clean factory CONFIG_DB entries
   that would conflict with newtron-managed state. Idempotent. See below.
2. **`POST /newtron/v1/networks/{n}/nodes/{d}/intent/reconcile`** with `?mode=topology` — load
   the topology spec into the projection and deliver it to the device. This is
   the canonical "spec → device" path. See §11.

There is no separate `/provision` endpoint. The intent/reconcile pipeline IS the
provisioning pipeline — provisioning and reconciliation are two sides of the same
coin (substrate-faithful, §46): the only difference is whether the projection
starts from topology spec (provisioning) or from the device's existing intents
(maintenance reconcile). For network-wide provisioning, iterate over
`/networks/{n}/topology/nodes` and call init-device + intent/reconcile per node.

### POST /newtron/v1/networks/{netID}/nodes/{device}/init-device

Initialize a device by cleaning factory CONFIG_DB entries that conflict with
newtron-managed configuration. Idempotent -- returns `"already_initialized"`
if the device was previously initialized.

**Path parameters:** `device` -- device name from topology

**Request body (optional):**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `force` | boolean | no | Force re-initialization even if already initialized |

**Response (200):**

```json
{"data": {"status": "initialized"}}
```

or if already initialized:

```json
{"data": {"status": "already_initialized"}}
```

---

## 7. Node Read Operations

These endpoints read live device state by connecting to the device via SSH,
refreshing CONFIG_DB from Redis, and querying the cached data. They use the
`connectAndRead` pattern: connect -> refresh -> read. No `dry_run`/`no_save`
parameters apply.

All node endpoints require `{netID}` (registered network) and `{device}` (device
name from the network's topology or profiles). The first request to a device
establishes an SSH connection that is cached for subsequent requests.

### Device Overview

#### GET /newtron/v1/networks/{netID}/nodes/{device}/info

Get a structured overview of the device.

**Response (200):** `DeviceInfo` (see [S13](#deviceinfo))

**Example response:**

```json
{
  "data": {
    "name": "switch1",
    "mgmt_ip": "192.168.1.10",
    "loopback_ip": "10.0.0.1",
    "platform": "ciscovs",
    "zone": "dc1",
    "bgp_as": 65001,
    "router_id": "10.0.0.1",
    "vtep_source_ip": "10.0.0.1",
    "bgp_neighbors": ["10.0.0.2"],
    "interfaces": 32,
    "port_channels": 0,
    "vlans": 3,
    "vrfs": 2
  }
}
```

### Interfaces

#### GET /newtron/v1/networks/{netID}/nodes/{device}/interfaces

List all interfaces with summary status.

**Response (200):** Array of `InterfaceSummary` (see [S13](#interfacesummary))

#### GET /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}

Show detailed properties of a single interface.

**Path parameters:** `name` -- interface name (URL-encode slashes: `Ethernet0%2F1`)

**Response (200):** `InterfaceDetail` (see [S13](#interfacedetail))

**Status codes:** 200 success, 404 interface not found

#### GET /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/binding

Show the service binding on an interface.

**Path parameters:** `name` -- interface name

**Response (200):** `ServiceBindingDetail` (see [S13](#servicebindingdetail)) or `null` if no binding

### VLANs

#### GET /newtron/v1/networks/{netID}/nodes/{device}/vlans

List all VLANs with summary status.

**Response (200):** Array of `VLANStatusEntry` (see [S13](#vlanstatusentry))

#### GET /newtron/v1/networks/{netID}/nodes/{device}/vlans/{id}

Show a single VLAN with full details.

**Path parameters:** `id` -- VLAN ID (integer, 1-4094)

**Response (200):** `VLANStatusEntry`

**Status codes:** 200 success, 400 invalid VLAN ID, 404 VLAN not found

### VRFs

#### GET /newtron/v1/networks/{netID}/nodes/{device}/vrfs

List all VRFs with operational state.

**Response (200):** Array of `VRFStatusEntry` (see [S13](#vrfstatusentry))

#### GET /newtron/v1/networks/{netID}/nodes/{device}/vrfs/{name}

Show a VRF with its interfaces and BGP neighbors.

**Path parameters:** `name` -- VRF name

**Response (200):** `VRFDetail` (see [S13](#vrfdetail))

**Status codes:** 200 success, 404 VRF not found

### ACLs

#### GET /newtron/v1/networks/{netID}/nodes/{device}/acls

List all ACL tables with summary info.

**Response (200):** Array of `ACLTableSummary` (see [S13](#acltablesummary))

#### GET /newtron/v1/networks/{netID}/nodes/{device}/acls/{name}

Show an ACL table with all its rules.

**Path parameters:** `name` -- ACL table name

**Response (200):** `ACLTableDetail` (see [S13](#acltabledetail))

**Status codes:** 200 success, 404 ACL not found

### BGP

#### GET /newtron/v1/networks/{netID}/nodes/{device}/bgp/status

Get BGP status including local AS, router ID, and all neighbors with operational state.

**Response (200):** `BGPStatusResult` (see [S13](#bgpstatusresult))

**Example response:**

```json
{
  "data": {
    "local_as": 65001,
    "router_id": "10.0.0.1",
    "loopback_ip": "10.0.0.1",
    "neighbors": [
      {
        "address": "10.100.0.1",
        "vrf": "",
        "type": "underlay",
        "remote_as": "65002",
        "state": "Established",
        "pfx_rcvd": "3",
        "uptime": "01:23:45"
      }
    ],
    "evpn_peers": ["10.0.0.2"]
  }
}
```

#### GET /newtron/v1/networks/{netID}/nodes/{device}/bgp/check

Check BGP session states. Returns the same data as `bgp/status` (both call
`CheckBGPSessions` internally) but is semantically a health probe -- clients
use it to assert that all sessions are established.

**Response (200):** `BGPStatusResult`

### EVPN

#### GET /newtron/v1/networks/{netID}/nodes/{device}/evpn/status

Get EVPN overlay status: VTEP tunnels, NVO configuration, VNI mappings, L3VNI
VRF bindings, remote VTEPs, and VNI count.

**Response (200):** `EVPNStatusResult` (see [S13](#evpnstatusresult))

### Health

#### GET /newtron/v1/networks/{netID}/nodes/{device}/health

Run a comprehensive health check on the device. Includes CONFIG_DB verification
(comparing committed config against running config) and operational checks (BGP
sessions, interface status).

**Response (200):** `HealthReport` (see [S13](#healthreport))

**Example response:**

```json
{
  "data": {
    "device": "switch1",
    "status": "healthy",
    "config_check": {"passed": 42, "failed": 0},
    "oper_checks": [
      {"check": "bgp", "status": "pass", "message": "3/3 sessions established"},
      {"check": "interface-oper", "status": "pass", "message": "all admin-up interfaces are oper-up"}
    ]
  }
}
```

### LAGs

#### GET /newtron/v1/networks/{netID}/nodes/{device}/lags

List all LAGs (PortChannels) with member and operational status.

**Response (200):** Array of `LAGStatusEntry` (see [S13](#lagstatusentry))

#### GET /newtron/v1/networks/{netID}/nodes/{device}/lags/{name}

Show a single LAG with full details.

**Path parameters:** `name` -- LAG name (e.g., `PortChannel1`)

**Response (200):** `LAGStatusEntry`

**Status codes:** 200 success, 404 LAG not found

### Neighbors

#### GET /newtron/v1/networks/{netID}/nodes/{device}/neighbors

Get BGP session state. This is functionally identical to `bgp/check` -- both
call `CheckBGPSessions` internally and return `BGPStatusResult`.

**Response (200):** `BGPStatusResult`

### Routes

#### GET /newtron/v1/networks/{netID}/nodes/{device}/routes/{vrf}/{prefix...}

Look up a route in APP_DB (FRR's routing table as synced by fpmsyncd).

**Path parameters:**
- `vrf` -- VRF name (use `"default"` for the global table)
- `prefix` -- IP prefix with mask (e.g., `10.0.0.0/24`). Uses catch-all pattern;
  no URL encoding needed for the slash.

**Response (200):** `RouteEntry` (see [S13](#routeentry))

**Status codes:** 200 success, 404 route not found

**Example:**

```
GET /newtron/v1/networks/default/node/switch1/route/default/10.0.0.0/24
```

#### GET /newtron/v1/networks/{netID}/nodes/{device}/routes-asic/{prefix...}

Look up a route in ASIC_DB (SAI route table as programmed by orchagent).

**Path parameters:** `prefix` -- IP prefix with mask (catch-all pattern)

**Response (200):** `RouteEntry` with `source: "ASIC_DB"`

**Example:**

```
GET /newtron/v1/networks/default/node/switch1/route-asic/10.0.0.0/24
```

### Intent Tree

#### GET /newtron/v1/networks/{netID}/nodes/{device}/intent/tree

Get a tree view of the intent DAG (directed acyclic graph). The intent tree
shows parent-child relationships between intent records.

**Query parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `kind` | string | `""` | Filter by intent kind (e.g., `"service"`, `"vlan"`) |
| `resource` | string | `""` | Filter by resource name |
| `ancestors` | string | `"false"` | When `"true"`, include ancestor intents |

**Response (200):** Intent tree structure (`TopologySnapshot` — a list of
`TopologyStep`).

Each step carries server-derived **`spec_kind`** and **`spec_name`** when it is
the instantiation of a named network spec — a service applied, an
IP-VPN/MAC-VPN bound, a QoS policy bound, or a service-derived ACL:

`spec_name` is the spec's **canonical** identity (`NormalizeName` — upper-case,
`-`→`_`), so it equals the `GET /services` / `/ipvpns` / … key **exactly**,
regardless of the casing the operator typed at apply time. A client matches
`spec_name` against the spec list key with no transformation.

| `spec_kind` | `spec_name` | from |
|---|---|---|
| `service` | canonical service name (e.g. `TRANSIT`) | `apply-service` / `deploy-service` |
| `ipvpn` | canonical IP-VPN name (e.g. `IRB`) | `bind-ipvpn` |
| `macvpn` | canonical MAC-VPN name | `bind-macvpn` |
| `qos` | canonical QoS policy name | `bind-qos` |
| `filter` | canonical source filter name (e.g. `MGMT_IN`) | service-derived `create-acl` |

Both are omitted for primitives (device/VLAN/VRF) and for a standalone/raw
`create-acl` (no source filter spec). A service-derived ACL is content-hash-named
(§24/§25), so newtron records the source filter name on the step rather than
reversing the hash. Route-policy and prefix-list never appear as standalone
steps — they are sub-resources of a service application, so the enclosing
`service` step already carries their provenance. The fields
are **server-derived at serve time** (re-computed each request, never stale)
and are **not** persisted to `topology.json`. A client reads them to map intent
→ spec — e.g. "where is service `TRANSIT` applied across the topology?" —
without re-implementing newtron's per-operation derivation.

---

## 8. Node Write Operations

These endpoints modify device CONFIG_DB. Most use the `connectAndExecute` pattern:
connect -> Lock (refresh) -> fn (build ChangeSet) -> Commit -> Save -> Unlock. They
accept `dry_run` and `no_save` query parameters.

Write operations return `WriteResult` (see [S13](#writeresult)) on success, which
reports the change count, whether changes were applied, verified, and saved.

### Setup Device

#### POST /newtron/v1/networks/{netID}/nodes/{device}/setup-device

Unified baseline setup that configures device metadata, loopback interface, BGP
globals, VTEP (optional), and route reflector (optional) in a single operation.
This replaces the former individual endpoints (`configure-bgp`, `configure-loopback`,
`setup-evpn`, `set-metadata`, `configure-route-reflector`).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `fields` | object | no | Device metadata fields (e.g., `{"hostname": "switch1"}`) |
| `source_ip` | string | no | VTEP source IP (empty = skip VTEP setup) |
| `route_reflector` | object | no | Route reflector config (null = skip RR setup) |

The `route_reflector` object:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `cluster_id` | string | yes | RR cluster ID |
| `local_asn` | integer | yes | RR's own ASN |
| `router_id` | string | yes | RR's router ID |
| `local_addr` | string | yes | Local address for eBGP multihop (loopback IP) |
| `clients` | array | no | RR clients |
| `peers` | array | no | RR-to-RR peers |

**Response (201):** `WriteResult`

**Example:**

```
POST /newtron/v1/networks/default/node/switch1/setup-device
{
  "fields": {"hostname": "switch1"},
  "source_ip": "10.0.0.1"
}
```

### VLANs

#### POST /newtron/v1/networks/{netID}/nodes/{device}/create-vlan

Create a VLAN on the device.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer | yes | VLAN ID (1-4094) |
| `description` | string | no | VLAN description |

**Response (201):** `WriteResult`

**Example:**

```
POST /newtron/v1/networks/default/node/switch1/create-vlan?dry_run=true
{"id": 100, "description": "Customer VLAN"}
```

#### POST /newtron/v1/networks/{netID}/nodes/{device}/delete-vlan

Delete a VLAN and all its members.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer | yes | VLAN ID to delete |

**Response (200):** `WriteResult`

### IRB (SVI)

#### POST /newtron/v1/networks/{netID}/nodes/{device}/configure-irb

Configure an IRB interface (SVI) -- creates the Vlan*N* interface with optional
VRF binding, IP address, and anycast MAC.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID for the SVI |
| `vrf` | string | no | VRF to bind the SVI to |
| `ip_address` | string | no | IP address in CIDR (e.g., `"10.1.1.1/24"`) |
| `anycast_mac` | string | no | SAG anycast MAC address |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/unconfigure-irb

Remove an IRB interface (SVI) configuration.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID of the SVI to remove |

**Response (200):** `WriteResult`

### VRFs

#### POST /newtron/v1/networks/{netID}/nodes/{device}/create-vrf

Create a VRF.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | VRF name |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/delete-vrf

Delete a VRF and clean up all associated resources (interfaces, routes, VNI
mappings).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | VRF name to delete |

**Response (200):** `WriteResult`

### IP-VPN Binding

The IP-VPN name is a normal, canonicalized spec name; the on-device SONiC VRF name is **derived** from it, read-only, as `"Vrf_"+name` (e.g. IP-VPN `IRB` → VRF `Vrf_IRB`). `sonic-vrf.yang` requires VRF names to start with `Vrf` (mixed case, per RCA-044) — the derivation supplies that prefix, so operators never author it. The derived name is surfaced read-only on the IP-VPN view as `vrf_name` (the IP-VPN schema exposes it as a computed `vrf_name` field). Operators must `vrf create <Vrf_Name>` (the derived VRF name) before `bind-ipvpn` (the VRF is the precondition; bind-ipvpn overlays L3VNI + EVPN config on top).

#### POST /newtron/v1/networks/{netID}/nodes/{device}/bind-ipvpn

Bind an IP-VPN on this device (sets up L3VNI, route targets, EVPN VNI configuration).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ipvpn` | string | yes | IP-VPN spec name (VRF name is derived as `"Vrf_"+name`) |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/unbind-ipvpn

Unbind the IP-VPN on this device (tears down L3VNI infrastructure).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ipvpn` | string | yes | IP-VPN spec name (VRF name is derived as `"Vrf_"+name`) |

**Response (200):** `WriteResult`

### MAC-VPN Binding (Node-Level)

#### POST /newtron/v1/networks/{netID}/nodes/{device}/bind-macvpn

Bind a MAC-VPN to a VLAN at the node level (maps VLAN to L2VNI).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID |
| `macvpn` | string | yes | MAC-VPN spec name (carries the L2VNI; resolved from the spec at apply time) |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/unbind-macvpn

Unbind the MAC-VPN from a VLAN at the node level.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID |

**Response (200):** `WriteResult`

### Static Routes

#### POST /newtron/v1/networks/{netID}/nodes/{device}/add-static-route

Add a static route.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name (use `"default"` for global) |
| `prefix` | string | yes | Destination prefix (e.g., `"0.0.0.0/0"`) |
| `nexthop` | string | yes | Next-hop IP address |
| `metric` | integer | no | Route metric (default 0) |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/update-static-route

Atomically update a static route's fields (nexthop, metric) under the
per-device intent lock. Closes the forwarding black hole that
remove-static-route + add-static-route exposes today (traffic destined
to the prefix has no next-hop during the DEL → ADD window). Issue #227.

The composite key `(vrf, prefix)` identifies the row; per §47, this
verb mutates fields only. To change the prefix, use remove-static-route
+ add-static-route — that's the protocol-honest path for a different
route at a different prefix.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |
| `prefix` | string | yes | Existing route prefix |
| `nexthop` | string | yes | New next-hop IP |
| `metric` | integer | no | Route metric/distance |

**Behaviors:**

- 404 if no route exists at `(vrf, prefix)`.

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/remove-static-route

Remove a static route.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |
| `prefix` | string | yes | Route prefix to remove |

**Response (200):** `WriteResult`

### ACLs

#### POST /newtron/v1/networks/{netID}/nodes/{device}/create-acl

Create an ACL table.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | ACL table name |
| `type` | string | yes | ACL type (e.g., `"L3"`, `"L3V6"`, `"MIRROR"`) |
| `stage` | string | yes | `"ingress"` or `"egress"` |
| `ports` | string | no | Comma-separated interface list |
| `description` | string | no | Description |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/delete-acl

Delete an ACL table and all its rules.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | ACL table name to delete |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/add-acl-rule

Add a rule to an ACL table.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `rule_name` | string | yes | Rule name (e.g., `"RULE_10"`) |
| `priority` | integer | yes | Rule priority (higher = matched first) |
| `action` | string | yes | `"FORWARD"` or `"DROP"` |
| `src_ip` | string | no | Source IP/prefix |
| `dst_ip` | string | no | Destination IP/prefix |
| `protocol` | string | no | IP protocol |
| `src_port` | string | no | Source port |
| `dst_port` | string | no | Destination port |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/update-acl-rule

Atomically update an ACL rule's fields under the per-device intent
lock. Closes the packet-leak window remove + add exposes today: the
prior `ACL_RULE` entry and the new one are written in a single
ChangeSet, so any traffic hitting the port during the mutation matches
either the old rule or the new rule — never neither. Issue #227.

The composite key `(table, rule_name)` identifies the row; per §47,
this verb mutates fields only. Note that **PRIORITY is a field, not a
handle**: CONFIG_DB allows two ACL_RULE rows in the same table with
the same PRIORITY (different `rule_name`s). To change the rule's name,
use remove-acl-rule + add-acl-rule.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `rule_name` | string | yes | Existing rule to update |
| `priority` | integer | yes | New priority |
| `action` | string | yes | `"FORWARD"` or `"DROP"` |
| `src_ip` | string | no | Source IP/prefix |
| `dst_ip` | string | no | Destination IP/prefix |
| `protocol` | string | no | IP protocol |
| `src_port` | string | no | Source port |
| `dst_port` | string | no | Destination port |

**Behaviors:**

- 404 if `rule_name` doesn't exist in the ACL table.

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/remove-acl-rule

Remove a rule from an ACL table.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `rule` | string | yes | Rule name to remove |

**Response (200):** `WriteResult`

### PortChannels

#### POST /newtron/v1/networks/{netID}/nodes/{device}/create-portchannel

Create a PortChannel (LAG).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | PortChannel name (e.g., `"PortChannel1"`) |
| `members` | string[] | no | Initial member interfaces |
| `min_links` | integer | no | Minimum links for the LAG to be up |
| `fast_rate` | boolean | no | LACP fast rate |
| `fallback` | boolean | no | LACP fallback |
| `mtu` | integer | no | MTU |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/delete-portchannel

Delete a PortChannel and remove all members.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | PortChannel name to delete |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/add-portchannel-member

Add an interface to a PortChannel.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `portchannel` | string | yes | PortChannel name |
| `interface` | string | yes | Interface name |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/remove-portchannel-member

Remove an interface from a PortChannel.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `portchannel` | string | yes | PortChannel name |
| `interface` | string | yes | Interface name |

**Response (200):** `WriteResult`

### BGP EVPN Peers

#### POST /newtron/v1/networks/{netID}/nodes/{device}/add-bgp-evpn-peer

Add a BGP EVPN overlay peer. These are loopback-to-loopback eBGP sessions for
L2VPN EVPN address family exchange.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `neighbor_ip` | string | yes | Neighbor IP address (loopback) |
| `remote_as` | integer | yes | Remote AS number |
| `description` | string | no | Neighbor description |
| `multihop` | integer | no | eBGP multihop TTL |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/update-bgp-evpn-peer

Atomically update an existing BGP EVPN overlay peer under the per-device
intent lock. Closes the EVPN session blip that remove-bgp-evpn-peer +
add-bgp-evpn-peer exposes today: EVPN session drop triggers MAC withdraw
across the fabric and forces a full route re-exchange after
re-establishment. Issue #227.

The composite key `(default, neighbor_ip)` identifies the row; per §47,
this verb mutates fields only. To change the BGP destination IP, use
remove-bgp-evpn-peer + add-bgp-evpn-peer — that's peering with a
different real-world endpoint. 404 if no peer exists at `neighbor_ip`.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `neighbor_ip` | string | yes | Existing peer's neighbor IP |
| `remote_as` | integer | yes | New remote AS |
| `description` | string | no | New description |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/remove-bgp-evpn-peer

Remove a BGP EVPN overlay peer.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ip` | string | yes | Neighbor IP address to remove |

**Response (200):** `WriteResult`

### QoS at the node level (substrate-only annotation)

Newtron does NOT expose node-level `POST /nodes/{device}/bind-qos` or
`POST /nodes/{device}/unbind-qos` endpoints. QoS bind/unbind is an
interface-scoped operation (per `DESIGN_PRINCIPLES_NEWTRON.md` §6: "The
interface is the point of service delivery, unit of lifecycle"). The
wired endpoints are:

- `POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/bind-qos`
- `POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/unbind-qos`

See §QoS Bindings (Interface-Level) below for the canonical interfaces.

---

## 9. Node Lifecycle Operations

These endpoints perform device-level lifecycle operations that don't follow the
standard ChangeSet model. Most use the `connectAndRead` pattern (connect + refresh,
no Lock/Commit/Save cycle) because they execute CLI commands directly or perform
special-purpose operations. They do not accept `dry_run`/`no_save`.

Most lifecycle operations return null data on success:

```json
{"data": null}
```

Exception: `ssh-command` returns `SSHCommandResponse`.

For post-facto re-verification ("is the projection currently actualized on the
device?"), use `GET /intent/drift` — empty drift ≡ all newtron writes are
present in CONFIG_DB. Drift is the canonical "intent vs reality" diff
(`DriftEntry` vocab, §11); per-write verification (`VerificationError` vocab)
is reported inline on the originating write via `WriteResult.Verification` or
the 409 envelope of `VerificationFailedError`.

### POST /newtron/v1/networks/{netID}/nodes/{device}/reload-config

Trigger a SONiC config reload on the device (`config reload -y`). This reloads
CONFIG_DB from `/etc/sonic/config_db.json` and restarts all SONiC services.

**Request body:** none

**Response (200):** `null` data on success

### POST /newtron/v1/networks/{netID}/nodes/{device}/save-config

Save the running CONFIG_DB to `/etc/sonic/config_db.json` (`config save -y`).

**Request body:** none

**Response (200):** `null` data on success

### POST /newtron/v1/networks/{netID}/nodes/{device}/restart-daemon

Restart a SONiC daemon on the device (`systemctl restart <daemon>`).

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `daemon` | string | yes | Daemon name (e.g., `"bgp"`, `"swss"`) |

**Response (200):** `null` data on success

### POST /newtron/v1/networks/{netID}/nodes/{device}/ssh-command

Execute an arbitrary SSH command on the device and return the output.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Shell command to execute |

**Response (200):** `SSHCommandResponse`

```json
{"data": {"output": "SONiC Software Version: SONiC.202505..."}}
```

---

## 10. Node Diagnostics

These endpoints provide direct access to SONiC Redis databases for debugging and
inspection. They use `connectAndRead` -- no `dry_run`/`no_save`.

### GET /newtron/v1/networks/{netID}/nodes/{device}/configdb

Returns the device's actual CONFIG_DB state as a single internally-consistent
snapshot (`sonic.RawConfigDB` — `map[table]map[key]map[field]string`). One
round-trip per table, so consumers needing a full picture do not stitch
hundreds of per-key requests and lose internal consistency mid-read.

**Query parameters:**
- `owned_only` — `true` (default): return only newtron-owned tables; `false`:
  return every schema-known table on the device (superset, includes factory
  state and daemon-managed tables).

**Response (200):** `RawConfigDB` map. Tables with zero entries are omitted.

**Errors:** 500 when the device transport cannot connect.

_Lands newtron#17 (Cluster D — device-reality substrate, §46)._

### GET /newtron/v1/networks/{netID}/nodes/{device}/configdb/{table}

List all keys in a CONFIG_DB table.

**Path parameters:** `table` -- CONFIG_DB table name (e.g., `VLAN`, `BGP_GLOBALS`)

**Response (200):** Array of key strings

### GET /newtron/v1/networks/{netID}/nodes/{device}/configdb/{table}/{key}

Get all fields of a CONFIG_DB entry.

**Path parameters:** `table` -- table name, `key` -- entry key (e.g., `Vlan100`)

**Response (200):** Field map (`map[string]string`)

**Example:**

```
GET /newtron/v1/networks/default/node/switch1/configdb/VLAN/Vlan100
```

### GET /newtron/v1/networks/{netID}/nodes/{device}/configdb/{table}/{key}/exists

Check if a CONFIG_DB entry exists.

**Path parameters:** `table` -- table name, `key` -- entry key

**Response (200):**

```json
{"data": {"exists": true}}
```

### GET /newtron/v1/networks/{netID}/nodes/{device}/statedb/{table}/{key}

Get all fields of a STATE_DB entry.

**Path parameters:** `table` -- STATE_DB table name, `key` -- entry key

**Response (200):** Field map (`map[string]string`)

---

## 11. Intent Operations

These endpoints expose newtron's intent DAG — the canonical substrate that
records every operation newtron applied to a device. Intent records are
typed `NEWTRON_INTENT` rows in CONFIG_DB (`DESIGN_PRINCIPLES_NEWTRON.md`
§1 + §11); the projection is rebuilt from intent replay (§21).

### Substrate-only: intent records as a bulk list

Newtron does NOT expose a bulk `GET /nodes/{device}/intents` HTTP endpoint
that returns every `NEWTRON_INTENT` row. The substrate is reachable via two
typed substrate paths instead:

- `GET /nodes/{device}/intent/tree` returns the structured intent DAG with
  parent/child relationships (the operator-meaningful view).
- `GET /nodes/{device}/configdb/NEWTRON_INTENT` returns the raw CONFIG_DB
  table (the per-key generic substrate read).

The bulk-list endpoint as a separate route would be derivative of these two
typed primitives and a §46 violation (typed substrate exists and is already
exposed; a parallel "list everything" endpoint would summarize what's
already typed). Per `DESIGN_PRINCIPLES_NEWTRON.md` §21 (Reconstruct, Don't
Record): the intent DB is reconstructed by replay, not preserved as a flat
list. Consumers needing the flat list use `configdb/NEWTRON_INTENT`.

### Wired intent operations

#### GET /newtron/v1/networks/{netID}/nodes/{device}/intent/tree

Get a tree view of the intent DAG. See [S7 Intent Tree](#intent-tree) for query parameters.

#### GET /newtron/v1/networks/{netID}/nodes/{device}/intent/projection

Returns the per-table per-key per-field expected state derived from intent
replay (`sonic.RawConfigDB`). This is the typed projection representing
"what newtron believes this device should look like" — compare against
`/configdb` (device reality) to see drift.

**Query parameters:** `mode` (`topology`, `loopback`, or default `intent` /
actuated).

**Response (200):** `RawConfigDB` map. Empty when no intents exist on the
node.

**Errors:** 500 when actuated mode is requested and transport connection
fails.

_Lands newtron#5 (Cluster A — projection substrate, §46)._

#### POST /newtron/v1/networks/{netID}/nodes/{device}/intent/projection-diff

Returns the projection delta a hypothetical set of operations would produce
on top of the Node's current intent DB. Operations are applied in-memory
only; the Node's observable state (intent DB + projection) is restored before
the response. Workbench (`/api/workbench/{batch}/diff`) consumes this for
pre-commit previews — operationalizes operator-philosophy invariant #4 (show
before do) at the substrate level.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operations` | TopologyStep[] | yes | Operations to apply hypothetically. Same `TopologyStep` shape `/intent/save` consumes — `{ url, params }` per step. |

**Response (200):** `ProjectionDiffResult`:

| Field | Type | Description |
|-------|------|-------------|
| `before` | `RawConfigDB` | The projection bracketing the operations on the input side. |
| `after`  | `RawConfigDB` | The projection that would exist if the operations were applied. |
| `diff`   | `sonic.DriftEntry[]` | The entry-level delta, in the canonical §11 vocabulary. `extra` entries are adds; `missing` entries are deletes; `modified` entries are field-level changes. |

**Example:**

```
POST /newtron/v1/networks/default/nodes/switch1/intent/projection-diff
{
  "operations": [
    { "url": "/create-vlan", "params": { "vlan_id": 100 } }
  ]
}
```

```json
{
  "data": {
    "before": { ... },
    "after":  { "VLAN": { "Vlan100": { "vlanid": "100" } }, ... },
    "diff":   [
      { "table": "VLAN", "key": "Vlan100", "type": "extra",
        "actual": { "vlanid": "100" } }
    ]
  }
}
```

**Errors:** 400 invalid JSON or unknown step URL; 500 if rebuild fails.

_Lands newtron#4 (Cluster A — projection diff for Workbench pre-commit, §11 + §46)._

### Substrate-only: per-operation rollback and operation history

Newtron does NOT expose `GET /history`, `POST /rollback-history`,
`GET /zombie`, `POST /rollback-zombie`, `POST /clear-zombie`, or
`GET|PUT /settings`. These endpoints appeared in earlier drafts of this
document but were never implemented, and the substrate they would expose
isn't internally tracked either — there is no operation-history buffer,
no zombie-intent record, and no `NEWTRON_SETTINGS` device-level
configuration store.

The principled basis for not exposing them:

- **Operation history** — Per `DESIGN_PRINCIPLES_NEWTRON.md` §21
  ("Reconstruct, Don't Record"), newtron does not keep a temporal log
  of past operations. Intent records ARE the durable trace: the current
  set of `NEWTRON_INTENT` rows describes everything newtron has applied
  to the device that still applies. Reverse operations (§15) undo
  individual changes; there is no "rollback the last N operations" log.
- **Zombie intents** — Operations that fail mid-flight raise typed
  errors at the point of failure; partial CONFIG_DB writes are caught
  by `Verify` and reported via `VerificationFailedError` with the typed
  envelope (`docs/newtron/api.md` §Verification-failure response
  envelope; newtron#21). There is no separate zombie-record substrate.
- **Device settings** — The `NEWTRON_SETTINGS` table and `max_history`
  field that appeared in earlier `schema.go` drafts were never read by
  any code path and have been removed (see commit log around this
  audit). Device-level newtron behavior is derived from intent records
  + the device's profile, not from a mutable settings store.

Consumers needing per-operation rollback or partial-failure recovery
build it from substrate that IS exposed: the typed `device_ops[]` on
write responses (newtron#19 Option A — Phase 2a), the
`VerificationResult.Errors[]` with `DeviceResponse` field (Phase 1 +
envelope fix #21), and the reverse-operation half of §15 (every CRUD
verb has a reverse already; the operator composes them per task).

### Drift detection

Per-device drift detection is exposed via `GET /intent/drift` (under
the Intent operations group above; documented in §11 Wired intent
operations). There is no network-wide `/networks/{n}/drift` endpoint;
operators iterate over `/networks/{n}/topology/nodes` and call
`/intent/drift` per node.

### Device status — operator badge (issue #75A) {#device-status}

`GET /newtron/v1/networks/{netID}/nodes/{device}/status` produces the
cheap operator-facing badge data without warming an SSH session. Newtcon
polls this per device on a short timer to render `online / drift /
unsaved` indicators. Listed in the quick reference (§7) as `/status`.

Response shape (`NodeStatus`):

```
{
  "online": true,
  "online_reason": "ssh_port_resolved",
  "has_unsaved_intents": false,
  "intent_source": "intent",
  "intent_drift_count": 0
}
```

| Field | Meaning |
|-------|---------|
| `online` | `true` only when SSH port resolves AND a 500ms TCP probe to it succeeds. |
| `online_reason` | One of `ssh_port_resolved`, `newtlab_not_realised`, `port_closed`, `unreachable`, `no_resolver`. Browser UI dispatches on this string rather than parsing free-form errors. |
| `has_unsaved_intents` | True when the cached node has CRUD mutations not yet saved to topology.json. False when no node is cached. |
| `intent_source` | `intent` (built from device NEWTRON_INTENT), `topology` (built from topology.json), `loopback` (offline config testing), or `unloaded` (no cached node yet). Mirrors the `?mode=` enum (§1 Common Query Parameters). |
| `intent_drift_count` | Diff entries between cached projection and CONFIG_DB. Populated **only when the cached actor already has a live device connection** — otherwise `intent_drift_reason` explains why the count is `0` (typically `"not_connected"` or `"drift_query_failed"`). |

Cost: sub-second when the runtime is available (one resolver call + one
non-blocking TCP dial). The drift count adds at most one device-CONFIG_DB
scan when the cached connection is already open. Topology drift is **not**
in this payload — computing it would require a fresh SSH session inside
the actor lock. Use the dedicated endpoint below.

### Topology drift — on-demand (issue #75B) {#topology-drift}

`GET /newtron/v1/networks/{netID}/nodes/{device}/intent/topology-drift`
answers "does the device CONFIG_DB diverge from topology.json right
now?" — independent of the operator's in-flight in-memory edits. Listed
in the quick reference (§7) as `/intent/topology-drift`.

The handler builds a transient `TopologyNode` from topology.json, opens
its own SSH session, runs `Drift`, and closes. Distinct from
`/intent/drift`, which drifts against the cached in-memory projection
(which may include unsaved CRUD).

Strictly more expensive than `/status` ([§Device status](#device-status))
— one fresh SSH session per call. Invoke on demand from a "show topology
drift" UI action, not from a polling badge.

Response: `[]sonic.DriftEntry`, same shape as `/intent/drift`.

---

## 12. Interface Operations

These endpoints operate on a specific interface within a device. They are the
primary way to apply and manage services. All use `connectAndExecute` and accept
`dry_run`/`no_save` query parameters. Return `WriteResult` on success.

Interface names containing slashes must be URL-encoded: `Ethernet0%2F1` -> `Ethernet0/1`.

**Quick reference** -- all interface endpoints under `.../interfaces/{name}/`:

| Category | Endpoints | Key params |
|----------|-----------|------------|
| Service | `apply-service`, `remove-service`, `refresh-service` | `service`, `ip_address`, `vlan`, `peer_as` |
| Interface config | `configure-interface`, `unconfigure-interface`, `remove-trunk-vlan` | `vrf`, `ip`, `vlan_id`, `tagged` |
| ACL | `bind-acl`, `unbind-acl` | `acl`, `direction` |
| BGP | `add-bgp-peer`, `remove-bgp-peer` | `neighbor_ip`, `remote_as` |
| QoS | `bind-qos`, `unbind-qos` | `policy` |
| Port property | `set-property`, `clear-property` | `property`, `value` (set only) |

All endpoints use `POST` method.

### Service Lifecycle

The three core service operations: apply, remove, refresh. These are the most
frequently used endpoints in the API -- most network automation workflows center
on applying services to interfaces.

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/apply-service

Apply a service definition to the interface. Creates all required CONFIG_DB
infrastructure (VLANs, VRFs, VNI mappings, route policies, ACLs, QoS) based
on the service type.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `service` | string | yes | Service name from specs |
| `ip_address` | string | no | IP address for routed/IRB services (e.g., `"10.1.1.1/30"`) |
| `vlan` | integer | no | VLAN ID for local service types (`irb`, `bridged`) |
| `peer_as` | integer | no | BGP peer AS (for services with `routing.peer_as="request"`) |
| `params` | object | no | Additional parameters (e.g., `{"route_reflector_client": "true"}`) |

**Response (200):** `WriteResult`

**Example:**

```
POST /newtron/v1/networks/default/nodes/switch1/interfaces/Ethernet0/apply-service
{
  "service": "customer-l3",
  "ip_address": "10.1.1.1/30"
}
```

**Example response:**

```json
{
  "data": {
    "change_count": 12,
    "applied": true,
    "verified": true,
    "saved": true,
    "verification": {"passed": 12, "failed": 0}
  }
}
```

Fields with `omitempty` are absent (not null) when empty -- `preview` is absent
on non-dry-run, `errors` is absent when verification passes.

With `?dry_run=true`, changes are not applied and `preview` shows the diff:

```json
{
  "data": {
    "preview": "VLAN|Vlan100: SET {vlanid: 100}\nVLAN_MEMBER|Vlan100|Ethernet0: SET ...",
    "change_count": 12,
    "applied": false,
    "verified": false,
    "saved": false
  }
}
```

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/remove-service

Remove the service binding from the interface. Tears down all CONFIG_DB
infrastructure that was created by `apply-service`, using the stored binding
(not the current spec) to determine what to remove.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/refresh-service

Refresh the service binding -- removes the current configuration and re-applies
from the current spec. Use after spec changes to update a running service
without manual remove+apply.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### Interface Configuration

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/configure-interface

Configure an interface in routed mode (VRF + IP), bridged access mode (single
VLAN, `tagged: false`), or bridged trunk mode (one tagged VLAN per call,
`tagged: true`). Routed and bridged are mutually exclusive.

**Trunk additivity (#224)**: each call with `tagged: true` adds one VLAN to
the trunk and creates a per-VLAN intent record at
`NEWTRON_INTENT|interface|{name}|trunk-vlan|{vlan_id}`. Repeated calls for
different VLANs accumulate — the second call does not clobber the first.
Repeating the same VLAN is an idempotent no-op. Access mode (`tagged:
false`) stays singleton on the base `interface|{name}` record. This change
restores Intent Round-Trip Completeness for trunk ports: replay of the
intent log reconstructs the full trunk-membership set.

**Cross-mode swaps are rejected.** Calling configure-interface with a
different mode than the existing intent (routed → access, access vlan N
→ access vlan M, access → routed, routed vrf X → routed vrf Y) returns
500 with `writeIntent ...: parents mismatch (existing [<old>], requested
[<new>]) — delete and recreate to change parents`. Call
`unconfigure-interface` first to clear the existing mode, then
configure-interface for the new one. The check is at the intent DAG
parents-mismatch guard — the interface record's `_parents` encodes mode
(`vrf|<vrf>` for routed, `vlan|<id>` for access, `device` for empty).

**Within-mode field changes (#228)**: when the new call keeps the same
parents but changes a sub-entry-owning field (today: the IP in routed
mode), the prior CONFIG_DB sub-entry for that field is deleted before
the new one is written. The intent record's params are also fully
replaced (DEL+HSET semantics) so dropped fields don't survive as ghost
data. Concretely:

- Routed IP swap (`{vrf:X, ip:A}` → `{vrf:X, ip:B}`): `INTERFACE|<name>|A`
  is deleted; `INTERFACE|<name>|B` is added; intent's `ip` reflects B.
- Routed IP drop (`{vrf:X, ip:A}` → `{vrf:X}`): `INTERFACE|<name>|A` is
  deleted; intent's `ip` field is cleared (not merged stale).
- Routed IP add (`{vrf:X}` → `{vrf:X, ip:A}`): `INTERFACE|<name>|A` is
  added; no spurious delete.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | no | VRF binding (routed mode) |
| `ip` | string | no | IP address in CIDR (routed mode) |
| `vlan_id` | integer | no | VLAN ID (bridged mode) |
| `tagged` | boolean | no | Tagged trunk membership (bridged mode). `false` = access; `true` = additive trunk |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/remove-trunk-vlan

Atomically strip a single tagged VLAN from a trunk port. The named VLAN's
`VLAN_MEMBER` entry and its `interface|{name}|trunk-vlan|{vlan_id}` intent
record are deleted; other trunk VLANs, the access VLAN (if any), VRF/IP
bindings, BGP peers, QoS bindings, and ACL bindings on this interface are
untouched.

Reverse mirror of `configure-interface` with `tagged: true` per §15 —
closes the gap where `unconfigure-interface` (full-teardown) was the only
removal path. Issue #224.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | The trunk VLAN to remove |

**Behaviors:**

- 404 if the interface is not a trunk member of the specified VLAN.
- 400 if `vlan_id` is missing or non-positive.
- Atomic — under the per-device intent lock.

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/unconfigure-interface

Remove all configuration from an interface (VRF binding, IP addresses, access
VLAN, all trunk VLAN memberships, BGP peers, QoS, ACL bindings, property
overrides). Returns the interface to its unconfigured state.

For removing one trunk VLAN without affecting the rest of the port, use
`remove-trunk-vlan` instead (issue #224).

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### ACL Binding

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/bind-acl

Bind an ACL to the interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `direction` | string | yes | `"ingress"` or `"egress"` |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/unbind-acl

Unbind an ACL from the interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name to unbind |

**Response (200):** `WriteResult`

### MAC-VPN Binding (substrate-only annotation)

MAC-VPN binding (mapping a VLAN to an L2VNI) is a **node-level** operation,
not an interface-level one — MAC-VPN entries pin to the device's VLAN
state rather than to a specific interface. The wired endpoints are:

- `POST /newtron/v1/networks/{netID}/nodes/{device}/bind-macvpn` — see §Node-level
  Service Composition above.
- `POST /newtron/v1/networks/{netID}/nodes/{device}/unbind-macvpn` — same.

The earlier `/interfaces/{name}/bind-macvpn` and `/interfaces/{name}/unbind-macvpn`
paths in this document were never implemented.

### BGP Peer

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/add-bgp-peer

Add a BGP peer scoped to this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `neighbor_ip` | string | no | Neighbor IP address |
| `remote_as` | integer | no | Remote AS number |
| `description` | string | no | Description |
| `multihop` | integer | no | eBGP multihop TTL |

**Response (201):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/update-bgp-peer

Atomically update the existing BGP peer on this interface under the
per-device intent lock. Closes the session-blip window that
remove-bgp-peer + add-bgp-peer exposes (BGP session tears down and
re-establishes during the gap). Issue #227.

The composite key `(vrf, neighbor_ip)` identifies the row; per §47,
this verb mutates fields only. The intent record is read to recover
the current neighbor IP — operators do not need to pass it. To change
the BGP destination IP, use remove-bgp-peer + add-bgp-peer.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `neighbor_ip` | string | no | Current neighbor IP (informational; the existing peer is read from intent) |
| `remote_as` | integer | yes | New remote AS number |
| `description` | string | no | New description |
| `multihop` | integer | no | New eBGP multihop TTL |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/remove-bgp-peer

Remove the BGP peer from this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### QoS

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/bind-qos

Bind a QoS policy to this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | QoS policy name from specs |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/unbind-qos

Unbind the QoS policy from this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### Port Property

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/set-property

Set a property on the interface (e.g., `mtu`, `admin_status`, `speed`).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `property` | string | yes | Property name (e.g., `"mtu"`, `"admin_status"`) |
| `value` | string | yes | Property value |

**Response (200):** `WriteResult`

#### POST /newtron/v1/networks/{netID}/nodes/{device}/interfaces/{name}/clear-property

Clear a previously-set property on the interface (reverse of `set-property`).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `property` | string | yes | Property name to clear (e.g., `"mtu"`, `"admin_status"`) |

**Response (200):** `WriteResult`

---

## 13. Types Reference

All request and response types used across the API, grouped by domain. Types are
defined in `pkg/newtron/types.go` (public API) and `pkg/newtron/api/types.go`
(HTTP layer).

### Write Result Types

These types are returned by all device write operations (S8, S13).

#### WriteResult

| Field | Type | Description |
|-------|------|-------------|
| `preview` | string (optional) | Human-readable diff preview. Present only on dry-run; absent (not empty string) otherwise. |
| `changes` | ConfigChange[] (optional) | Typed ChangeSet entries — every CONFIG_DB add/modify/delete in this operation, in the same `sonic.ConfigChange` shape newtron uses internally. Each entry carries `fields` (the after-state) and, for a CONFIG_DB row, `from` (the before-state it overwrote or deleted — #236); `from` is omitted on a pure add and on `NEWTRON_INTENT`/`NEWTRON_HISTORY` rows. §46 canonical substrate. Absent when `change_count` is 0. |
| `device_ops` | DeviceOp[] (optional) | Per-operation outcomes recorded during Apply and Verify — one entry per Redis HSET/DEL and one verify_read entry per change. Operationalizes operator-philosophy invariant #1 (no black boxes) for the apply pipeline. Absent in loopback mode (no device transport). §11 + §46. See DeviceOp below. |
| `change_count` | integer | Number of CONFIG_DB changes |
| `applied` | boolean | Whether changes were committed to Redis |
| `verified` | boolean | Whether post-apply verification passed |
| `saved` | boolean | Whether `config save` was run |
| `verification` | VerificationResult (optional) | Detailed verification outcome. Absent (not null) on dry-run or when verification is skipped. |

#### VerificationResult

Inline detail of post-Apply verify. Returned on `WriteResult.Verification` when
verify ran, and as the `data` payload of a 409 envelope when `VerificationFailedError`
fired. Substrate vocabulary for per-write verify; broader "is intent currently
actualized?" questions use `DriftEntry` via `GET /intent/drift` (§11).

| Field | Type | Description |
|-------|------|-------------|
| `passed` | integer | Number of entries verified successfully |
| `failed` | integer | Number of entries that failed verification |
| `errors` | VerificationError[] (optional) | Details of each failure. Absent when all entries pass. |

#### VerificationError

| Field | Type | Description |
|-------|------|-------------|
| `table` | string | CONFIG_DB table name |
| `key` | string | Entry key |
| `field` | string | Field name |
| `expected` | string | Expected value |
| `actual` | string | Actual value (empty string if missing) |
| `device_response` | string (optional) | Verbatim device-side reply observed when the mismatch was detected. For field mismatches, the full HGETALL content as sorted `key=value` pairs; for missing-key or still-present cases, the Redis-level status. §46. |

#### DeviceOp

One record per Device I/O Operation newtron performed during Apply or Verify
— one Redis HSET, one Redis DEL, one daemon-settle wait, one verify re-read.
Per `docs/newtron/unified-pipeline-architecture.md` §7. Surfaced on
`WriteResult.device_ops` (200 path) and inside the typed envelope `data`
field of 409 responses to `VerificationFailedError`. Vocabulary matches the
newtcon contract verbatim.

| Field | Type | Description |
|-------|------|-------------|
| `seq` | integer | Zero-based ordinal within the per-target apply sequence. Monotonically increasing. |
| `kind` | string | Bounded enum: `redis_write`, `redis_delete`, `daemon_wait`, `verify_read`. |
| `table` | string (optional) | CONFIG_DB table the op acted on. Omitted for whole-pipeline `daemon_wait`. |
| `key` | string (optional) | CONFIG_DB entry key. |
| `fields` | map[string]string (optional) | Intended write content for `redis_write`; nil for `redis_delete` and `daemon_wait`; the expected content for `verify_read`. |
| `result` | string | Bounded enum: `applied`, `rejected`, `skipped`. |
| `device_response` | string (optional) | Verbatim device/Redis-level reply observed at execution time. For `applied` `redis_write`/`redis_delete`, the Redis-protocol integer reply (`"(integer) 1"` etc.). For `rejected` ops, the verbatim error. For `verify_read`, the HGETALL content sorted as `key=value` pairs (pass case) or the missing/present sentinel (fail case). |
| `at` | string (RFC3339 UTC) | Wall-clock timestamp the op completed at. |

**Per-Node atomicity** (DESIGN_PRINCIPLES_NEWTRON.md §13, §18): when
newtron's pipeline uses a Redis `TxPipeline` (currently `Reconcile`,
`ApplyDrift`), every `redis_write`/`redis_delete` op within a single
`EXEC` carries the same `result` — all `applied` or all `rejected`. The
per-change `ChangeSet.Apply` path (used by primitive and service
operations) applies writes individually, so per-op results may differ
when one write succeeds and a later one in the same ChangeSet fails.
The wire shape reflects whichever delivery mechanism produced the op.

### Verification-failure response envelope

Write endpoints that return `*newtron.VerificationFailedError` emit HTTP 409
Conflict with the standard envelope and the typed `*WriteResult` in `data`:

```json
{
  "error": "verification failed on switch1: 1/1 entries did not persist",
  "data": {
    "applied": true,
    "verified": false,
    "changes": [...],
    "device_ops": [...],
    "verification": {
      "passed": 0,
      "failed": 1,
      "errors": [
        { "table": "BGP_GLOBALS", "key": "default", "field": "local_asn",
          "expected": "65001", "actual": "99999",
          "device_response": "local_asn=99999 router_id=10.0.0.1" }
      ]
    }
  }
}
```

The substrate (`verification.errors[].device_response` + `device_ops`)
survives the error envelope — §46 (HTTP API Boundary) on the failure path.
Other error kinds emit only the `error` field; only
`VerificationFailedError` attaches structured `data`.

_Lands newtron#21 (companion to #19 Phase 2a — write-handler error envelope fix)._

### Device Info Types

#### DeviceInfo

Returned by `GET .../info`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Device name |
| `mgmt_ip` | string | Management IP address |
| `loopback_ip` | string | Loopback IP address |
| `platform` | string | Platform name |
| `zone` | string | Zone name |
| `bgp_as` | integer | BGP autonomous system number |
| `router_id` | string | BGP router ID |
| `vtep_source_ip` | string | VTEP source IP |
| `bgp_neighbors` | string[] | List of BGP neighbor addresses |
| `interfaces` | integer | Number of interfaces |
| `port_channels` | integer | Number of PortChannels |
| `vlans` | integer | Number of VLANs |
| `vrfs` | integer | Number of VRFs |

### Interface Types

#### InterfaceSummary

Returned in array by `GET .../interface`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Interface name |
| `admin_status` | string | `"up"` or `"down"` |
| `oper_status` | string | `"up"` or `"down"` |
| `ip_addresses` | string[] | IP addresses on the interface |
| `vrf` | string | VRF binding (empty if default) |
| `service` | string | Service name (empty if no binding) |

#### InterfaceDetail

Returned by `GET .../interface/{name}`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Interface name |
| `admin_status` | string | `"up"` or `"down"` |
| `oper_status` | string | `"up"` or `"down"` |
| `speed` | string | Port speed |
| `mtu` | integer | MTU |
| `ip_addresses` | string[] | IP addresses |
| `vrf` | string | VRF binding |
| `service` | string | Service binding name |
| `pc_member` | boolean | Whether this is a PortChannel member |
| `pc_parent` | string | Parent PortChannel name (if member) |
| `ingress_acl` | string | Ingress ACL name |
| `egress_acl` | string | Egress ACL name |
| `pc_members` | string[] | Member interfaces (if this is a PortChannel) |
| `vlan_members` | string[] | VLAN memberships |

#### ServiceBindingDetail

Returned by `GET .../interfaces/{name}/binding`.

| Field | Type | Description |
|-------|------|-------------|
| `service` | string | Service name |
| `ip_addresses` | string[] | IP addresses from the binding |
| `vrf` | string | VRF from the binding |

### VLAN Types

#### VLANStatusEntry

Returned by `GET .../vlan` and `GET .../vlans/{id}`.

| Field | Type | Description |
|-------|------|-------------|
| `id` | integer | VLAN ID |
| `name` | string | VLAN name (e.g., `"Vlan100"`) |
| `l2_vni` | integer | L2 VNI mapping (0 if none) |
| `svi` | string | SVI interface name (empty if no SVI) |
| `member_count` | integer | Number of member interfaces |
| `members` | string[] | Member interface names |
| `macvpn` | string | MAC-VPN binding name |
| `macvpn_detail` | VLANMACVPNDetail | MAC-VPN binding details |

#### VLANMACVPNDetail

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | MAC-VPN name |
| `l2_vni` | integer | L2 VNI |
| `arp_suppression` | boolean | ARP suppression enabled |

### VRF Types

#### VRFStatusEntry

Returned in array by `GET .../vrf`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | VRF name |
| `l3_vni` | integer | L3 VNI (0 if none) |
| `interfaces` | integer | Number of interfaces in the VRF |
| `state` | string | Operational state |

#### VRFDetail

Returned by `GET .../vrfs/{name}`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | VRF name |
| `l3_vni` | integer | L3 VNI (0 if none) |
| `interfaces` | string[] | Interface names in the VRF |
| `bgp_neighbors` | BGPNeighborEntry[] | BGP neighbors in the VRF |

#### BGPNeighborEntry

| Field | Type | Description |
|-------|------|-------------|
| `address` | string | Neighbor IP address |
| `asn` | string | Remote ASN |
| `description` | string | Description |

### ACL Types

#### ACLTableSummary

Returned in array by `GET .../acl`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | ACL table name |
| `type` | string | ACL type |
| `stage` | string | `"ingress"` or `"egress"` |
| `interfaces` | string | Bound interfaces (comma-separated) |
| `rule_count` | integer | Number of rules |

#### ACLTableDetail

Returned by `GET .../acls/{name}`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | ACL table name |
| `type` | string | ACL type |
| `stage` | string | Stage |
| `interfaces` | string | Bound interfaces |
| `description` | string | Description |
| `rules` | ACLRuleInfo[] | Rules in the table |

#### ACLRuleInfo

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Rule name |
| `priority` | string | Priority (as string) |
| `action` | string | `"FORWARD"` or `"DROP"` |
| `src_ip` | string | Source IP/prefix |
| `dst_ip` | string | Destination IP/prefix |
| `protocol` | string | IP protocol |
| `src_port` | string | Source port |
| `dst_port` | string | Destination port |

### BGP Types

#### BGPStatusResult

Returned by `GET .../bgp/status`, `GET .../bgp/check`, and `GET .../neighbors`.

| Field | Type | Description |
|-------|------|-------------|
| `local_as` | integer | Local AS number |
| `router_id` | string | Router ID |
| `loopback_ip` | string | Loopback IP |
| `neighbors` | BGPNeighborStatus[] | All BGP neighbors with state |
| `evpn_peers` | string[] | EVPN peer addresses |

#### BGPNeighborStatus

| Field | Type | Description |
|-------|------|-------------|
| `address` | string | Neighbor address |
| `vrf` | string | VRF (empty for default) |
| `type` | string | Neighbor type (e.g., `"underlay"`, `"overlay"`) |
| `remote_as` | string | Remote AS |
| `local_addr` | string | Local address |
| `admin_status` | string | Admin status |
| `description` | string | Description |
| `state` | string | Operational state (e.g., `"Established"`) |
| `pfx_rcvd` | string | Prefixes received |
| `pfx_sent` | string | Prefixes sent |
| `uptime` | string | Session uptime |

### EVPN Types

#### EVPNStatusResult

Returned by `GET .../evpn/status`.

| Field | Type | Description |
|-------|------|-------------|
| `vteps` | object | VTEP tunnel map (name -> source IP) |
| `nvos` | object | NVO map (name -> source VTEP) |
| `vni_mappings` | VNIMapping[] | VNI to VLAN/VRF mappings |
| `l3vni_vrfs` | L3VNIEntry[] | L3VNI to VRF mappings |
| `vtep_status` | string | VTEP operational status |
| `remote_vteps` | string[] | Discovered remote VTEP IPs |
| `vni_count` | integer | Total VNI count |

#### VNIMapping

| Field | Type | Description |
|-------|------|-------------|
| `vni` | string | VNI number |
| `type` | string | `"L2"` or `"L3"` |
| `resource` | string | Associated VLAN or VRF name |

#### L3VNIEntry

| Field | Type | Description |
|-------|------|-------------|
| `vrf` | string | VRF name |
| `l3vni` | integer | L3 VNI number |

### Health Types

#### HealthReport

Returned by `GET .../health`.

| Field | Type | Description |
|-------|------|-------------|
| `device` | string | Device name |
| `status` | string | `"healthy"`, `"degraded"`, or `"unhealthy"` |
| `config_check` | VerificationResult | CONFIG_DB verification result |
| `oper_checks` | HealthCheckResult[] | Operational health check results |

#### HealthCheckResult

| Field | Type | Description |
|-------|------|-------------|
| `check` | string | Check name (e.g., `"bgp"`, `"interface-oper"`) |
| `status` | string | `"pass"`, `"warn"`, or `"fail"` |
| `message` | string | Human-readable message |

### LAG Types

#### LAGStatusEntry

Returned by `GET .../lag` and `GET .../lags/{name}`.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | PortChannel name |
| `admin_status` | string | Admin status |
| `oper_status` | string | Operational status |
| `members` | string[] | Configured member interfaces |
| `active_members` | string[] | Active (LACP-up) members |
| `mtu` | integer | MTU |

### Route Types

#### RouteEntry

Returned by `GET .../routes/{vrf}/{prefix...}` and `GET .../routes-asic/{prefix...}`.

| Field | Type | Description |
|-------|------|-------------|
| `prefix` | string | Route prefix |
| `vrf` | string | VRF name |
| `protocol` | string | Protocol that installed the route |
| `next_hops` | RouteNextHop[] | Next-hop list |
| `source` | string | `"APP_DB"` or `"ASIC_DB"` |

#### RouteNextHop

| Field | Type | Description |
|-------|------|-------------|
| `address` | string | Next-hop IP address |
| `interface` | string | Egress interface |

### Host Types

#### HostProfile

Returned by `GET .../hosts/{name}`.

| Field | Type | Description |
|-------|------|-------------|
| `mgmt_ip` | string | Management IP address |
| `ssh_user` | string | SSH username |
| `ssh_pass` | string | SSH password |
| `ssh_port` | integer | SSH port |

### Spec Detail Types

These types are returned by the spec read endpoints in S4. They are the API's
view of spec objects -- they contain only the fields relevant to consumers, not
internal implementation details.

#### SpecInstance

Returned (as an array) by `GET .../spec-instances` -- the flat cross-scope spec
inventory. Each entry locates one spec definition in the network → zone → node
hierarchy; it is not the spec's content (read that via the per-kind show
endpoint).

| Field | Type | Description |
|-------|------|-------------|
| `kind` | string | Spec kind: `ServiceSpec`, `IPVPNSpec`, `MACVPNSpec`, `QoSPolicy`, `RoutePolicy`, `FilterSpec`, `PrefixListSpec` |
| `name` | string | Canonical spec name |
| `scope` | string | `network`, `zone`, or `node` |
| `scope_instance` | string | Zone or node name; empty for `network` scope |

#### ServiceDetail

Returned by `GET .../service` (array) and `GET .../services/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Service name |
| `description` | string | Description |
| `service_type` | string | One of: `routed`, `bridged`, `irb`, `evpn-routed`, `evpn-bridged`, `evpn-irb` |
| `ipvpn` | string | IP-VPN reference |
| `macvpn` | string | MAC-VPN reference |
| `vrf_type` | string | VRF type |
| `qos_policy` | string | QoS policy reference |
| `ingress_filter` | string | Ingress filter reference |
| `egress_filter` | string | Egress filter reference |

#### IPVPNDetail

Returned by `GET .../ipvpns` (array) and `GET .../ipvpns/{name}` (single). `name` is a normal, canonicalized spec name; `vrf_name` is the on-device SONiC VRF name, derived read-only as `"Vrf_"+name` (sonic-vrf.yang requires the `Vrf` prefix — RCA-044). `vrf_name` is never authored — it is computed and returned for display.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | IP-VPN spec name |
| `vrf_name` | string | Derived on-device SONiC VRF name (`"Vrf_"+name`), read-only |
| `description` | string | Description |
| `l3vni` | integer | L3 VNI |
| `route_targets` | string[] | Route targets |

#### MACVPNDetail

Returned by `GET .../macvpn` (array) and `GET .../macvpns/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | MAC-VPN name |
| `description` | string | Description |
| `anycast_ip` | string | Anycast gateway IP |
| `anycast_mac` | string | Anycast gateway MAC |
| `vni` | integer | L2 VNI |
| `vlan_id` | integer | Local VLAN ID |
| `route_targets` | string[] | Route targets |
| `arp_suppression` | boolean | ARP suppression enabled |

#### QoSPolicyDetail

Returned by `GET .../qos-policy` (array) and `GET .../qos-policies/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Policy name |
| `description` | string | Description |
| `queues` | QoSQueueEntry[] | Queue definitions |

#### QoSQueueEntry

| Field | Type | Description |
|-------|------|-------------|
| `queue_id` | integer | Queue number |
| `name` | string | Queue name |
| `type` | string | Queue type |
| `weight` | integer | WRR weight |
| `dscp` | integer[] | DSCP values |
| `ecn` | boolean | ECN enabled |

#### FilterDetail

Returned by `GET .../filter` (array) and `GET .../filters/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Filter name |
| `description` | string | Description |
| `type` | string | Filter type |
| `rules` | FilterRuleEntry[] | Rule list |

#### FilterRuleEntry

| Field | Type | Description |
|-------|------|-------------|
| `seq` | integer | Sequence number |
| `action` | string | `"permit"` or `"deny"` |
| `src_ip` | string | Source IP/prefix |
| `dst_ip` | string | Destination IP/prefix |
| `src_prefix_list` | string | Source prefix list |
| `dst_prefix_list` | string | Destination prefix list |
| `protocol` | string | IP protocol |
| `src_port` | string | Source port |
| `dst_port` | string | Destination port |
| `dscp` | string | DSCP value |
| `cos` | string | CoS value |
| `log` | boolean | Logging enabled |

#### PlatformDetail

Returned by `GET .../platforms` (map of platform name → PlatformDetail) and `GET .../platforms/{name}` (single).

The list endpoint is keyed by platform name rather than an array because platforms are referenced by name everywhere downstream (`profile.platform`, `topology.platform`). Other list endpoints (services, zones) return name arrays since their downstream references already arrive named at the call site.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Platform name |
| `hwsku` | string | SONiC HWSKU |
| `description` | string | Description |
| `device_type` | string | Device type |
| `dataplane` | string | Dataplane type |
| `default_speed` | string | Default port speed |
| `port_count` | integer | Number of ports |
| `breakouts` | string[] | Supported breakout modes |
| `unsupported_features` | string[] | Features not supported on this platform |

#### NodeSpec

Returned by `GET .../profile` (array of names) and `GET .../nodes/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Profile name |
| `mgmt_ip` | string | Management IP address |
| `loopback_ip` | string | Loopback IP address |
| `zone` | string | Zone name |
| `platform` | string | Platform name |
| `mac` | string | MAC address |
| `underlay_asn` | integer | BGP underlay AS number |
| `ssh_user` | string | SSH username |
| `ssh_port` | integer | SSH port |
| `evpn` | object | EVPN config: `peers` (string[]), `route_reflector` (bool), `cluster_id` (string) |

#### ZoneDetail

Returned by `GET .../zone` (array of names) and `GET .../zones/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Zone name |

#### AuthorizationDetail

Returned by `GET /newtron/v1/networks/{netID}/authorization` — the
network's authorization table as `network.json` carries it.

| Field | Type | Description |
|-------|------|-------------|
| `user_groups` | `{[group: string]: string[]}` | Group name → usernames. |
| `permissions` | `{[permission: string]: PermissionGrant[]}` | Permission key → grants. Grant entries serialize as bare strings (`["group", ...]`) when no `where` clause is set, as objects (`[{"groups": [...], "where": {...}}]`) when one is — same wire form an operator authors in `network.json`. |
| `super_users` | `string[]` | Usernames that bypass every permission check. |

### SSH Command Types

#### SSHCommandResponse

Returned by `POST .../ssh-command`.

| Field | Type | Description |
|-------|------|-------------|
| `output` | string | Command output text |

### Network Registration Types

#### NetworkInfo

Returned in array by `GET /newtron/v1/networks`.

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Network identifier |
| `dir` | string | Spec directory path |
| `has_topology` | boolean | Whether a topology file was loaded |
| `nodes` | string[] | Device names from topology |

---

### Audit Types

#### AuditEvent

Returned by `GET .../audit/events` (in `AuditEventPage.events`, lean) and
`GET .../audit/events/{id}` (full, with `request_body`).

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Hash-chain event ID (L6). Use as `{eventID}` on the detail endpoint. |
| `timestamp` | RFC3339 string | When the event was recorded. |
| `user` | string | Caller identity. Empty when the request was anonymous (see `verification_source`). |
| `verification_source` | string (optional) | How `user` was established: `pam`, `session_key`, `service_cert_cn`, `unix_peer_creds` (verified); `self_attested_header` (unverified); or `anonymous` (no identity presented — the server accepted the request in permissive mode). An empty `user` paired with `anonymous` is an expected permissive-mode record, **not** missing data. Absent only on synthetic/pre-feature entries. |
| `device` | string | Target device, when the operation was device-scoped. |
| `operation` | string | HTTP method + path of the mutation. |
| `service` | string (optional) | Service name, when the operation was service-scoped. |
| `interface` | string (optional) | Interface name, when the operation was interface-scoped. |
| `changes` | AuditChange[] | CONFIG_DB / intent rows the operation added, removed, or updated. Empty for spec-authoring and read/no-op operations. Present on both list and detail. |
| `request_body` | raw JSON (optional) | The JSON the caller submitted, with secret-bearing fields redacted to `***redacted***` (`${secret:KEY}` references preserved). **Detail endpoint only** — omitted from list rows. |
| `success` | boolean | Whether the operation succeeded (HTTP 2xx/3xx). |
| `error` | string (optional) | When `success` is false, the **underlying failure reason** — the same message the live error envelope returned to the caller (e.g. *"l3vni must be an integer in 1..16777215"*, a referential-conflict message). Falls back to the HTTP status text (*"Bad Request"*) only when the response carried no message. |
| `execute_mode` | boolean | Whether the operation ran in execute (`-x`) mode. |
| `dry_run` | boolean | Whether the operation was a dry run. |
| `duration` | string | Server-side handling duration. |
| `client_ip` | string (optional) | Remote address of the caller. |
| `session_id` | string (optional) | Session key ID, when the caller used a session (L2c). |

#### AuditChange

One CONFIG_DB / intent change within an `AuditEvent` — the audit-log
projection of `sonic.ConfigChange`.

| Field | Type | Description |
|-------|------|-------------|
| `table` | string | CONFIG_DB table name. |
| `key` | string | Row key within the table. |
| `type` | string | `add`, `modify`, or `delete`. |
| `fields` | map[string]string (optional) | The **after** state — field values for an `add`/`modify`; absent for a `delete`. |
| `from` | map[string]string (optional) | The **before** state — field values this change overwrote or deleted, for undo composition (#236). Omitted on a pure `add` (nothing was there) and on `NEWTRON_INTENT`/`NEWTRON_HISTORY` rows (newtron's substrate, reversed by replaying the inverse operation, not raw writes). For a `delete`, `from` holds the deleted fields; for a `modify`, the prior fields. |

---

## 14. Error Reference

### Error Response Format

All errors return the `APIResponse` envelope with the `error` field:

```json
{"error": "network 'prod' not registered"}
```

### Error Types and Status Codes

| Error Type | HTTP Status | Example Message |
|-----------|-------------|-----------------|
| Network not registered | 404 | `"network 'prod' not registered"` |
| Network already registered | 409 | `"network 'prod' already registered"` |
| Resource not found | 404 | `"service 'foo' not found"` |
| Validation error | 400 | `"validation error: id: invalid VLAN ID"` |
| Verification failed | 409 | `"verification failed on switch1: 3/42 entries did not persist"` |
| Context timeout | 504 | `"context deadline exceeded"` |
| Internal error | 500 | `"loading network from /etc/newtron: open network.json: no such file"` |

### Common Error Scenarios

**400 Bad Request:**
- Missing required field (`"service"` in apply-service, `"command"` in ssh-command)
- Invalid JSON body
- Invalid integer path parameter (VLAN ID, queue ID, sequence number)
- Invalid duration format in `?timeout=` parameter

**404 Not Found:**
- Network ID not registered
- Device not in topology
- Interface not found on device
- VRF/VLAN/ACL/service/spec not found

**409 Conflict:**
- Registering a network ID that already exists
- Composite verification finds entries that did not persist

**500 Internal Server Error:**
- SSH connection failure
- Redis command failure
- Spec directory parsing error
- Composite handle expired or not found

**504 Gateway Timeout:**
- Device unreachable (SSH connect timeout)
- Operation exceeds 5-minute request timeout

---

## 15. Server Configuration

### Binary Flags

The `newtron-server` binary accepts these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:18080` | Listen address (host:port) |
| `-spec-dir` | `""` | Spec directory to auto-register as a network at startup |
| `-net-id` | `"default"` | Network ID for the auto-registered network directory |
| `-idle-timeout` | `0` (5m default) | SSH connection idle timeout. `0` = default (5 minutes). Negative = disable caching (connect per request). |

**Example:**

```bash
newtron-server -addr :9090 -spec-dir /etc/newtron -net-id prod -idle-timeout 10m
```

### HTTP Server Timeouts

| Timeout | Value | Description |
|---------|-------|-------------|
| Read | 30 seconds | Maximum time to read the request (headers + body) |
| Write | 5 minutes | Maximum time to write the response |
| Idle | 120 seconds | Keep-alive connection idle timeout |
| Request | 5 minutes | Middleware-enforced per-request timeout |

### SSH Connection Caching

The server uses an actor model to manage device connections:

- Each registered network gets a **networkEntity**: a per-network registration record
  that owns the engine `*Network` and a NodeActor cache. It is not an actor — it
  holds no goroutine and no spec lock. Spec atomicity lives in the engine layer via
  per-key locks (`keyNetworkSpec`, `keyTopology`, `keyNodes`).
- Each device gets a **NodeActor** (created on first access) that serializes
  device operations and caches the SSH connection.
- The SSH tunnel is reused across requests. Each request still refreshes CONFIG_DB
  from Redis before operating.
- After `idle-timeout` of inactivity, the SSH connection is automatically closed.
- The next request to that device transparently re-establishes the connection.

### Graceful Shutdown

On SIGINT or SIGTERM, the server:
1. Stops accepting new connections
2. Closes all cached SSH connections (stops all NodeActors)
3. Waits up to 10 seconds for in-flight requests to complete
4. Exits
