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
| POST | `/network` | Register a network |
| GET | `/network` | List networks |
| POST | `/network/{n}/unregister` | Unregister a network |
| POST | `/network/{n}/reload` | Reload specs from disk |
| GET | `/network/{n}/service` | List services (also: `/ipvpn`, `/macvpn`, `/qos-policy`, `/filter`, `/platform`, `/route-policy`, `/prefix-list`) |
| GET | `/network/{n}/service/{name}` | Show service (also: ipvpn, macvpn, qos-policy, filter, platform, route-policy, prefix-list) |
| GET | `/network/{n}/service/{name}/projection` | Per-Node projection slices the service contributes (replay-diff) |
| GET | `/network/{n}/profile` | List device profile names |
| GET | `/network/{n}/profile/{name}` | Show device profile |
| GET | `/network/{n}/zone` | List zone names |
| GET | `/network/{n}/zone/{name}` | Show zone |
| GET | `/network/{n}/topology` | Full topology spec (devices, links, metadata) |
| GET | `/network/{n}/topology/node` | List topology device names |
| GET | `/network/{n}/host/{name}` | Get host profile |
| GET | `/network/{n}/feature` | List features (also: `/{name}/dependency`, `/{name}/unsupported-due-to`) |
| GET | `/network/{n}/platform/{name}/supports/{feature}` | Check platform feature support |
| POST | `/network/{n}/create-service` | Create service (also: create-ipvpn, create-macvpn, etc.) |
| POST | `/network/{n}/delete-service` | Delete service (also: delete-ipvpn, delete-macvpn, etc.) |
| POST | `/network/{n}/create-profile` | Create device profile |
| POST | `/network/{n}/delete-profile` | Delete device profile |
| POST | `/network/{n}/create-zone` | Create zone |
| POST | `/network/{n}/delete-zone` | Delete zone |
| POST | `/network/{n}/add-qos-queue` | Add queue to QoS policy |
| POST | `/network/{n}/remove-qos-queue` | Remove queue from QoS policy |
| POST | `/network/{n}/add-filter-rule` | Add rule to filter |
| POST | `/network/{n}/remove-filter-rule` | Remove rule from filter |
| POST | `/network/{n}/add-prefix-list-entry` | Add entry to prefix list |
| POST | `/network/{n}/remove-prefix-list-entry` | Remove entry from prefix list |
| POST | `/network/{n}/add-route-policy-rule` | Add rule to route policy |
| POST | `/network/{n}/remove-route-policy-rule` | Remove rule from route policy |

**Provisioning** (S6)

| Method | Path | What it does |
|--------|------|--------------|
| POST | `/network/{n}/node/{d}/init-device` | Initialize device (clean factory config) |

Spec-to-device delivery is via `POST /newtron/v1/network/{n}/node/{d}/intent/reconcile?mode=topology` (S11).

**Device Reads** (S7) -- all `GET /newtron/v1/network/{n}/node/{d}/...`

| Path suffix | Returns |
|-------------|---------|
| `/info` | Device overview |
| `/interface` | Interface list |
| `/interface/{i}` | Interface detail |
| `/interface/{i}/binding` | Service binding |
| `/vlan` | VLAN list |
| `/vlan/{id}` | VLAN detail |
| `/vrf` | VRF list |
| `/vrf/{name}` | VRF detail |
| `/acl` | ACL list |
| `/acl/{name}` | ACL detail |
| `/bgp/status` | BGP status + neighbors |
| `/bgp/check` | BGP session check |
| `/evpn/status` | EVPN overlay status |
| `/health` | Health report |
| `/lag`, `/lag/{name}` | LAG list / detail |
| `/neighbor` | BGP sessions (alias for `/bgp/check`) |
| `/route/{vrf}/{prefix...}` | APP_DB route lookup |
| `/route-asic/{prefix...}` | ASIC_DB route lookup |
| `/intent/projection` | Per-Node projection (RawConfigDB) from intent replay |
| `POST /intent/projection-diff` | Pre-commit diff for a hypothetical operation set (before/after/diff) |
| `/intent/tree` | Intent DAG tree view |
| `/intents` | List all intent records |

**Device Writes** (S8) -- `POST` under `/newtron/v1/network/{n}/node/{d}/...`

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
| `/apply-qos`, `/remove-qos` | Apply/remove QoS (node-level) |

**Intent Operations** (S11)

| Method | Path suffix | What it does |
|--------|-------------|--------------|
| GET | `/intent/projection` | Per-Node projection (RawConfigDB) from intent replay |
| `POST /intent/projection-diff` | Pre-commit diff for a hypothetical operation set (before/after/diff) |
| GET | `/intent/tree` | Intent DAG tree view |
| GET | `/intent/drift` | Drift between projection (expected) and CONFIG_DB (actual) |
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

**Interface Operations** (S12) -- all `POST /newtron/v1/network/{n}/node/{d}/interface/{i}/...`

| Path suffix | What it does |
|-------------|--------------|
| `/apply-service`, `/remove-service`, `/refresh-service` | Service lifecycle |
| `/configure-interface`, `/unconfigure-interface` | Configure/unconfigure interface |
| `/bind-acl`, `/unbind-acl` | ACL binding |
| `/bind-macvpn`, `/unbind-macvpn` | MAC-VPN binding |
| `/add-bgp-peer`, `/remove-bgp-peer` | BGP peer |
| `/apply-qos`, `/remove-qos` | QoS policy |
| `/set-property` | Set port property |

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

