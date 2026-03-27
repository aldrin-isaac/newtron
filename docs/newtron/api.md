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
11. [Intent, History, Settings, and Drift](#11-intent-history-settings-and-drift)
12. [Composite Operations](#12-composite-operations)
13. [Interface Operations](#13-interface-operations)
14. [Batch Execution](#14-batch-execution)
15. [Types Reference](#15-types-reference)
16. [Error Reference](#16-error-reference)
17. [Server Configuration](#17-server-configuration)

### Endpoint Quick Reference

All paths are relative to `http://<host>:<port>`. `{n}` = `{netID}`, `{d}` = `{device}`, `{i}` = `{name}` (interface).

**Server & Specs** (S3-S5)

| Method | Path | What it does |
|--------|------|--------------|
| POST | `/network` | Register a network |
| GET | `/network` | List networks |
| POST | `/network/{n}/unregister` | Unregister a network |
| POST | `/network/{n}/reload` | Reload specs from disk |
| GET | `/network/{n}/service` | List services (also: `/ipvpn`, `/macvpn`, `/qos-policy`, `/filter`, `/platform`, `/route-policy`, `/prefix-list`) |
| GET | `/network/{n}/service/{name}` | Show service (also: ipvpn, macvpn, qos-policy, filter, platform, route-policy, prefix-list) |
| GET | `/network/{n}/profile` | List device profile names |
| GET | `/network/{n}/profile/{name}` | Show device profile |
| GET | `/network/{n}/zone` | List zone names |
| GET | `/network/{n}/zone/{name}` | Show zone |
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

**Provisioning & Composites** (S6, S12)

| Method | Path | What it does |
|--------|------|--------------|
| POST | `/network/{n}/provision` | Provision devices from topology |
| POST | `/network/{n}/node/{d}/init-device` | Initialize device (clean factory config) |
| POST | `/network/{n}/node/{d}/generate-composite` | Generate + store composite (returns UUID handle) |
| POST | `/network/{n}/node/{d}/verify-composite` | Verify composite against device (handle in body) |
| POST | `/network/{n}/node/{d}/deliver-composite` | Deliver composite to device (handle in body) |

**Device Reads** (S7) -- all `GET /network/{n}/node/{d}/...`

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
| `/intent/tree` | Intent DAG tree view |
| `/intents` | List all intent records |

**Device Writes** (S8) -- `POST` under `/network/{n}/node/{d}/...`

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
| `/cleanup` | Remove orphaned resources |

**Intent, History, Settings, Drift** (S11)

| Method | Path suffix | What it does |
|--------|-------------|--------------|
| GET | `/intents` | List all intent records |
| GET | `/intent/tree` | Intent DAG tree view |
| GET | `/zombie` | Read zombie intent |
| POST | `/rollback-zombie` | Rollback zombie intent |
| POST | `/clear-zombie` | Clear zombie intent |
| GET | `/history` | Read operation history |
| POST | `/rollback-history` | Rollback last operation |
| GET | `/settings` | Read device settings |
| PUT | `/settings` | Write device settings |
| GET | `/drift` | Per-device drift detection |
| GET | `/network/{n}/drift` | Network-wide drift detection |

**Lifecycle & Diagnostics** (S9-S10) -- all `POST` unless noted

| Path suffix | What it does |
|-------------|--------------|
| `/reload-config` | Reload CONFIG_DB from disk |
| `/save-config` | Save CONFIG_DB to disk |
| `/refresh` | Refresh cached CONFIG_DB (supports `?timeout=`) |
| `/restart-daemon` | Restart a SONiC daemon |
| `/ssh-command` | Execute SSH command |
| `/verify-committed` | Verify last ChangeSet |
| `GET /configdb/{table}` | List CONFIG_DB keys |
| `GET /configdb/{table}/{key}` | Read CONFIG_DB entry |
| `GET /configdb/{table}/{key}/exists` | Check CONFIG_DB entry exists |
| `GET /statedb/{table}/{key}` | Read STATE_DB entry |

**Interface Operations** (S13) -- all `POST /network/{n}/node/{d}/interface/{i}/...`

| Path suffix | What it does |
|-------------|--------------|
| `/apply-service`, `/remove-service`, `/refresh-service` | Service lifecycle |
| `/configure-interface`, `/unconfigure-interface` | Configure/unconfigure interface |
| `/bind-acl`, `/unbind-acl` | ACL binding |
| `/bind-macvpn`, `/unbind-macvpn` | MAC-VPN binding |
| `/add-bgp-peer`, `/remove-bgp-peer` | BGP peer |
| `/apply-qos`, `/remove-qos` | QoS policy |
| `/set-port-property` | Set port property |

**Batch** (S14) -- `POST /network/{n}/node/{d}/execute`

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
| 409 | Conflict | Network already registered, composite verification failed |
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
is reused. See [S17 Server Configuration](#17-server-configuration) for tuning.

### Example Request

A complete curl example showing the request/response cycle:

```bash
curl -s -X POST http://localhost:8080/network/default/node/switch1/create-vlan \
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
curl -s -X POST http://localhost:8080/network/default/node/switch1/create-vlan \
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
curl -X POST http://localhost:8080/network \
  -H "Content-Type: application/json" \
  -d '{"id": "default", "spec_dir": "/etc/newtron"}'
```

See [S3 Server Management](#3-server-management).

### 2. Provision devices from the topology

```bash
# Provision all devices in the topology
curl -X POST http://localhost:8080/network/default/provision \
  -H "Content-Type: application/json" \
  -d '{"devices": []}'
```

This generates a complete CONFIG_DB composite for each device (BGP, loopback,
EVPN, interfaces) and delivers it. See [S6 Provisioning](#6-provisioning).

### 3. Verify health after provisioning

```bash
# Check that BGP sessions came up
curl http://localhost:8080/network/default/node/switch1/bgp/check

# Run full health check
curl http://localhost:8080/network/default/node/switch1/health
```

See [S7 Node Read Operations](#7-node-read-operations).

### 4. Apply services to interfaces

```bash
# Apply a service to an interface
curl -X POST http://localhost:8080/network/default/node/switch1/interface/Ethernet0/apply-service \
  -H "Content-Type: application/json" \
  -d '{"service": "customer-l3", "ip_address": "10.1.1.1/30"}'
```

Services are the primary operational unit. `apply-service` creates all required
CONFIG_DB infrastructure (VLANs, VRFs, VNI mappings, ACLs, QoS) automatically.
See [S13 Interface Operations](#13-interface-operations).

### 5. Verify the applied configuration

```bash
# Verify that committed changes persisted
curl -X POST http://localhost:8080/network/default/node/switch1/verify-committed

# Check a specific route in the forwarding table
curl http://localhost:8080/network/default/node/switch1/route/default/10.1.1.0/30
```

See [S9 Node Lifecycle Operations](#9-node-lifecycle-operations) and [S7 Node
Read Operations](#7-node-read-operations).

### 6. Day-2 operations

```bash
# Preview a change without applying (dry-run)
curl -X POST 'http://localhost:8080/network/default/node/switch1/create-vlan?dry_run=true' \
  -H "Content-Type: application/json" \
  -d '{"id": 200, "description": "New VLAN"}'

# Refresh a service after spec changes
curl -X POST http://localhost:8080/network/default/node/switch1/interface/Ethernet0/refresh-service

# Remove a service
curl -X POST http://localhost:8080/network/default/node/switch1/interface/Ethernet0/remove-service
```

### When to use batch execution

For operations that should be atomic -- all succeed or none take effect -- use
the batch execute endpoint ([S14](#14-batch-execution)). This is common during
provisioning-like flows where you need to configure baseline and services
in a single ChangeSet:

```bash
curl -X POST http://localhost:8080/network/default/node/switch1/execute \
  -H "Content-Type: application/json" \
  -d '{
    "execute": true,
    "operations": [
      {"action": "create-vlan", "params": {"id": 100}},
      {"action": "create-vrf", "params": {"name": "CUSTOMER"}},
      {"action": "apply-service", "interface": "Ethernet0",
       "params": {"service": "customer-l3", "ip_address": "10.1.1.1/30"}}
    ]
  }'
```

For ad-hoc, individual changes (adding a VLAN, checking status), use the
dedicated endpoints -- they're simpler and the response is the same `WriteResult`.

---

## 3. Server Management

These endpoints register and unregister networks. A network must be registered
before any spec reads, device operations, or provisioning can occur. Registration
loads the spec directory (network.json, device profiles, service definitions) into
memory.

### POST /network

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
POST /network
{"id": "lab", "spec_dir": "/etc/newtron/lab"}
```

### GET /network

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

### POST /network/{netID}/unregister

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

### POST /network/{netID}/reload

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
POST /network/default/reload
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
| Services | `GET /network/{netID}/service` | `GET .../service/{name}` | [`ServiceDetail`](#servicedetail) |
| IP-VPNs | `GET /network/{netID}/ipvpn` | `GET .../ipvpn/{name}` | [`IPVPNDetail`](#ipvpndetail) |
| MAC-VPNs | `GET /network/{netID}/macvpn` | `GET .../macvpn/{name}` | [`MACVPNDetail`](#macvpndetail) |
| QoS Policies | `GET /network/{netID}/qos-policy` | `GET .../qos-policy/{name}` | [`QoSPolicyDetail`](#qospolicydetail) |
| Filters | `GET /network/{netID}/filter` | `GET .../filter/{name}` | [`FilterDetail`](#filterdetail) |
| Platforms | `GET /network/{netID}/platform` | `GET .../platform/{name}` | [`PlatformDetail`](#platformdetail) |
| Route Policies | `GET /network/{netID}/route-policy` | `GET .../route-policy/{name}` | Route policy detail |
| Prefix Lists | `GET /network/{netID}/prefix-list` | `GET .../prefix-list/{name}` | Prefix list detail |
| Profiles | `GET /network/{netID}/profile` | `GET .../profile/{name}` | [`DeviceProfileDetail`](#deviceprofiledetail) |
| Zones | `GET /network/{netID}/zone` | `GET .../zone/{name}` | [`ZoneDetail`](#zonedetail) |

All response types are defined in [S15 Types Reference](#15-types-reference).

**Example:**

```
GET /network/default/service          -> {"data": [ ... array of ServiceDetail ... ]}
GET /network/default/service/transit  -> {"data": { ... single ServiceDetail ... }}
GET /network/default/service/missing  -> {"error": "not found: service 'missing'"}
```

### Topology

#### GET /network/{netID}/topology/node

List device names from the topology file.

**Response (200):** Array of strings (device names)

**Example response:**

```json
{"data": ["switch1", "switch2"]}
```

### Hosts

#### GET /network/{netID}/host/{name}

Get the host profile for a virtual host device. Returns 404 for switch devices
(even if they exist in the topology) -- the client uses 200 vs 404 from this
endpoint to classify devices as hosts vs switches.

**Response (200):** `HostProfile` (see [S15](#hostprofile))

**Status codes:** 200 success, 404 not a host device or not found

### Features

#### GET /network/{netID}/feature

List all features and their support status.

**Response (200):** Feature map

#### GET /network/{netID}/feature/{name}/dependency

Get the dependency list for a feature.

**Path parameters:** `name` -- feature name

**Response (200):** Array of dependency strings

#### GET /network/{netID}/feature/{name}/unsupported-due-to

Get the features that cause a given feature to be unsupported.

**Response (200):** Array of blocking feature strings

#### GET /network/{netID}/platform/{name}/supports/{feature}

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

#### POST /network/{netID}/create-service

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
POST /network/default/create-service
{
  "name": "customer-l3",
  "type": "evpn-routed",
  "ipvpn": "customer-vpn",
  "description": "L3 overlay service with IP-VPN"
}
```

#### POST /network/{netID}/delete-service

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

#### POST /network/{netID}/create-ipvpn

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

#### POST /network/{netID}/delete-ipvpn

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

#### POST /network/{netID}/create-macvpn

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

#### POST /network/{netID}/delete-macvpn

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

#### POST /network/{netID}/create-qos-policy

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

#### POST /network/{netID}/delete-qos-policy

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

#### POST /network/{netID}/add-qos-queue

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

#### POST /network/{netID}/remove-qos-queue

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

#### POST /network/{netID}/create-filter

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

#### POST /network/{netID}/delete-filter

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

#### POST /network/{netID}/add-filter-rule

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

#### POST /network/{netID}/remove-filter-rule

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

#### POST /network/{netID}/create-prefix-list

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

#### POST /network/{netID}/delete-prefix-list

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

#### POST /network/{netID}/add-prefix-list-entry

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

#### POST /network/{netID}/remove-prefix-list-entry

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

#### POST /network/{netID}/create-route-policy

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

#### POST /network/{netID}/delete-route-policy

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

#### POST /network/{netID}/add-route-policy-rule

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

#### POST /network/{netID}/remove-route-policy-rule

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

#### POST /network/{netID}/create-profile

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

#### POST /network/{netID}/delete-profile

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

#### POST /network/{netID}/create-zone

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

#### POST /network/{netID}/delete-zone

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

Provisioning generates a complete CONFIG_DB composite from the topology file and
device profiles, then delivers it to devices. This is the one operation where
spec intent replaces device reality (CompositeOverwrite mode).

### POST /network/{netID}/provision

Provision one or more devices from the topology.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `devices` | string[] | no | Device names to provision. Empty = all devices in topology. |

**Response (200):** `ProvisionResult` -- an aggregate of per-device results. See
[S15 ProvisionResult](#provisionresult) for the structure. On partial failure,
successful devices are still provisioned; the error is reported per-device.

**Example:**

```
POST /network/default/provision
{"devices": ["switch1", "switch2"]}
```

### POST /network/{netID}/node/{device}/init-device

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

#### GET /network/{netID}/node/{device}/info

Get a structured overview of the device.

**Response (200):** `DeviceInfo` (see [S15](#deviceinfo))

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

#### GET /network/{netID}/node/{device}/interface

List all interfaces with summary status.

**Response (200):** Array of `InterfaceSummary` (see [S15](#interfacesummary))

#### GET /network/{netID}/node/{device}/interface/{name}

Show detailed properties of a single interface.

**Path parameters:** `name` -- interface name (URL-encode slashes: `Ethernet0%2F1`)

**Response (200):** `InterfaceDetail` (see [S15](#interfacedetail))

**Status codes:** 200 success, 404 interface not found

#### GET /network/{netID}/node/{device}/interface/{name}/binding

Show the service binding on an interface.

**Path parameters:** `name` -- interface name

**Response (200):** `ServiceBindingDetail` (see [S15](#servicebindingdetail)) or `null` if no binding

### VLANs

#### GET /network/{netID}/node/{device}/vlan

List all VLANs with summary status.

**Response (200):** Array of `VLANStatusEntry` (see [S15](#vlanstatusentry))

#### GET /network/{netID}/node/{device}/vlan/{id}

Show a single VLAN with full details.

**Path parameters:** `id` -- VLAN ID (integer, 1-4094)

**Response (200):** `VLANStatusEntry`

**Status codes:** 200 success, 400 invalid VLAN ID, 404 VLAN not found

### VRFs

#### GET /network/{netID}/node/{device}/vrf

List all VRFs with operational state.

**Response (200):** Array of `VRFStatusEntry` (see [S15](#vrfstatusentry))

#### GET /network/{netID}/node/{device}/vrf/{name}

Show a VRF with its interfaces and BGP neighbors.

**Path parameters:** `name` -- VRF name

**Response (200):** `VRFDetail` (see [S15](#vrfdetail))

**Status codes:** 200 success, 404 VRF not found

### ACLs

#### GET /network/{netID}/node/{device}/acl

List all ACL tables with summary info.

**Response (200):** Array of `ACLTableSummary` (see [S15](#acltablesummary))

#### GET /network/{netID}/node/{device}/acl/{name}

Show an ACL table with all its rules.

**Path parameters:** `name` -- ACL table name

**Response (200):** `ACLTableDetail` (see [S15](#acltabledetail))

**Status codes:** 200 success, 404 ACL not found

### BGP

#### GET /network/{netID}/node/{device}/bgp/status

Get BGP status including local AS, router ID, and all neighbors with operational state.

**Response (200):** `BGPStatusResult` (see [S15](#bgpstatusresult))

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

#### GET /network/{netID}/node/{device}/bgp/check

Check BGP session states. Returns the same data as `bgp/status` (both call
`CheckBGPSessions` internally) but is semantically a health probe -- clients
use it to assert that all sessions are established.

**Response (200):** `BGPStatusResult`

### EVPN

#### GET /network/{netID}/node/{device}/evpn/status

Get EVPN overlay status: VTEP tunnels, NVO configuration, VNI mappings, L3VNI
VRF bindings, remote VTEPs, and VNI count.

**Response (200):** `EVPNStatusResult` (see [S15](#evpnstatusresult))

### Health

#### GET /network/{netID}/node/{device}/health

Run a comprehensive health check on the device. Includes CONFIG_DB verification
(comparing committed config against running config) and operational checks (BGP
sessions, interface status).

**Response (200):** `HealthReport` (see [S15](#healthreport))

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

#### GET /network/{netID}/node/{device}/lag

List all LAGs (PortChannels) with member and operational status.

**Response (200):** Array of `LAGStatusEntry` (see [S15](#lagstatusentry))

#### GET /network/{netID}/node/{device}/lag/{name}

Show a single LAG with full details.

**Path parameters:** `name` -- LAG name (e.g., `PortChannel1`)

**Response (200):** `LAGStatusEntry`

**Status codes:** 200 success, 404 LAG not found

### Neighbors

#### GET /network/{netID}/node/{device}/neighbor

Get BGP session state. This is functionally identical to `bgp/check` -- both
call `CheckBGPSessions` internally and return `BGPStatusResult`.

**Response (200):** `BGPStatusResult`

### Routes

#### GET /network/{netID}/node/{device}/route/{vrf}/{prefix...}

Look up a route in APP_DB (FRR's routing table as synced by fpmsyncd).

**Path parameters:**
- `vrf` -- VRF name (use `"default"` for the global table)
- `prefix` -- IP prefix with mask (e.g., `10.0.0.0/24`). Uses catch-all pattern;
  no URL encoding needed for the slash.

**Response (200):** `RouteEntry` (see [S15](#routeentry))

**Status codes:** 200 success, 404 route not found

**Example:**

```
GET /network/default/node/switch1/route/default/10.0.0.0/24
```

#### GET /network/{netID}/node/{device}/route-asic/{prefix...}

Look up a route in ASIC_DB (SAI route table as programmed by orchagent).

**Path parameters:** `prefix` -- IP prefix with mask (catch-all pattern)

**Response (200):** `RouteEntry` with `source: "ASIC_DB"`

**Example:**

```
GET /network/default/node/switch1/route-asic/10.0.0.0/24
```

### Intent Tree

#### GET /network/{netID}/node/{device}/intent/tree

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

Write operations return `WriteResult` (see [S15](#writeresult)) on success, which
reports the change count, whether changes were applied, verified, and saved.

### Setup Device

#### POST /network/{netID}/node/{device}/setup-device

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
POST /network/default/node/switch1/setup-device
{
  "fields": {"hostname": "switch1"},
  "source_ip": "10.0.0.1"
}
```

### VLANs

#### POST /network/{netID}/node/{device}/create-vlan

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
POST /network/default/node/switch1/create-vlan?dry_run=true
{"id": 100, "description": "Customer VLAN"}
```

#### POST /network/{netID}/node/{device}/delete-vlan

Delete a VLAN and all its members.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer | yes | VLAN ID to delete |

**Response (200):** `WriteResult`

### IRB (SVI)

#### POST /network/{netID}/node/{device}/configure-irb

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

#### POST /network/{netID}/node/{device}/unconfigure-irb

Remove an IRB interface (SVI) configuration.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID of the SVI to remove |

**Response (200):** `WriteResult`

### VRFs

#### POST /network/{netID}/node/{device}/create-vrf

Create a VRF.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | VRF name |

**Response (201):** `WriteResult`

#### POST /network/{netID}/node/{device}/delete-vrf

Delete a VRF and clean up all associated resources (interfaces, routes, VNI
mappings).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | VRF name to delete |

**Response (200):** `WriteResult`

### IP-VPN Binding

#### POST /network/{netID}/node/{device}/bind-ipvpn

Bind an IP-VPN to a VRF (sets up L3VNI, route targets, EVPN VNI configuration).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |
| `ipvpn` | string | yes | IP-VPN spec name |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/unbind-ipvpn

Unbind the IP-VPN from a VRF (tears down L3VNI infrastructure).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |

**Response (200):** `WriteResult`

### MAC-VPN Binding (Node-Level)

#### POST /network/{netID}/node/{device}/bind-macvpn

Bind a MAC-VPN to a VLAN at the node level (maps VLAN to L2VNI).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID |
| `vni` | integer | yes | L2 VNI number |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/unbind-macvpn

Unbind the MAC-VPN from a VLAN at the node level.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vlan_id` | integer | yes | VLAN ID |

**Response (200):** `WriteResult`

### Static Routes

#### POST /network/{netID}/node/{device}/add-static-route

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

#### POST /network/{netID}/node/{device}/remove-static-route

Remove a static route.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `vrf` | string | yes | VRF name |
| `prefix` | string | yes | Route prefix to remove |

**Response (200):** `WriteResult`

### ACLs

#### POST /network/{netID}/node/{device}/create-acl

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

#### POST /network/{netID}/node/{device}/delete-acl

Delete an ACL table and all its rules.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | ACL table name to delete |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/add-acl-rule

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

#### POST /network/{netID}/node/{device}/remove-acl-rule

Remove a rule from an ACL table.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `rule` | string | yes | Rule name to remove |

**Response (200):** `WriteResult`

### PortChannels

#### POST /network/{netID}/node/{device}/create-portchannel

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

#### POST /network/{netID}/node/{device}/delete-portchannel

Delete a PortChannel and remove all members.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | PortChannel name to delete |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/add-portchannel-member

Add an interface to a PortChannel.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `portchannel` | string | yes | PortChannel name |
| `interface` | string | yes | Interface name |

**Response (201):** `WriteResult`

#### POST /network/{netID}/node/{device}/remove-portchannel-member

Remove an interface from a PortChannel.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `portchannel` | string | yes | PortChannel name |
| `interface` | string | yes | Interface name |

**Response (200):** `WriteResult`

### BGP EVPN Peers

#### POST /network/{netID}/node/{device}/add-bgp-evpn-peer

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

#### POST /network/{netID}/node/{device}/remove-bgp-evpn-peer

Remove a BGP EVPN overlay peer.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ip` | string | yes | Neighbor IP address to remove |

**Response (200):** `WriteResult`

### QoS (Node-Level)

#### POST /network/{netID}/node/{device}/apply-qos

Apply a QoS policy to a specific interface (node-level convenience that delegates
to the interface's `ApplyQoS` method).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `interface` | string | yes | Interface name |
| `policy` | string | yes | QoS policy name from specs |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/remove-qos

Remove QoS policy from a specific interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `interface` | string | yes | Interface name |

**Response (200):** `WriteResult`

### Cleanup

#### POST /network/{netID}/node/{device}/cleanup

Scan for and remove orphaned CONFIG_DB resources (ACLs, VRFs, VNI mappings not
referenced by any service binding).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | no | `"acls"`, `"vrfs"`, `"vnis"`, or empty for all |

**Response (200):** `WriteResult`

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

Exceptions: `ssh-command` returns `SSHCommandResponse`, `verify-committed` returns
`VerificationResult`.

### POST /network/{netID}/node/{device}/reload-config

Trigger a SONiC config reload on the device (`config reload -y`). This reloads
CONFIG_DB from `/etc/sonic/config_db.json` and restarts all SONiC services.

**Request body:** none

**Response (200):** `null` data on success

### POST /network/{netID}/node/{device}/save-config

Save the running CONFIG_DB to `/etc/sonic/config_db.json` (`config save -y`).

**Request body:** none

**Response (200):** `null` data on success

### POST /network/{netID}/node/{device}/refresh

Refresh the server's cached CONFIG_DB snapshot from the device's Redis. Use after
external changes (manual CLI, other tools) to ensure the server sees current state.

**Query parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `timeout` | string | none | Go duration (e.g., `"30s"`, `"2m"`). When set, retries the refresh until success or timeout. Use when waiting for a device to become reachable after reboot. |

**Request body:** none

**Response (200):** `null` data on success

**Example:**

```
POST /network/default/node/switch1/refresh?timeout=2m
```

### POST /network/{netID}/node/{device}/restart-daemon

Restart a SONiC daemon on the device (`systemctl restart <daemon>`).

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `daemon` | string | yes | Daemon name (e.g., `"bgp"`, `"swss"`) |

**Response (200):** `null` data on success

### POST /network/{netID}/node/{device}/ssh-command

Execute an arbitrary SSH command on the device and return the output.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Shell command to execute |

**Response (200):** `SSHCommandResponse`

```json
{"data": {"output": "SONiC Software Version: SONiC.202505..."}}
```

### POST /network/{netID}/node/{device}/verify-committed

Verify that the last committed ChangeSet was persisted correctly in CONFIG_DB. Reads
back the entries that were written and compares them against expected values.

This uses `connectAndRead` -- it reads current CONFIG_DB state and compares against
the stored committed changes.

**Request body:** none

**Response (200):** `VerificationResult` (see [S15](#verificationresult))

---

## 10. Node Diagnostics

These endpoints provide direct access to SONiC Redis databases for debugging and
inspection. They use `connectAndRead` -- no `dry_run`/`no_save`.

### GET /network/{netID}/node/{device}/configdb/{table}

List all keys in a CONFIG_DB table.

**Path parameters:** `table` -- CONFIG_DB table name (e.g., `VLAN`, `BGP_GLOBALS`)

**Response (200):** Array of key strings

### GET /network/{netID}/node/{device}/configdb/{table}/{key}

Get all fields of a CONFIG_DB entry.

**Path parameters:** `table` -- table name, `key` -- entry key (e.g., `Vlan100`)

**Response (200):** Field map (`map[string]string`)

**Example:**

```
GET /network/default/node/switch1/configdb/VLAN/Vlan100
```

### GET /network/{netID}/node/{device}/configdb/{table}/{key}/exists

Check if a CONFIG_DB entry exists.

**Path parameters:** `table` -- table name, `key` -- entry key

**Response (200):**

```json
{"data": {"exists": true}}
```

### GET /network/{netID}/node/{device}/statedb/{table}/{key}

Get all fields of a STATE_DB entry.

**Path parameters:** `table` -- STATE_DB table name, `key` -- entry key

**Response (200):** Field map (`map[string]string`)

---

## 11. Intent, History, Settings, and Drift

These endpoints manage intent records, operation history, device settings, and
drift detection. Intent records track what newtron has applied to a device; history
tracks the sequence of operations; settings control device-level behavior; drift
detection compares actual CONFIG_DB state against expected state from intents.

### Intents

#### GET /network/{netID}/node/{device}/intents

List all intent records on the device. Returns the full set of NEWTRON_INTENT
entries from CONFIG_DB.

**Response (200):** Array of intent records

#### GET /network/{netID}/node/{device}/intent/tree

Get a tree view of the intent DAG. See [S7 Intent Tree](#intent-tree) for query parameters.

### Zombie Intents

A "zombie" intent is an intent record left behind by a failed operation. The
forward operation partially succeeded (wrote some CONFIG_DB entries) but failed
before completing. The zombie record captures what was partially applied so it
can be rolled back.

#### GET /network/{netID}/node/{device}/zombie

Read the current zombie intent (if any).

**Response (200):** Zombie intent record, or `null` if no zombie exists

#### POST /network/{netID}/node/{device}/rollback-zombie

Roll back the zombie intent by reversing its partial changes.

**Query parameters:** `dry_run`, `no_save`

When `dry_run=true`, returns a preview of what would be reversed without applying.

**Request body:** none

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/clear-zombie

Clear the zombie intent without rolling back. Use when you have manually cleaned
up the partial changes or when rollback is not needed.

**Request body:** none

**Response (200):** `null` data on success

### History

#### GET /network/{netID}/node/{device}/history

Read the operation history for a device.

**Response (200):** Operation history records

#### POST /network/{netID}/node/{device}/rollback-history

Roll back the most recent operation from history.

**Query parameters:** `dry_run`, `no_save`

When `dry_run=true`, returns a preview of what would be reversed.

**Request body:** none

**Response (200):** `WriteResult`

### Device Settings

#### GET /network/{netID}/node/{device}/settings

Read device-level settings.

**Response (200):** `DeviceSettings`

```json
{"data": {"max_history": 10}}
```

#### PUT /network/{netID}/node/{device}/settings

Write device-level settings.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `max_history` | integer | yes | Maximum number of history entries to keep |

**Response (200):** The written settings object

### Drift Detection

#### GET /network/{netID}/node/{device}/drift

Detect configuration drift on a single device. Compares the device's actual
CONFIG_DB state against the expected state derived from intent records and
topology provisioning.

**Response (200):** Drift detection result

#### GET /network/{netID}/drift

Detect configuration drift across all devices in the network. Connects to each
device and checks for drift.

**Response (200):** Network-wide drift detection result

---

## 12. Composite Operations

The composite workflow is a three-phase process for generating, verifying, and
delivering complete device configurations. It separates intent (generate) from
validation (verify) from effect (deliver), giving callers control over each step.

The flow:

1. **Generate** -- builds a `CompositeInfo` from the abstract node model and
   returns a UUID handle. No device connection needed.
2. **Verify** (optional) -- connects to the device, reads current CONFIG_DB,
   and compares against the composite. Reports drift.
3. **Deliver** -- connects to the device, locks CONFIG_DB, writes the composite,
   and unlocks. The handle is consumed (one-time use).

Composite handles expire after 10 minutes. An expired handle returns 500.

### POST /network/{netID}/node/{device}/generate-composite

Generate a composite CONFIG_DB for the device and store it under a UUID handle.

**Request body:** none

**Response (200):** `CompositeHandleResponse`

```json
{
  "data": {
    "handle": "a1b2c3d4e5f6...",
    "device_name": "switch1",
    "entry_count": 42,
    "tables": {"VLAN": 3, "BGP_GLOBALS": 1, "INTERFACE": 8}
  }
}
```

### POST /network/{netID}/node/{device}/verify-composite

Verify a generated composite against current device state.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `handle` | string | yes | UUID from generate step |

**Response (200):** `VerificationResult` (see [S15](#verificationresult))

### POST /network/{netID}/node/{device}/deliver-composite

Deliver a generated composite to the device. Overwrites or merges CONFIG_DB
entries. The handle is removed after successful delivery.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `handle` | string | yes | UUID from generate step |
| `mode` | string | no | `"overwrite"` (default) or `"merge"`. Overwrite removes stale keys; merge only adds. |

**Response (200):** `DeliveryResult` (see [S15](#deliveryresult))

**Example:**

```
POST /network/default/node/switch1/deliver-composite
{"handle": "a1b2c3d4e5f6...", "mode": "overwrite"}
```

**Example response:**

```json
{
  "data": {
    "applied": 42,
    "skipped": 0,
    "failed": 0
  }
}
```

---

## 13. Interface Operations

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
| Port property | `set-port-property` | `property`, `value` |

All endpoints use `POST` method.

### Service Lifecycle

The three core service operations: apply, remove, refresh. These are the most
frequently used endpoints in the API -- most network automation workflows center
on applying services to interfaces.

#### POST /network/{netID}/node/{device}/interface/{name}/apply-service

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
POST /network/default/node/switch1/interface/Ethernet0/apply-service
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

#### POST /network/{netID}/node/{device}/interface/{name}/remove-service

Remove the service binding from the interface. Tears down all CONFIG_DB
infrastructure that was created by `apply-service`, using the stored binding
(not the current spec) to determine what to remove.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/interface/{name}/refresh-service

Refresh the service binding -- removes the current configuration and re-applies
from the current spec. Use after spec changes to update a running service
without manual remove+apply.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### Interface Configuration

#### POST /network/{netID}/node/{device}/interface/{name}/configure-interface

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

#### POST /network/{netID}/node/{device}/interface/{name}/unconfigure-interface

Remove all configuration from an interface (VRF binding, IP addresses, VLAN
membership). Returns the interface to its unconfigured state.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### ACL Binding

#### POST /network/{netID}/node/{device}/interface/{name}/bind-acl

Bind an ACL to the interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name |
| `direction` | string | yes | `"ingress"` or `"egress"` |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/interface/{name}/unbind-acl

Unbind an ACL from the interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `acl` | string | yes | ACL table name to unbind |

**Response (200):** `WriteResult`

### MAC-VPN Binding

#### POST /network/{netID}/node/{device}/interface/{name}/bind-macvpn

Bind a MAC-VPN to the interface (creates VLAN, VNI mapping, VXLAN tunnel map).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `macvpn` | string | yes | MAC-VPN spec name |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/interface/{name}/unbind-macvpn

Unbind the MAC-VPN from the interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### BGP Peer

#### POST /network/{netID}/node/{device}/interface/{name}/add-bgp-peer

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

#### POST /network/{netID}/node/{device}/interface/{name}/remove-bgp-peer

Remove the BGP peer from this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### QoS

#### POST /network/{netID}/node/{device}/interface/{name}/apply-qos

Apply a QoS policy to this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `policy` | string | yes | QoS policy name from specs |

**Response (200):** `WriteResult`

#### POST /network/{netID}/node/{device}/interface/{name}/remove-qos

Remove the QoS policy from this interface.

**Query parameters:** `dry_run`, `no_save`

**Request body:** none

**Response (200):** `WriteResult`

### Port Property

#### POST /network/{netID}/node/{device}/interface/{name}/set-port-property

Set a property on the interface (e.g., `mtu`, `admin_status`, `speed`).

**Query parameters:** `dry_run`, `no_save`

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `property` | string | yes | Property name (e.g., `"mtu"`, `"admin_status"`) |
| `value` | string | yes | Property value |

**Response (200):** `WriteResult`

---

## 14. Batch Execution

The batch execute endpoint runs multiple operations within a single SSH connection
and ChangeSet. All operations share one Lock -> operations -> Commit -> Save -> Unlock
cycle, reducing round trips and ensuring atomicity.

### POST /network/{netID}/node/{device}/execute

Execute a batch of operations.

**Request body:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operations` | Operation[] | yes | List of operations (see below) |
| `execute` | boolean | yes | `true` to apply, `false` for dry-run preview |
| `no_save` | boolean | no | Skip config save after apply |

Note: this endpoint reads `execute` and `no_save` from the request body, not
from query parameters.

**Operation object:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action` | string | yes | Action name (see dispatch table below) |
| `interface` | string | conditional | Required for interface-scoped actions |
| `params` | object | conditional | Action-specific parameters |

**Response (200):** `WriteResult` (aggregate of all operations)

If any operation fails, execution stops and the error includes the failed action name.

### Action Dispatch Table

**Node actions** (no `interface` field needed):

| Action | Params | Description |
|--------|--------|-------------|
| `create-vlan` | `id`, `description`, `vni` | Create a VLAN |
| `delete-vlan` | `id` | Delete a VLAN |
| `configure-irb` | `vlan_id`, `vrf`, `ip_address`, `anycast_mac` | Configure an IRB |
| `create-vrf` | `name` | Create a VRF |
| `delete-vrf` | `name` | Delete a VRF |
| `create-acl` | `name`, `type`, `stage`, `ports`, `description` | Create an ACL table |
| `delete-acl` | `name` | Delete an ACL table |
| `create-portchannel` | `name`, `members`, `mtu`, `min_links`, `fallback`, `fast_rate` | Create a PortChannel |
| `delete-portchannel` | `name` | Delete a PortChannel |
| `node-bind-macvpn` | `vlan_id`, `vni` | Bind MAC-VPN (node-level) |
| `node-unbind-macvpn` | `vlan_id` | Unbind MAC-VPN (node-level) |

**Interface actions** (`interface` field required):

| Action | Params | Description |
|--------|--------|-------------|
| `apply-service` | `service`, `ip_address`, `peer_as`, `vlan_id`, `route_reflector_client`, `next_hop_self` | Apply service to interface |
| `remove-service` | none | Remove service from interface |
| `refresh-service` | none | Refresh service on interface |
| `unconfigure-interface` | none | Unconfigure interface |
| `configure-interface` | `vrf`, `ip`, `vlan_id`, `tagged` | Configure interface |
| `bind-acl` | `acl`, `direction` | Bind ACL |
| `unbind-acl` | `acl` | Unbind ACL |
| `bind-macvpn` | `macvpn` | Bind MAC-VPN |
| `unbind-macvpn` | none | Unbind MAC-VPN |
| `set-port-property` | `property`, `value` | Set interface property |
| `apply-qos` | `policy` | Apply QoS policy |
| `remove-qos` | none | Remove QoS policy |
| `add-bgp-peer` | `neighbor_ip`, `remote_as`, `description`, `multihop` | Add BGP peer |
| `remove-bgp-peer` | none | Remove BGP peer |

**Example:**

```json
POST /network/default/node/switch1/execute
{
  "execute": true,
  "no_save": false,
  "operations": [
    {"action": "create-vlan", "params": {"id": 100}},
    {"action": "create-vrf", "params": {"name": "CUSTOMER"}},
    {
      "action": "apply-service",
      "interface": "Ethernet0",
      "params": {"service": "customer-l3", "ip_address": "10.1.1.1/30"}
    }
  ]
}
```

---

## 15. Types Reference

All request and response types used across the API, grouped by domain. Types are
defined in `pkg/newtron/types.go` (public API) and `pkg/newtron/api/types.go`
(HTTP layer).

### Write Result Types

These types are returned by all device write operations (S8, S13).

#### WriteResult

| Field | Type | Description |
|-------|------|-------------|
| `preview` | string (optional) | Human-readable diff preview. Present only on dry-run; absent (not empty string) otherwise. |
| `change_count` | integer | Number of CONFIG_DB changes |
| `applied` | boolean | Whether changes were committed to Redis |
| `verified` | boolean | Whether post-apply verification passed |
| `saved` | boolean | Whether `config save` was run |
| `verification` | VerificationResult (optional) | Detailed verification outcome. Absent (not null) on dry-run or when verification is skipped. |

#### VerificationResult

Also returned standalone by composite verify (`POST .../verify-composite`, S12)
and embedded in `DeliveryResult` from composite deliver.

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

### Device Settings Types

#### DeviceSettings

Returned by `GET .../settings` and accepted by `PUT .../settings`.

| Field | Type | Description |
|-------|------|-------------|
| `max_history` | integer | Maximum number of history entries to keep |

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

### Composite Types

#### CompositeHandleResponse

Returned by `POST .../generate-composite`.

| Field | Type | Description |
|-------|------|-------------|
| `handle` | string | UUID for subsequent verify/deliver calls |
| `device_name` | string | Device name |
| `entry_count` | integer | Total entries |
| `tables` | object | Table name -> entry count map |

#### DeliveryResult

Returned by `POST .../deliver-composite`.

| Field | Type | Description |
|-------|------|-------------|
| `applied` | integer | Number of entries written |
| `skipped` | integer | Number of entries skipped |
| `failed` | integer | Number of entries that failed |

### Provisioning Types

#### ProvisionResult

Returned by `POST /network/{netID}/provision`. Contains per-device results
wrapped in a `Results` array.

| Field | Type | Description |
|-------|------|-------------|
| `Results` | ProvisionDeviceResult[] | Per-device outcomes |

Each `ProvisionDeviceResult` entry:

| Field | Type | Description |
|-------|------|-------------|
| `Device` | string | Device name |
| `Applied` | integer | Number of entries applied |
| `Err` | error | Always `null` in JSON (see note) |

**Example response:**

```json
{
  "data": {
    "Results": [
      {"Device": "switch1", "Applied": 42, "Err": null},
      {"Device": "switch2", "Applied": 38, "Err": null}
    ]
  }
}
```

Note: Both `ProvisionResult` and `ProvisionDeviceResult` lack JSON struct tags,
so fields serialize with Go-style capitalization (`Results`, `Device`, `Applied`).
The `Err` field is a Go `error` interface, which always serializes to `null` in
JSON (Go's `error` interface does not implement `json.Marshaler`). On device-level
failure, the error details appear in the top-level `error` field of the
`APIResponse` envelope.

### SSH Command Types

#### SSHCommandResponse

Returned by `POST .../ssh-command`.

| Field | Type | Description |
|-------|------|-------------|
| `output` | string | Command output text |

### Network Registration Types

#### NetworkInfo

Returned in array by `GET /network`.

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Network identifier |
| `spec_dir` | string | Spec directory path |
| `has_topology` | boolean | Whether a topology file was loaded |
| `nodes` | string[] | Device names from topology |

### Cleanup Types

The `CleanupSummary` type exists in the Go API (`pkg/newtron/types.go`) but the
HTTP handler discards it -- `POST .../cleanup` returns the standard `WriteResult`
from the Execute cycle. The cleanup details (which orphaned resources were found)
are not currently exposed via HTTP.

---

## 16. Error Reference

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

## 17. Server Configuration

### Binary Flags

The `newtron-server` binary accepts these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | Listen address (host:port) |
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

### Composite Handle Expiry

Generated composite handles expire after **10 minutes**. An attempt to verify or
deliver an expired handle returns 500 with message
`"composite handle '<uuid>' has expired"`.

### Graceful Shutdown

On SIGINT or SIGTERM, the server:
1. Stops accepting new connections
2. Closes all cached SSH connections (stops all NodeActors)
3. Waits up to 10 seconds for in-flight requests to complete
4. Exits