These parameters apply to endpoints documented with "**Query parameters:** `dry_run`, `no_save`" below. Read-only endpoints and lifecycle operations (reload-config, save-config, restart-daemon, ssh-command) ignore them.

Network spec write endpoints (S5) also accept `dry_run` -- when `"true"`, the spec
is validated but not persisted to disk.

### Path Parameters

**Interface names** containing slashes (e.g., `Ethernet0/1`) must be URL-encoded:
`Ethernet0%2F1`. The server decodes `%2F` back to `/`.

**Route prefixes** use Go 1.22's `{prefix...}` catch-all pattern, which captures
the remainder of the path including slashes. A prefix like `10.0.0.0/24` is passed
as a literal path segment: `/route/default/10.0.0.0/24`.

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
curl -s -X POST http://localhost:18080/newtron/v1/network/default/node/switch1/create-vlan \
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
curl -s -X POST http://localhost:18080/newtron/v1/network/default/node/switch1/create-vlan \
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
# Start the server with a spec directory
newtron-server -spec-dir /etc/newtron -net-id default

# Or register dynamically via the API
curl -X POST http://localhost:18080/network \
  -H "Content-Type: application/json" \
  -d '{"id": "default", "spec_dir": "/etc/newtron"}'
```

See [S3 Server Management](#3-server-management).

### 2. Provision devices from the topology

```bash
# Per-device: clean factory CONFIG_DB, then load topology spec and deliver
curl -X POST http://localhost:18080/newtron/v1/network/default/node/switch1/init-device
curl -X POST 'http://localhost:18080/newtron/v1/network/default/node/switch1/intent/reconcile?mode=topology'
```

This is the canonical "spec → device" path: init-device clears factory entries,
intent/reconcile in topology mode loads the spec into the projection and writes
it to the device. The intent/reconcile pipeline IS the provisioning pipeline.
See [S6 Provisioning](#6-provisioning) and [S11](#11-intent-operations).

### 3. Verify health after provisioning

```bash
# Check that BGP sessions came up
curl http://localhost:18080/newtron/v1/network/default/node/switch1/bgp/check

# Run full health check
curl http://localhost:18080/newtron/v1/network/default/node/switch1/health
```

See [S7 Node Read Operations](#7-node-read-operations).

### 4. Apply services to interfaces

```bash
# Apply a service to an interface
curl -X POST http://localhost:18080/newtron/v1/network/default/node/switch1/interface/Ethernet0/apply-service \
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
curl http://localhost:18080/newtron/v1/network/default/node/switch1/intent/drift

# Check a specific route in the forwarding table
curl http://localhost:18080/newtron/v1/network/default/node/switch1/route/default/10.1.1.0/30
```

Per-write verification (did THIS specific write land?) is reported inline on
the originating `WriteResult.Verification` field, or surfaced as the 409 Data
envelope on a `VerificationFailedError`. See [S11](#11-intent-operations)
and [S7 Node Read Operations](#7-node-read-operations).

### 6. Day-2 operations

```bash
# Preview a change without applying (dry-run)
curl -X POST 'http://localhost:18080/newtron/v1/network/default/node/switch1/create-vlan?dry_run=true' \
  -H "Content-Type: application/json" \
  -d '{"id": 200, "description": "New VLAN"}'

# Refresh a service after spec changes
curl -X POST http://localhost:18080/newtron/v1/network/default/node/switch1/interface/Ethernet0/refresh-service

# Remove a service
curl -X POST http://localhost:18080/newtron/v1/network/default/node/switch1/interface/Ethernet0/remove-service
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
loads the spec directory (network.json, device profiles, service definitions) into
memory.

### POST /newtron/v1/network

Register a new network from a spec directory.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Unique network identifier (e.g., `"default"`) |
| `spec_dir` | string | yes | Absolute path to the spec directory |

**Response (201):**

```json
{"data": {"id": "default"}}
```

**Status codes:** 201 created, 400 missing fields or invalid JSON, 409 ID already registered, 500 spec directory load error

**Example:**

```
POST /newtron/v1/network
{"id": "lab", "spec_dir": "/etc/newtron/lab"}
```

### GET /newtron/v1/network

List all registered networks.

**Response (200):**

```json
{
  "data": [
    {
      "id": "default",
      "spec_dir": "/etc/newtron",
      "has_topology": true,
      "nodes": ["switch1", "switch2"]
    }
  ]
}
```

The `nodes` field lists device names from the topology file (empty if `has_topology`
is false).

### POST /newtron/v1/network/{netID}/unregister

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

### POST /newtron/v1/network/{netID}/reload

Reload a network's specs from disk without restarting the server. Stops the existing
NetworkActor (draining all NodeActors and SSH connections), reloads specs from the
stored spec directory, and creates a fresh NetworkActor. SSH connections reconnect
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

**Status codes:** 200 success, 404 network not registered, 500 spec directory load error

**Example:**

```
POST /newtron/v1/network/default/reload
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
specs, QoS policies, filters, platforms, device profiles, zones, and topology
metadata. They do not connect to any device; they read what was loaded from the
spec directory at registration time.

All spec read endpoints require a registered network (`{netID}`) and are serialized
through the NetworkActor to prevent concurrent modification during spec writes.

### Spec Resource Endpoints (List / Show)

Ten resource types follow an identical pattern -- list all returns an array, show
one by name returns a single object (or 404 if not found):

| Resource | List endpoint | Show endpoint | Response type |
|----------|--------------|---------------|---------------|
| Services | `GET /newtron/v1/network/{netID}/service` | `GET .../service/{name}` | [`ServiceDetail`](#servicedetail) |
| IP-VPNs | `GET /newtron/v1/network/{netID}/ipvpn` | `GET .../ipvpn/{name}` | [`IPVPNDetail`](#ipvpndetail) |
| MAC-VPNs | `GET /newtron/v1/network/{netID}/macvpn` | `GET .../macvpn/{name}` | [`MACVPNDetail`](#macvpndetail) |
| QoS Policies | `GET /newtron/v1/network/{netID}/qos-policy` | `GET .../qos-policy/{name}` | [`QoSPolicyDetail`](#qospolicydetail) |
| Filters | `GET /newtron/v1/network/{netID}/filter` | `GET .../filter/{name}` | [`FilterDetail`](#filterdetail) |
| Platforms | `GET /newtron/v1/network/{netID}/platform` | `GET .../platform/{name}` | [`PlatformDetail`](#platformdetail) |
| Route Policies | `GET /newtron/v1/network/{netID}/route-policy` | `GET .../route-policy/{name}` | Route policy detail |
| Prefix Lists | `GET /newtron/v1/network/{netID}/prefix-list` | `GET .../prefix-list/{name}` | Prefix list detail |
| Profiles | `GET /newtron/v1/network/{netID}/profile` | `GET .../profile/{name}` | [`DeviceProfileDetail`](#deviceprofiledetail) |
| Zones | `GET /newtron/v1/network/{netID}/zone` | `GET .../zone/{name}` | [`ZoneDetail`](#zonedetail) |

All response types are defined in [S13 Types Reference](#13-types-reference).

**Example:**

```
GET /newtron/v1/network/default/service          -> {"data": [ ... array of ServiceDetail ... ]}
GET /newtron/v1/network/default/service/transit  -> {"data": { ... single ServiceDetail ... }}
GET /newtron/v1/network/default/service/missing  -> {"error": "not found: service 'missing'"}
```

#### GET /newtron/v1/network/{netID}/service/{name}/projection

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
GET /newtron/v1/network/default/service/TRANSIT/projection
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

### Topology

#### GET /newtron/v1/network/{netID}/topology

Returns the full topology spec as `TopologySpecFile` — the canonical typed
substrate newtron uses internally (devices, links, newtlab metadata).

**Response (200):** `TopologySpecFile` with `version`, `description`,
`devices` (map of name → `TopologyDevice`), and `links` (array; omitted when
empty).

**Errors:** 404 when no `topology.json` was loaded for the network.

_Lands newtron#14 (Cluster C — topology spec substrate, §46)._

#### POST /newtron/v1/network/{netID}/topology/create-node

Adds a device entry to `topology.json`. The matching profile file
(`profiles/{name}.json`) must already exist; if absent, the call returns 400
with the resolution path included.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Topology device name; must match a profile filename. |
| `device` | TopologyDevice | yes | Typed entry: `ports` (interface declarations) and optional `steps[]` (intent operations to replay when the node is built). May be empty for a bare declaration; subsequent operations + `intent save --topology` populate `steps[]`. |

**Response (201):** the persisted `TopologyDevice`.

**Errors:** 409 with `*ConflictError` if a device with this name already
exists; 400 if the profile file is missing or the body is invalid.

#### DELETE /newtron/v1/network/{netID}/topology/node/{name}

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

#### PUT /newtron/v1/network/{netID}/topology/node/{name}

Replaces the device entry at `name` with the body (full-replacement
semantics — no partial patch). Closes the api-layer NodeActor cache so the
next request rebuilds from the new spec.

**Path params:** `name`.

**Request body:** `TopologyDevice` (the full new entry).

**Response (200):** the new `TopologyDevice`.

**Errors:** 404 when the name doesn't exist; 400 if profile missing or body
invalid.

#### POST /newtron/v1/network/{netID}/topology/create-link

Adds a link to `topology.json`. Refuses when either endpoint is already
wired to another link (a port participates in at most one link). Validates
that both endpoint devices exist in topology AND that each interface is
declared on its device's `Ports` map.

**Request body:** `TopologyLink` (`{a: "device:interface", z: "device:interface"}`).

**Response (201):** the persisted `TopologyLink`.

**Errors:** 409 with `*ConflictError` when an endpoint is already wired;
400 when an endpoint device or interface is unknown.

#### DELETE /newtron/v1/network/{netID}/topology/link/{device}/{interface}

Removes the link containing the given `{device, interface}` endpoint.
Single-endpoint identification: a port participates in at most one link, so
one endpoint uniquely identifies the link.

**Path params:** `device`, `interface`.

**Response (200):** `{"deleted": "<device>:<interface>"}`.

**Errors:** 404 when no link contains the endpoint.

_All five CRUD endpoints land newtron#15 + #16 (Phase 5 — topology spec
substrate CRUD). §7 + §15 + §27 + §46._

#### GET /newtron/v1/network/{netID}/topology/node

List device names from the topology file.

**Response (200):** Array of strings (device names)

**Example response:**

```json
{"data": ["switch1", "switch2"]}
```

### Hosts

#### GET /newtron/v1/network/{netID}/host/{name}

Get the host profile for a virtual host device. Returns 404 for switch devices
(even if they exist in the topology) -- the client uses 200 vs 404 from this
endpoint to classify devices as hosts vs switches.

**Response (200):** `HostProfile` (see [S13](#hostprofile))

**Status codes:** 200 success, 404 not a host device or not found

### Features

#### GET /newtron/v1/network/{netID}/feature

List all features and their support status.

**Response (200):** Feature map

#### GET /newtron/v1/network/{netID}/feature/{name}/dependency

Get the dependency list for a feature.

**Path parameters:** `name` -- feature name

**Response (200):** Array of dependency strings

#### GET /newtron/v1/network/{netID}/feature/{name}/unsupported-due-to

Get the features that cause a given feature to be unsupported.

**Response (200):** Array of blocking feature strings

#### GET /newtron/v1/network/{netID}/platform/{name}/supports/{feature}

Check whether a platform supports a specific feature.

**Path parameters:** `name` -- platform name, `feature` -- feature name

**Response (200):**

```json
{"data": {"supported": true}}
```

---

## 5. Network Spec Writes

These endpoints create and delete spec definitions (services, VPNs, QoS policies,
filters, device profiles, zones, prefix lists, route policies). They modify the
in-memory spec and persist changes to the spec directory on disk. Like spec reads,
they are serialized through the NetworkActor.

All spec write endpoints use RPC-style naming: `POST .../create-X` and
`POST .../delete-X`. They accept the `dry_run` query parameter. When `dry_run=true`,
the spec is validated but not persisted.

### Services

#### POST /newtron/v1/network/{netID}/create-service

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
POST /newtron/v1/network/default/create-service
{
  "name": "customer-l3",
  "type": "evpn-routed",
  "ipvpn": "customer-vpn",
  "description": "L3 overlay service with IP-VPN"
}
```

#### POST /newtron/v1/network/{netID}/delete-service

Delete a service definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Service name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

**Status codes:** 200 success, 404 service not found

### IP-VPNs

#### POST /newtron/v1/network/{netID}/create-ipvpn

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

#### POST /newtron/v1/network/{netID}/delete-ipvpn

Delete an IP-VPN definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | IP-VPN name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### MAC-VPNs

#### POST /newtron/v1/network/{netID}/create-macvpn

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

#### POST /newtron/v1/network/{netID}/delete-macvpn

Delete a MAC-VPN definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | MAC-VPN name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### QoS Policies

#### POST /newtron/v1/network/{netID}/create-qos-policy

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

#### POST /newtron/v1/network/{netID}/delete-qos-policy

Delete a QoS policy definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Policy name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

#### POST /newtron/v1/network/{netID}/add-qos-queue

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

#### POST /newtron/v1/network/{netID}/remove-qos-queue

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

#### POST /newtron/v1/network/{netID}/create-filter

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

#### POST /newtron/v1/network/{netID}/delete-filter

Delete a filter definition.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Filter name to delete |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

#### POST /newtron/v1/network/{netID}/add-filter-rule

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

#### POST /newtron/v1/network/{netID}/remove-filter-rule

Remove a rule from a filter.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filter` | string | yes | Filter name |
| `sequence` | integer | yes | Sequence number to remove |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### Prefix Lists

#### POST /newtron/v1/network/{netID}/create-prefix-list

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

#### POST /newtron/v1/network/{netID}/delete-prefix-list

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

#### POST /newtron/v1/network/{netID}/add-prefix-list-entry

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

#### POST /newtron/v1/network/{netID}/remove-prefix-list-entry

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

#### POST /newtron/v1/network/{netID}/create-route-policy

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

#### POST /newtron/v1/network/{netID}/delete-route-policy

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

#### POST /newtron/v1/network/{netID}/add-route-policy-rule

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

#### POST /newtron/v1/network/{netID}/remove-route-policy-rule

Remove a rule from a route policy.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | Route policy name |
| `sequence` | integer | yes | Sequence number to remove |

**Response (200):**

```json
{"data": {"status": "deleted"}}
```

### Device Profiles

Profiles are stored as individual JSON files under `profiles/{name}.json` in the
spec directory. They define per-device settings (management IP, loopback, zone,
platform, EVPN peering).

#### POST /newtron/v1/network/{netID}/create-profile

Create a new device profile.

**Query parameters:** `dry_run`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Profile name (becomes `profiles/{name}.json`) |
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

#### POST /newtron/v1/network/{netID}/delete-profile

Delete a device profile.

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

#### POST /newtron/v1/network/{netID}/create-zone

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

#### POST /newtron/v1/network/{netID}/delete-zone

Delete a zone. Returns error if any device profile references this zone.

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

---

## 6. Provisioning

Provisioning brings a device from clean-factory to fully-configured-per-topology.
It is decomposed into two operations:

1. **`POST /newtron/v1/network/{n}/node/{d}/init-device`** — clean factory CONFIG_DB entries
   that would conflict with newtron-managed state. Idempotent. See below.
2. **`POST /newtron/v1/network/{n}/node/{d}/intent/reconcile`** with `?mode=topology` — load
   the topology spec into the projection and deliver it to the device. This is
   the canonical "spec → device" path. See §11.

There is no separate `/provision` endpoint. The intent/reconcile pipeline IS the
provisioning pipeline — provisioning and reconciliation are two sides of the same
coin (substrate-faithful, §46): the only difference is whether the projection
starts from topology spec (provisioning) or from the device's existing intents
(maintenance reconcile). For network-wide provisioning, iterate over
`/network/{n}/topology/node` and call init-device + intent/reconcile per node.

### POST /newtron/v1/network/{netID}/node/{device}/init-device

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

#### GET /newtron/v1/network/{netID}/node/{device}/info

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

#### GET /newtron/v1/network/{netID}/node/{device}/interface

List all interfaces with summary status.

**Response (200):** Array of `InterfaceSummary` (see [S13](#interfacesummary))

#### GET /newtron/v1/network/{netID}/node/{device}/interface/{name}

Show detailed properties of a single interface.

**Path parameters:** `name` -- interface name (URL-encode slashes: `Ethernet0%2F1`)

**Response (200):** `InterfaceDetail` (see [S13](#interfacedetail))

**Status codes:** 200 success, 404 interface not found

#### GET /newtron/v1/network/{netID}/node/{device}/interface/{name}/binding

Show the service binding on an interface.

**Path parameters:** `name` -- interface name

**Response (200):** `ServiceBindingDetail` (see [S13](#servicebindingdetail)) or `null` if no binding

### VLANs

#### GET /newtron/v1/network/{netID}/node/{device}/vlan

List all VLANs with summary status.

**Response (200):** Array of `VLANStatusEntry` (see [S13](#vlanstatusentry))

#### GET /newtron/v1/network/{netID}/node/{device}/vlan/{id}

Show a single VLAN with full details.

**Path parameters:** `id` -- VLAN ID (integer, 1-4094)

**Response (200):** `VLANStatusEntry`

**Status codes:** 200 success, 400 invalid VLAN ID, 404 VLAN not found

### VRFs

#### GET /newtron/v1/network/{netID}/node/{device}/vrf

List all VRFs with operational state.

**Response (200):** Array of `VRFStatusEntry` (see [S13](#vrfstatusentry))

#### GET /newtron/v1/network/{netID}/node/{device}/vrf/{name}

Show a VRF with its interfaces and BGP neighbors.

**Path parameters:** `name` -- VRF name

**Response (200):** `VRFDetail` (see [S13](#vrfdetail))

**Status codes:** 200 success, 404 VRF not found

### ACLs

#### GET /newtron/v1/network/{netID}/node/{device}/acl

List all ACL tables with summary info.

**Response (200):** Array of `ACLTableSummary` (see [S13](#acltablesummary))

#### GET /newtron/v1/network/{netID}/node/{device}/acl/{name}

Show an ACL table with all its rules.

**Path parameters:** `name` -- ACL table name

**Response (200):** `ACLTableDetail` (see [S13](#acltabledetail))

**Status codes:** 200 success, 404 ACL not found

### BGP

#### GET /newtron/v1/network/{netID}/node/{device}/bgp/status

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

#### GET /newtron/v1/network/{netID}/node/{device}/bgp/check

Check BGP session states. Returns the same data as `bgp/status` (both call
`CheckBGPSessions` internally) but is semantically a health probe -- clients
use it to assert that all sessions are established.

**Response (200):** `BGPStatusResult`

### EVPN

#### GET /newtron/v1/network/{netID}/node/{device}/evpn/status

Get EVPN overlay status: VTEP tunnels, NVO configuration, VNI mappings, L3VNI
VRF bindings, remote VTEPs, and VNI count.

**Response (200):** `EVPNStatusResult` (see [S13](#evpnstatusresult))

### Health

#### GET /newtron/v1/network/{netID}/node/{device}/health

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

#### GET /newtron/v1/network/{netID}/node/{device}/lag

List all LAGs (PortChannels) with member and operational status.

**Response (200):** Array of `LAGStatusEntry` (see [S13](#lagstatusentry))

#### GET /newtron/v1/network/{netID}/node/{device}/lag/{name}

Show a single LAG with full details.

**Path parameters:** `name` -- LAG name (e.g., `PortChannel1`)

**Response (200):** `LAGStatusEntry`

**Status codes:** 200 success, 404 LAG not found

### Neighbors

#### GET /newtron/v1/network/{netID}/node/{device}/neighbor

Get BGP session state. This is functionally identical to `bgp/check` -- both
call `CheckBGPSessions` internally and return `BGPStatusResult`.

**Response (200):** `BGPStatusResult`

### Routes

#### GET /newtron/v1/network/{netID}/node/{device}/route/{vrf}/{prefix...}

Look up a route in APP_DB (FRR's routing table as synced by fpmsyncd).

**Path parameters:**
- `vrf` -- VRF name (use `"default"` for the global table)
- `prefix` -- IP prefix with mask (e.g., `10.0.0.0/24`). Uses catch-all pattern;
  no URL encoding needed for the slash.

**Response (200):** `RouteEntry` (see [S13](#routeentry))

**Status codes:** 200 success, 404 route not found

**Example:**

```
GET /newtron/v1/network/default/node/switch1/route/default/10.0.0.0/24
```

#### GET /newtron/v1/network/{netID}/node/{device}/route-asic/{prefix...}

Look up a route in ASIC_DB (SAI route table as programmed by orchagent).

**Path parameters:** `prefix` -- IP prefix with mask (catch-all pattern)

**Response (200):** `RouteEntry` with `source: "ASIC_DB"`

**Example:**

```
GET /newtron/v1/network/default/node/switch1/route-asic/10.0.0.0/24
```

### Intent Tree

#### GET /newtron/v1/network/{netID}/node/{device}/intent/tree

Get a tree view of the intent DAG (directed acyclic graph). The intent tree
shows parent-child relationships between intent records.

**Query parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `kind` | string | `""` | Filter by intent kind (e.g., `"service"`, `"vlan"`) |
| `resource` | string | `""` | Filter by resource name |
| `ancestors` | string | `"false"` | When `"true"`, include ancestor intents |

**Response (200):** Intent tree structure

---

## 8. Node Write Operations

These endpoints modify device CONFIG_DB. Most use the `connectAndExecute` pattern:
connect -> Lock (refresh) -> fn (build ChangeSet) -> Commit -> Save -> Unlock. They
accept `dry_run` and `no_save` query parameters.

Write operations return `WriteResult` (see [S13](#writeresult)) on success, which
reports the change count, whether changes were applied, verified, and saved.

### Setup Device

#### POST /newtron/v1/network/{netID}/node/{device}/setup-device

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
POST /newtron/v1/network/default/node/switch1/setup-device
{
  "fields": {"hostname": "switch1"},
  "source_ip": "10.0.0.1"
}
```

### VLANs

#### POST /newtron/v1/network/{netID}/node/{device}/create-vlan

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
POST /newtron/v1/network/default/node/switch1/create-vlan?dry_run=true
{"id": 100, "description": "Customer VLAN"}
```

#### POST /newtron/v1/network/{netID}/node/{device}/delete-vlan

Delete a VLAN and all its members.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer | yes | VLAN ID to delete |

**Response (200):** `WriteResult`

### IRB (SVI)

#### POST /newtron/v1/network/{netID}/node/{device}/configure-irb

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

#### POST /newtron/v1/network/{netID}/node/{device}/unconfigure-irb

Remove an IRB interface (SVI) configuration.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID of the SVI to remove |

**Response (200):** `WriteResult`

### VRFs

#### POST /newtron/v1/network/{netID}/node/{device}/create-vrf

Create a VRF.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | VRF name |

**Response (201):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/delete-vrf

Delete a VRF and clean up all associated resources (interfaces, routes, VNI
mappings).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | VRF name to delete |

**Response (200):** `WriteResult`

### IP-VPN Binding

#### POST /newtron/v1/network/{netID}/node/{device}/bind-ipvpn

Bind an IP-VPN to a VRF (sets up L3VNI, route targets, EVPN VNI configuration).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |
| `ipvpn` | string | yes | IP-VPN spec name |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/unbind-ipvpn

Unbind the IP-VPN from a VRF (tears down L3VNI infrastructure).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |

**Response (200):** `WriteResult`

### MAC-VPN Binding (Node-Level)

#### POST /newtron/v1/network/{netID}/node/{device}/bind-macvpn

Bind a MAC-VPN to a VLAN at the node level (maps VLAN to L2VNI).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID |
| `vni` | integer | yes | L2 VNI number |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/unbind-macvpn

Unbind the MAC-VPN from a VLAN at the node level.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID |

**Response (200):** `WriteResult`

### Static Routes

#### POST /newtron/v1/network/{netID}/node/{device}/add-static-route

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

#### POST /newtron/v1/network/{netID}/node/{device}/remove-static-route

Remove a static route.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |
| `prefix` | string | yes | Route prefix to remove |

**Response (200):** `WriteResult`

### ACLs

#### POST /newtron/v1/network/{netID}/node/{device}/create-acl

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

#### POST /newtron/v1/network/{netID}/node/{device}/delete-acl

Delete an ACL table and all its rules.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | ACL table name to delete |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/add-acl-rule

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

#### POST /newtron/v1/network/{netID}/node/{device}/remove-acl-rule

Remove a rule from an ACL table.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `rule` | string | yes | Rule name to remove |

**Response (200):** `WriteResult`

### PortChannels

#### POST /newtron/v1/network/{netID}/node/{device}/create-portchannel

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

#### POST /newtron/v1/network/{netID}/node/{device}/delete-portchannel

Delete a PortChannel and remove all members.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | PortChannel name to delete |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/add-portchannel-member

Add an interface to a PortChannel.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `portchannel` | string | yes | PortChannel name |
| `interface` | string | yes | Interface name |

**Response (201):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/remove-portchannel-member

Remove an interface from a PortChannel.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `portchannel` | string | yes | PortChannel name |
| `interface` | string | yes | Interface name |

**Response (200):** `WriteResult`

### BGP EVPN Peers

#### POST /newtron/v1/network/{netID}/node/{device}/add-bgp-evpn-peer

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

#### POST /newtron/v1/network/{netID}/node/{device}/remove-bgp-evpn-peer

Remove a BGP EVPN overlay peer.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ip` | string | yes | Neighbor IP address to remove |

**Response (200):** `WriteResult`

### QoS at the node level (substrate-only annotation)

Newtron does NOT expose node-level `POST /node/{device}/apply-qos` or
`POST /node/{device}/remove-qos` endpoints. QoS apply/remove is an
interface-scoped operation (per `DESIGN_PRINCIPLES_NEWTRON.md` §6: "The
interface is the point of service delivery, unit of lifecycle"). The
wired endpoints are:

- `POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/apply-qos`
- `POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-qos`

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

### POST /newtron/v1/network/{netID}/node/{device}/reload-config

Trigger a SONiC config reload on the device (`config reload -y`). This reloads
CONFIG_DB from `/etc/sonic/config_db.json` and restarts all SONiC services.

**Request body:** none

**Response (200):** `null` data on success

### POST /newtron/v1/network/{netID}/node/{device}/save-config

Save the running CONFIG_DB to `/etc/sonic/config_db.json` (`config save -y`).

**Request body:** none

**Response (200):** `null` data on success

### POST /newtron/v1/network/{netID}/node/{device}/restart-daemon

Restart a SONiC daemon on the device (`systemctl restart <daemon>`).

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `daemon` | string | yes | Daemon name (e.g., `"bgp"`, `"swss"`) |

**Response (200):** `null` data on success

### POST /newtron/v1/network/{netID}/node/{device}/ssh-command

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

### GET /newtron/v1/network/{netID}/node/{device}/configdb

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

### GET /newtron/v1/network/{netID}/node/{device}/configdb/{table}

List all keys in a CONFIG_DB table.

**Path parameters:** `table` -- CONFIG_DB table name (e.g., `VLAN`, `BGP_GLOBALS`)

**Response (200):** Array of key strings

### GET /newtron/v1/network/{netID}/node/{device}/configdb/{table}/{key}

Get all fields of a CONFIG_DB entry.

**Path parameters:** `table` -- table name, `key` -- entry key (e.g., `Vlan100`)

**Response (200):** Field map (`map[string]string`)

**Example:**

```
GET /newtron/v1/network/default/node/switch1/configdb/VLAN/Vlan100
```

### GET /newtron/v1/network/{netID}/node/{device}/configdb/{table}/{key}/exists

Check if a CONFIG_DB entry exists.

**Path parameters:** `table` -- table name, `key` -- entry key

**Response (200):**

```json
{"data": {"exists": true}}
```

### GET /newtron/v1/network/{netID}/node/{device}/statedb/{table}/{key}

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

Newtron does NOT expose a bulk `GET /node/{device}/intents` HTTP endpoint
that returns every `NEWTRON_INTENT` row. The substrate is reachable via two
typed substrate paths instead:

- `GET /node/{device}/intent/tree` returns the structured intent DAG with
  parent/child relationships (the operator-meaningful view).
- `GET /node/{device}/configdb/NEWTRON_INTENT` returns the raw CONFIG_DB
  table (the per-key generic substrate read).

The bulk-list endpoint as a separate route would be derivative of these two
typed primitives and a §46 violation (typed substrate exists and is already
exposed; a parallel "list everything" endpoint would summarize what's
already typed). Per `DESIGN_PRINCIPLES_NEWTRON.md` §21 (Reconstruct, Don't
Record): the intent DB is reconstructed by replay, not preserved as a flat
list. Consumers needing the flat list use `configdb/NEWTRON_INTENT`.

### Wired intent operations

#### GET /newtron/v1/network/{netID}/node/{device}/intent/tree

Get a tree view of the intent DAG. See [S7 Intent Tree](#intent-tree) for query parameters.

#### GET /newtron/v1/network/{netID}/node/{device}/intent/projection

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

#### POST /newtron/v1/network/{netID}/node/{device}/intent/projection-diff

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
POST /newtron/v1/network/default/node/switch1/intent/projection-diff
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
operations). There is no network-wide `/network/{n}/drift` endpoint;
operators iterate over `/network/{n}/topology/node` and call
`/intent/drift` per node.

---

## 12. Interface Operations

These endpoints operate on a specific interface within a device. They are the
primary way to apply and manage services. All use `connectAndExecute` and accept
`dry_run`/`no_save` query parameters. Return `WriteResult` on success.

Interface names containing slashes must be URL-encoded: `Ethernet0%2F1` -> `Ethernet0/1`.

**Quick reference** -- all interface endpoints under `.../interface/{name}/`:

| Category | Endpoints | Key params |
|----------|-----------|------------|
| Service | `apply-service`, `remove-service`, `refresh-service` | `service`, `ip_address`, `vlan`, `peer_as` |
| Interface config | `configure-interface`, `unconfigure-interface` | `vrf`, `ip`, `vlan_id`, `tagged` |
| ACL | `bind-acl`, `unbind-acl` | `acl`, `direction` |
| MAC-VPN | `bind-macvpn`, `unbind-macvpn` | `macvpn` |
| BGP | `add-bgp-peer`, `remove-bgp-peer` | `neighbor_ip`, `remote_as` |
| QoS | `apply-qos`, `remove-qos` | `policy` |
| Port property | `set-property` | `property`, `value` |

All endpoints use `POST` method.

### Service Lifecycle

The three core service operations: apply, remove, refresh. These are the most
frequently used endpoints in the API -- most network automation workflows center
on applying services to interfaces.

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/apply-service

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
POST /newtron/v1/network/default/node/switch1/interface/Ethernet0/apply-service
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

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-service

Remove the service binding from the interface. Tears down all CONFIG_DB
infrastructure that was created by `apply-service`, using the stored binding
(not the current spec) to determine what to remove.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/refresh-service

Refresh the service binding -- removes the current configuration and re-applies
from the current spec. Use after spec changes to update a running service
without manual remove+apply.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### Interface Configuration

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/configure-interface

Configure an interface in routed mode (VRF + IP) or bridged mode (VLAN membership).
The two modes are mutually exclusive.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | no | VRF binding (routed mode) |
| `ip` | string | no | IP address in CIDR (routed mode) |
| `vlan_id` | integer | no | VLAN ID (bridged mode) |
| `tagged` | boolean | no | Tagged membership (bridged mode) |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/unconfigure-interface

Remove all configuration from an interface (VRF binding, IP addresses, VLAN
membership). Returns the interface to its unconfigured state.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### ACL Binding

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/bind-acl

Bind an ACL to the interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `direction` | string | yes | `"ingress"` or `"egress"` |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/unbind-acl

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

- `POST /newtron/v1/network/{netID}/node/{device}/bind-macvpn` — see §Node-level
  Service Composition above.
- `POST /newtron/v1/network/{netID}/node/{device}/unbind-macvpn` — same.

The earlier `/interface/{name}/bind-macvpn` and `/interface/{name}/unbind-macvpn`
paths in this document were never implemented.

### BGP Peer

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/add-bgp-peer

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

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-bgp-peer

Remove the BGP peer from this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### QoS

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/apply-qos

Apply a QoS policy to this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | QoS policy name from specs |

**Response (200):** `WriteResult`

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/remove-qos

Remove the QoS policy from this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### Port Property

#### POST /newtron/v1/network/{netID}/node/{device}/interface/{name}/set-property

Set a property on the interface (e.g., `mtu`, `admin_status`, `speed`).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `property` | string | yes | Property name (e.g., `"mtu"`, `"admin_status"`) |
| `value` | string | yes | Property value |

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
| `changes` | ConfigChange[] (optional) | Typed ChangeSet entries — every CONFIG_DB add/modify/delete in this operation, in the same `sonic.ConfigChange` shape newtron uses internally. §46 canonical substrate. Absent when `change_count` is 0. |
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

Returned by `GET .../interface/{name}/binding`.

| Field | Type | Description |
|-------|------|-------------|
| `service` | string | Service name |
| `ip_addresses` | string[] | IP addresses from the binding |
| `vrf` | string | VRF from the binding |

### VLAN Types

#### VLANStatusEntry

Returned by `GET .../vlan` and `GET .../vlan/{id}`.

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

Returned by `GET .../vrf/{name}`.

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

Returned by `GET .../acl/{name}`.

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

Returned by `GET .../bgp/status`, `GET .../bgp/check`, and `GET .../neighbor`.

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

Returned by `GET .../lag` and `GET .../lag/{name}`.

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

Returned by `GET .../route/{vrf}/{prefix...}` and `GET .../route-asic/{prefix...}`.

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

Returned by `GET .../host/{name}`.

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

#### ServiceDetail

Returned by `GET .../service` (array) and `GET .../service/{name}` (single).

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

Returned by `GET .../ipvpn` (array) and `GET .../ipvpn/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | IP-VPN name |
| `description` | string | Description |
| `vrf` | string | VRF name |
| `l3vni` | integer | L3 VNI |
| `route_targets` | string[] | Route targets |

#### MACVPNDetail

Returned by `GET .../macvpn` (array) and `GET .../macvpn/{name}` (single).

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

Returned by `GET .../qos-policy` (array) and `GET .../qos-policy/{name}` (single).

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

Returned by `GET .../filter` (array) and `GET .../filter/{name}` (single).

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

Returned by `GET .../platform` (array) and `GET .../platform/{name}` (single).

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

#### DeviceProfileDetail

Returned by `GET .../profile` (array of names) and `GET .../profile/{name}` (single).

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

Returned by `GET .../zone` (array of names) and `GET .../zone/{name}` (single).

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Zone name |

### SSH Command Types

#### SSHCommandResponse

Returned by `POST .../ssh-command`.

| Field | Type | Description |
|-------|------|-------------|
| `output` | string | Command output text |

### Network Registration Types

#### NetworkInfo

Returned in array by `GET /newtron/v1/network`.

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Network identifier |
| `spec_dir` | string | Spec directory path |
| `has_topology` | boolean | Whether a topology file was loaded |
| `nodes` | string[] | Device names from topology |

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
| `-net-id` | `"default"` | Network ID for the auto-registered spec directory |
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

- Each registered network gets a **NetworkActor** that serializes spec operations.
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
