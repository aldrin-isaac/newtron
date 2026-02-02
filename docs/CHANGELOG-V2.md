# What Changed in v2

This document describes all changes between v1 and v2 of the Newtron documentation suite.
All nine documents are covered below, organized into the three documentation areas:
core Newtron docs, labgen docs (new in v2), and E2E testing docs.

---

## 1. docs/HLD.md (High-Level Design)

**Lines:** 583 (v1) --> 1019 (v2) | **Size:** 21 KB --> 46 KB

**Summary:** The HLD was expanded from a general architecture overview to a comprehensive
design document covering SSH-tunneled Redis access, StateDB, config persistence, three-tier
verification, lab generation, and testing architecture. The document nearly doubled in size.

### Key Changes

- Added SSH-tunneled Redis access as the primary device connection mechanism, replacing
  the assumption of direct Redis access on port 6379
- Added SSH credential fields (`ssh_user`, `ssh_pass`) to the DeviceProfile and
  ResolvedProfile descriptions
- Added StateDB (Redis DB 6) as a read-only operational state source alongside ConfigDB
  (Redis DB 4)
- Added config persistence model documenting the distinction between runtime Redis
  state and persistent `/etc/sonic/config_db.json`
- Added three-tier verification strategy with concrete code examples for each tier
  (CONFIG_DB hard fail, ASIC_DB topology-dependent, data-plane soft fail)
- Added labgen pipeline overview showing topology YAML to generated artifacts flow
- Added testing architecture section with build tag separation across unit, integration,
  and E2E tiers
- Expanded the Layer Diagram to show SSH tunnel and StateDB components within the
  Device Layer box
- Added six design decisions with rationale (SSH tunnel vs direct Redis, dry-run default,
  build tags, precondition checking, fresh connections for verification, non-fatal StateDB)
- Expanded Glossary with Connection Types (SSH Tunnel, Direct Redis), Redis Databases
  (DB 1 ASIC_DB, DB 4 CONFIG_DB, DB 6 STATE_DB), and Operations terms (Baseline Reset,
  Device Lock)

### Sections Added

- **Section 5: Device Connection Architecture** -- Connection flow diagram, SSH tunnel
  implementation details (NewSSHTunnel, acceptLoop, forward, bidirectional io.Copy),
  disconnect sequence with tunnel cleanup ordering
- **Section 6: StateDB Access** -- STATE_DB overview, available state tables
  (PORT_TABLE, LAG_TABLE, BGP_NEIGHBOR_TABLE, VXLAN_TUNNEL_TABLE), non-fatal connection
  failure semantics, RefreshState vs Reload methods
- **Section 7: Config Persistence** -- Runtime vs persistent config diagram, Newtron's
  write-to-Redis-only role, `config save -y` operator responsibility, implications table
- **Section 11: Security Model** -- Transport security (SSH for Redis, no Redis auth,
  InsecureIgnoreHostKey for labs), permission levels table, audit logging fields
- **Section 13: Three-Tier Verification Strategy** -- Detailed per-tier documentation
  with code patterns, failure mode tables, and tier summary matrix
- **Section 14: Lab Architecture** -- Lab generation pipeline diagram, topology YAML
  format, node types (spine/leaf/server with vrnetlab/netshoot), generated artifact
  descriptions
- **Section 15: Testing Architecture** -- Three test tiers with build tags, integration
  test Redis container setup, E2E test patterns (SSH-tunneled Redis, shared tunnel pool,
  fresh connections, baseline reset, cleanup strategy, test tracking/reporting)
- **Section 17: Design Decisions** -- Six numbered decisions with rationale

### Sections Updated

- **Section 3.1: Layer Diagram** -- Added SSH Tunnel and StateDBClient boxes within the
  Device Layer, showing the conditional SSH path when SSHUser+SSHPass are set
- **Section 4.4: Device Layer** -- Added SSHTunnel, StateDB, and StateDBClient to the
  key components list; updated Device description to mention tunnel, stateClient, and
  mutex
- **Section 4.2: Network Layer** -- No structural change
- **Section 9: Specification Files** -- Added `ssh_user` / `ssh_pass` to the Single
  Source of Truth table; added configlets directory to the file structure tree
- **Appendix A: Glossary** -- Added Connection Types, Redis Databases, and Operations
  subsections with 8 new terms

### Sections Removed

- None. All v1 sections were preserved and expanded.

---

## 2. docs/LLD.md (Low-Level Design)

**Lines:** 2730 (v1) --> 2730 (v2, being rewritten concurrently) | **Size:** 92 KB

**Summary:** The LLD is undergoing concurrent rewriting to incorporate SSH tunnel
infrastructure, StateDB client, expanded ConfigDB tables, and updated testing patterns.
The following changes are planned or partially applied.

### Key Changes

- Added `pkg/device/tunnel.go` -- SSHTunnel struct with NewSSHTunnel, LocalAddr, Close,
  acceptLoop, and forward methods for SSH port-forwarding to Redis
- Added `pkg/device/statedb.go` -- StateDB struct with 13 tables
  (PortTable, VlanTable, VrfTable, InterfaceTable, LoopbackInterfaceTable,
  PortChannelTable, PortChannelMemberTable, VxlanTunnelTable, AclTable, AclRuleTable,
  BgpNeighborTable, VxlanTunnelMapTable, VxlanEvpnNvoTable) and StateDBClient methods
- Updated Device struct (`pkg/device/device.go`) to include tunnel (*SSHTunnel),
  stateClient (*StateDBClient), and StateDB (*StateDB) fields alongside the existing
  configDB and redisClient
- Updated ResolvedProfile to include SSHUser and SSHPass fields, populated from
  DeviceProfile during inheritance resolution
- Added NEWTRON_SERVICE_BINDING table to ConfigDB for tracking which service is bound
  to each interface
- Added new ConfigDB table types: SAG_GLOBAL (Static Anycast Gateway), BGP_GLOBALS,
  BGP_GLOBALS_AF, BGP_EVPN_VNI, Scheduler, Queue, WREDProfile, and other QoS tables
- Added DeviceState BGP and EVPN state structs for merged state from CONFIG_DB and
  STATE_DB
- Added config persistence documentation explaining Newtron's write-to-Redis-only
  behavior
- Added labgen package to the package structure tree

### Sections Added (Planned)

- **SSH Tunnel Implementation** -- SSHTunnel struct, creation flow, forwarding flow,
  lifecycle management
- **StateDB Client** -- StateDBClient struct, Connect, GetAll, table parsing, non-fatal
  failure handling
- **Config Persistence** -- Runtime vs persistent model within the device layer context
- **labgen Package** -- Package structure entry for `pkg/labgen/` and `pkg/configlet/`

### Sections Updated (Planned)

- **Section 3.3: Low-Level Device Types** -- Add tunnel, stateClient, StateDB fields
  to Device struct
- **Section 2: Package Structure** -- Add `pkg/labgen/`, `pkg/configlet/`, and
  `cmd/labgen/` entries
- **Section 9: Redis Integration** -- Rewrite to document SSH tunnel path when
  credentials are present and direct path otherwise
- **Section 3.4: ConfigDB Mapping** -- Add SagGlobalEntry, new BGP tables, QoS tables
- **Testing strategy** -- Update to document three-tier assertions, build tags,
  LabSonicNodes

### Sections Removed

- None planned.

---

## 3. docs/HOWTO.md (HOWTO Guide)

**Lines:** 1699 (v1) --> 2599 (v2) | **Size:** 43 KB --> 74 KB

**Summary:** The HOWTO was expanded with a new Connection Architecture section explaining
SSH tunnels, added lab environment documentation with labgen integration, and added
config persistence warnings. Redis access instructions were updated throughout to
reflect the SSH tunnel path.

### Key Changes

- Added complete Connection Architecture section (Section 2) explaining SSH tunnels,
  why they are needed (port 6379 not forwarded by QEMU), SSH credentials in device
  profiles, and the Connect() code flow with both tunnel and direct paths
- Added host key verification documentation noting InsecureIgnoreHostKey for labs and
  the need for production hardening
- Added dual database access explanation (CONFIG_DB on DB 4, STATE_DB on DB 6) with
  non-fatal STATE_DB semantics
- Added ssh_user/ssh_pass to device profile documentation in Section 3.5 (Device
  Profiles) and Section 3.6 (Profile Resolution)
- Added Config Persistence section (Section 16) warning that Newtron writes only to
  runtime Redis and changes are lost on reboot without `config save -y`
- Added Lab Environment section (Section 17) with labgen build instructions, lab-start
  lifecycle, lab-status checking, and topology switching
- Added State DB Queries section (Section 13) documenting operational state access
- Added Build Lab Tools subsection under Installation (Section 1.3)

### Sections Added

- **Section 2: Connection Architecture** -- How Newtron talks to devices (2.1), why SSH
  tunnels (2.2), SSH credentials in profiles (2.3), Connect() code reference (2.4), host
  key verification (2.5), dual database access (2.6)
- **Section 13: State DB Queries** -- Querying operational state from STATE_DB
- **Section 16: Config Persistence** -- Runtime vs persistent config, `config save -y`
  requirement, implications for operators
- **Section 17: Lab Environment** -- Lab tools build, lab lifecycle (start/stop/status),
  topology switching, generated output structure
- **Section 1.3: Build Lab Tools** -- `make labgen` and `make build` instructions

### Sections Updated

- **Section 3.5: Device Profiles** -- Added ssh_user and ssh_pass to the optional fields
  table with descriptions
- **Section 3.6: Profile Resolution** -- Added SSH credentials to the "From profile"
  list in the ResolvedProfile description
- **Section 4: Basic Usage** -- Table of Contents expanded from 16 to 21 items to
  reflect the new sections
- **Section 5: Service Management** -- ApplyService step list expanded to include
  NEWTRON_SERVICE_BINDING tracking (step 10)
- **Section 9: EVPN/VXLAN Configuration** -- No structural changes, but context
  references updated
- **Section 18: Troubleshooting** -- Renumbered from previous position; content
  preserved from v1
- **Section 21: Related Documentation** -- Updated link list

### Sections Removed

- None. All v1 sections were preserved and renumbered.

---

## 4. docs/labgen/hld.md (labgen High-Level Design)

**Lines:** 374 (v2, new) | **Size:** ~15 KB

**Summary:** Entirely new document. Describes the purpose, architecture, and design of
the labgen code-generation tool that transforms topology YAML into containerlab
deployment artifacts.

### Content

- **Section 1: Purpose** -- Defines labgen as a topology-to-artifacts generator;
  lists the four output categories (config_db.json, frr.conf, clab.yml, specs)
- **Section 2: Scope** -- In-scope/out-of-scope table distinguishing generation from
  deployment, runtime management, and image building
- **Section 3: Architecture Overview** -- Generation pipeline diagram showing
  LoadTopology -> validateTopology -> four parallel generators (configdb_gen, frr_gen,
  clab_gen, specs_gen); four generators table with source files and responsibilities
- **Section 4: Input Format** -- Topology YAML structure with five top-level sections
  (name, defaults, network, nodes, links, role_defaults); node roles table; configlet
  reference mechanism
- **Section 5: Output Artifacts** -- Per-node outputs (config_db.json, frr.conf),
  topology-level output (clab.yml), spec outputs (network/site/platforms/profiles JSON)
- **Section 6: Configlet System** -- Template format with `{{variable}}` placeholders;
  role-based defaults; deep merge semantics at table/key/field level; available
  configlets table (sonic-baseline, sonic-evpn-leaf, sonic-evpn-spine, sonic-acl-copp,
  sonic-qos-8q, sonic-evpn)
- **Section 7: Container Images** -- sonic-vm (vrnetlab QEMU), sonic-vs (native),
  linux (netshoot); kind auto-detection from image name
- **Section 8: FRR Configuration** -- Why FRR is generated separately
  (frr_split_config_enabled); spine route reflector config (LEAF-PEERS, cluster-id,
  route-reflector-client); leaf EVPN VTEP config (SPINE peer-group, advertise-all-vni);
  intentional omission of bgp suppress-fib-pending with rationale
- **Section 9: Network Addressing** -- Fabric link /31 assignment from 10.1.0.0/24;
  loopback IP usage; deterministic system MAC generation (02:42:f0:ab:XX:XX)
- **Section 10: Integration Points** -- containerlab deploy/destroy; setup.sh lab-start
  8-step orchestration sequence; newtron specs consumption path
- **Appendix: File Layout** -- Complete directory tree for cmd/labgen, pkg/labgen,
  pkg/configlet, configlets/, and testlab/topologies/

### Sections Added

- All sections are new (no v1 existed).

### Sections Updated

- N/A (new document).

### Sections Removed

- N/A (new document).

---

## 5. docs/labgen/lld.md (labgen Low-Level Design)

**Lines:** 901 (v2, new) | **Size:** ~35 KB

**Summary:** Entirely new document. Provides complete Go type definitions, function
signatures, and implementation details for all labgen packages.

### Content

- **Section 1: Package Structure** -- File-by-file listing of cmd/labgen/ and pkg/labgen/
  with one-line descriptions
- **Section 2: Data Structures** -- Full Go struct definitions for Topology,
  TopologyDefaults, TopologyNetwork, NodeDef, LinkDef, ClabTopology/ClabNode/ClabLink/
  ClabHealthcheck, FabricLinkIP, and Configlet; field-by-field documentation
- **Section 3: Generation Pipeline** -- Entry point (main.go) with flag parsing and
  generation sequence; Step 1: LoadTopology with 12 validation rules; Step 2:
  GenerateStartupConfigs with buildNodeConfigDB, buildVarsMap (variable source table),
  addPortEntries, addInterfaceEntries, nodeMAC, ComputeFabricLinkIPs, and
  SonicIfaceToClabIface; Step 3: GenerateFRRConfigs with complete FRR config templates
  for leaf and spine including all BGP address families; Step 4: GenerateClabTopology
  with resolveKind, kindFromImage, buildSequentialIfaceMaps (QEMU NIC remapping), node
  generation by kind, and link translation
- **Section 3.5 (continued):** GenerateLabSpecs producing network.json, site.json,
  platforms.json, and per-node profile JSON with is_route_reflector for spines
- **Determinism guarantees** documented throughout: sorted node names for MAC assignment,
  sorted peers for FRR config, sorted interfaces for sequential mapping

### Sections Added

- All sections are new (no v1 existed).

### Sections Updated

- N/A (new document).

### Sections Removed

- N/A (new document).

---

## 6. docs/labgen/howto.md (labgen HOWTO Guide)

**Lines:** 666 (v2, new) | **Size:** ~22 KB

**Summary:** Entirely new document. Practical guide for using labgen, creating topologies,
working with configlets, and troubleshooting generation issues.

### Content

- **Section 1: Quick Start** -- Direct invocation (`go run ./cmd/labgen`), Makefile
  targets (`make lab-start`, `make labgen`), generated output directory structure
- **Section 2: Creating a Topology** -- Complete YAML example with 4 SONiC nodes and
  2 servers; field reference table covering all topology YAML fields; node roles guide;
  link format documentation
- **Section 3: Configlet System** -- Creating new configlets; available variables table
  (10 auto-populated variables from topology); role defaults vs per-node configlet
  overrides; deep merge behavior with before/after examples
- **Section 4: Customizing Output** -- Adding custom PORT entries via configlets;
  adding custom variables per node; overriding defaults (image, platform) per node
- **Section 5: Working with Generated Output** -- Directory structure; how config_db.json
  is used (sonic-vm startup-config vs sonic-vs bind mount); how frr.conf is pushed
  (SCP + docker cp + vtysh -f + write memory); how specs are consumed by newtron
- **Section 6: Adding a New Node Type** -- Modifying parse.go validation, frr_gen.go
  for new role FRR generation, specs_gen.go for profile fields; creating configlets
  for a new role
- **Section 7: Troubleshooting** -- Seven scenarios: "configlet not found" (path
  resolution order), "interface mapping mismatch" (sequential mapping for sonic-vm),
  "PLACEHOLDER in profile" (lab_patch_profiles), "missing loopback_ip", "FRR config
  not loading", "kind detection wrong" (auto-detect vs explicit override), "configlet
  variable not substituted"

### Sections Added

- All sections are new (no v1 existed).

### Sections Updated

- N/A (new document).

### Sections Removed

- N/A (new document).

---

## 7. docs/testing/e2e-hld.md (E2E Testing High-Level Design)

**Lines:** ~350 (v1) --> 514 (v2) | **Size:** 14 KB --> 21 KB

**Summary:** The E2E testing HLD was significantly expanded with SSH-tunneled Redis
access documentation, three-tier assertion strategy, SSH tunnel pool architecture,
baseline reset mechanism, and a numbered design decisions section.

### Key Changes

- Added SSH-tunneled Redis as the sole access path for E2E tests, replacing the implicit
  assumption of direct Redis access
- Added the SSH tunnel pool architecture (labTunnels map with mutex, per-node tunnel
  reuse, CloseLabTunnels in TestMain)
- Added QEMU port forwarding explanation referencing vrnetlab.py mgmt_tcp_ports list
  (port 6379 absent, port 22 forwarded separately)
- Added three-tier assertion strategy with per-tier documentation:
  control-plane (hard fail), ASIC convergence (topology-dependent), state-plane
  (soft fail), data-plane (soft fail), health-check (hard fail)
- Added WaitForASICVLAN as the ASIC convergence polling mechanism with convergence
  behavior table by topology complexity
- Added ResetLabBaseline mechanism (containerlab inspect, SSH to each node, redis-cli
  DEL of staleE2EKeys, 5-second orchagent settling)
- Added LabSonicNodes vs LabNodes distinction (SONiC nodes with profiles vs all nodes
  including servers)
- Added 10 numbered design decisions section (build tags, no external test framework,
  Validate->Execute pattern, fresh connections, cleanup via raw Redis, three-tier
  assertions, SSH-tunneled Redis, baseline reset, device locking, LabSonicNodes vs
  LabNodes)

### Sections Added

- **Section 4.6.1: LabSonicNodes vs LabNodes** -- Profile-file-based filtering to
  exclude server containers from SONiC-specific operations
- **Section 5: Test Taxonomy** -- Five test categories (control-plane, ASIC convergence,
  state-plane, data-plane, health-check) with assertion patterns and failure modes
- **Section 6.3: Redis Access Path** -- Complete packet path diagram from test process
  through SSH tunnel to Redis inside QEMU VM, with shell script equivalent
- **Section 6.4: SSH Tunnel Pool** -- Pool data structure, creation/reuse flow,
  CloseLabTunnels lifecycle
- **Section 9: Design Decisions** -- 10 numbered decisions with rationale paragraphs
- **Section 9.7: SSH-tunneled Redis access** -- vrnetlab.py mgmt_tcp_ports list,
  Go tunnel path, shell script path, tunnel pooling
- **Section 9.8: Baseline reset** -- 5-step ResetLabBaseline sequence
- **Section 9.10: LabSonicNodes vs LabNodes** -- Design rationale for two discovery
  functions

### Sections Updated

- **Section 3: System Context** -- Diagram updated to show SSH tunnel pool, random
  local port forwarding, and the LabRedisClient path through the tunnel
- **Section 4.5: Lab Management** -- Added lab_wait_redis SSH-based implementation
  note; added lab_push_frr, lab_bridge_nics, lab_patch_profiles steps to the lifecycle
- **Section 4.6: Test Utilities** -- Added lab.go and report.go to the file table;
  added SSH tunnel pool functions (labSSHConfig, labTunnelAddr, CloseLabTunnels) to
  the API summary
- **Section 6.1: TestMain Setup Flow** -- Added InitReport, ResetLabBaseline,
  CloseLabTunnels, and WriteReport to the flow diagram
- **Section 6.2: Test Execution Flow** -- Updated to show SSH tunnel creation path
  through LabConnectedDevice -> ConnectDevice -> SSH tunnel
- **Section 7: Network Addressing** -- Added test subnet ranges (10.70.0.0/24 for L2,
  10.80.0.0/24 for IRB, 10.90.x.0/30 for L3)
- **Section 8: Failure Modes** -- Added SSH tunnel creation failure, stale VXLAN/VRF
  key crashes, and ASIC_DB non-convergence rows

### Sections Removed

- None. All v1 sections were preserved and expanded.

---

## 8. docs/testing/e2e-lld.md (E2E Testing Low-Level Design)

**Lines:** ~800 (v1) --> 1133 (v2) | **Size:** 29 KB --> 41 KB

**Summary:** The E2E testing LLD was expanded with SSH tunnel implementation details,
QEMU port forwarding documentation, ASIC convergence verification, baseline reset
implementation with complete stale key list, and detailed test catalogs.

### Key Changes

- Added complete SSH tunnel implementation section (Section 7) with SSHTunnel struct
  definition, NewSSHTunnel creation flow, forward() bidirectional copy, and Close()
  lifecycle
- Added SSH tunnel pool implementation (Section 7.2) with labTunnelAddr flow,
  labSSHConfig credential reading, tunnel reuse logic, and CloseLabTunnels cleanup
- Added QEMU port forwarding section (Section 6) with vrnetlab.py mgmt_tcp_ports
  list showing port 6379 is NOT forwarded; port access method table
- Added ASIC convergence verification section (Section 12) with WaitForASICVLAN
  implementation (Redis DB 1 polling for SAI_OBJECT_TYPE_VLAN), convergence behavior
  table by topology
- Added ResetLabBaseline implementation (Section 12.2) with complete staleE2EKeys list
  (40+ keys covering VLAN, VRF, VXLAN_TUNNEL_MAP, ACL, LAG, and PORTCHANNEL entries
  from all test categories)
- Added TestMain implementation (Section 13) with exact code showing InitReport ->
  ResetLabBaseline -> m.Run -> CloseLabTunnels -> WriteReport -> os.Exit sequence
- Added test utility API reference with SSH-specific helpers: labSSHConfig,
  labTunnelAddr, CloseLabTunnels, WaitForASICVLAN, ResetLabBaseline, PollStateDB
- Added test ID allocation table (Section 15) mapping VLAN IDs 500-800, PortChannel
  200-203, VRF names, L2VNI values, and subnet ranges to specific test files
- Added cleanup strategy section (Section 18) with three cleanup methods:
  ASIC-dependent ordering (IP -> SVI -> VNI -> member -> VLAN -> VRF),
  LabCleanupChanges (operation-based via fresh connection), and raw Redis DEL
- Added ASIC_DB tables to Redis database schema (Section 11.4) with
  SAI_OBJECT_TYPE_VLAN key pattern
- Added STATE_DB tables to Redis database schema (Section 11.3) with
  BGP_NEIGHBOR_TABLE
- Added E2E report generation section (Section 9.5) with InitReport, WriteReport,
  Track, TrackComment, SetNode API; PARTIAL detection logic
- Added container naming convention (Section 17) with clab prefix stripping

### Sections Added

- **Section 6: QEMU Port Forwarding (vrnetlab)** -- mgmt_tcp_ports list, hostfwd
  configuration, port access method table
- **Section 7: SSH Tunnel Implementation** -- SSHTunnel struct, NewSSHTunnel, forward,
  tunnel pool, labTunnelAddr, labSSHConfig, CloseLabTunnels
- **Section 8.1: lab_wait_redis Implementation** -- Shell script SSH-based Redis polling
- **Section 8.2: lab_patch_profiles Implementation** -- Python script for mgmt_ip and
  SSH credential injection
- **Section 9.5: report.go** -- E2E test report API (InitReport, WriteReport, Track,
  TrackComment, SetNode, PARTIAL detection)
- **Section 12: ASIC Convergence Verification** -- WaitForASICVLAN implementation,
  convergence table, ResetLabBaseline with staleE2EKeys, CloseLabTunnels
- **Section 13: TestMain Implementation** -- Complete TestMain code with execution order
- **Section 15: Test ID Allocation** -- Resource ID ranges per test file
- **Section 17: Container Naming Convention** -- clab prefix, short name extraction
- **Section 18: Cleanup Strategy** -- Three cleanup methods with ordering requirements

### Sections Updated

- **Section 1: Repository Layout** -- Added tunnel.go, lab.go, report.go, frr.conf,
  .lab-state, e2e-report.md to the tree; added labgen package entries
- **Section 2: Dependencies** -- Added golang.org/x/crypto for SSH client; added sshpass
  to external tools; noted redis-cli is NOT needed on host
- **Section 9: Test Utility API Reference** -- Significantly expanded lab.go section
  with SSH tunnel pool functions, CONFIG_DB/STATE_DB/ASIC_DB assertion functions,
  ResetLabBaseline, server container helpers (ServerExec, ServerConfigureInterface,
  ServerPing, ServerCleanupInterface)
- **Section 11: Redis Database Schema** -- Added ASIC_DB tables (DB 1) and STATE_DB
  tables (DB 6) subsections; added WARM_RESTART and SUPPRESS_VLAN_NEIGH to CONFIG_DB
  table list
- **Section 14: E2E Test Catalog** -- Updated with exact test names, node targets, and
  CONFIG_DB tables modified per test; added dataplane_test.go catalog with server IPs
  and verification layers; added helper function signatures

### Sections Removed

- None. All v1 sections were preserved and expanded.

---

## 9. docs/testing/e2e-howto.md (E2E Testing HOWTO Guide)

**Lines:** 836 (v1) --> 835 (v2, being rewritten concurrently) | **Size:** 20 KB

**Summary:** The E2E testing HOWTO is undergoing concurrent rewriting to update Redis
access instructions for SSH tunnels, add debugging sections, and incorporate the
three-tier assertion strategy. The following changes are planned or partially applied.

### Key Changes (Planned)

- Rewrote Section 6 (Working with Redis) to document SSH tunnel access path instead
  of direct Redis access; added LabRedisClient, LabStateDBEntry, and tunnel-based
  redis.NewClient patterns
- Added SSH tunnel debugging guidance for connection failures (verify SSH is up,
  check container health, inspect CLOSE-WAIT issues)
- Added ASIC convergence debugging section with WaitForASICVLAN usage and per-topology
  convergence expectations
- Added config persistence warning in the debugging section noting that Redis changes
  are lost on reboot without `config save -y`
- Added three-tier assertion strategy table mapping scenarios (operation error, missing
  CONFIG_DB entry, BGP not converging, data-plane ping fails) to appropriate failure
  modes (t.Fatal vs t.Skip)
- Added LabSonicNodes vs LabNodes guidance explaining when to use each function
  (control-plane tests use LabSonicNodes; data-plane tests referencing servers use
  LabNodes)
- Updated Common Pitfalls (Section 11) with SSH-related issues: "Redis unreachable"
  (SSH tunnel failure, not direct port failure), "CLOSE-WAIT" TCP issues, stale tunnel
  state
- Updated Section 8 (Debugging Failures) with Redis state inspection commands using
  management IP, SSH into SONiC VM instructions, and Redis port forwarding verification
- Updated Section 12 (Reference) with port reference table showing port 6379 is NOT
  forwarded, SSH tunnel requirement, and SSH credentials per topology
- Updated related documentation links to include NGDP_DEBUGGING.md, SONIC_VS_PITFALLS.md,
  CONFIGDB_GUIDE.md, VERIFICATION_TOOLKIT.md, and DESIGN_PRINCIPLES.md

### Sections Added (Planned)

- **SSH tunnel debugging** subsection within Debugging Failures
- **ASIC convergence debugging** subsection within Debugging Failures
- **Config persistence warning** subsection

### Sections Updated (Planned)

- **Section 5: Writing New E2E Tests** -- Added three-tier failure mode guidance table;
  added multi-device and data-plane test patterns with LabSonicNodes/LabNodes usage
- **Section 6: Working with Redis** -- Rewritten for SSH tunnel access; added
  LabRedisClient, in-test Redis operations examples
- **Section 8: Debugging Failures** -- Expanded with SSH debugging, container inspection,
  Redis state verification, and CLOSE-WAIT checking
- **Section 11: Common Pitfalls** -- Added SSH-related pitfalls
- **Section 12: Reference** -- Added port reference table, timeout table, file paths
  table, SONiC VM credentials table

### Sections Removed

- None planned. All v1 sections preserved.

---

## Cross-Cutting Themes in v2

The following themes appear across multiple documents:

1. **SSH-tunneled Redis access** -- Documented in HLD (Section 5), HOWTO (Section 2),
   e2e-hld (Section 6.3, 9.7), e2e-lld (Section 6, 7), and e2e-howto (Section 6, 12).
   Port 6379 is not forwarded by QEMU; SSH on port 22 is the only path to Redis.

2. **Three-tier verification strategy** -- Documented in HLD (Section 13), e2e-hld
   (Section 5, 9.6), e2e-lld (Section 12), and e2e-howto (Section 5). CONFIG_DB
   assertions are hard failures; ASIC_DB is topology-dependent; data-plane is always
   soft fail on SONiC-VS.

3. **StateDB (DB 6)** -- Documented in HLD (Section 6), HOWTO (Section 13), and e2e-lld
   (Section 11.3). Non-fatal connection; provides operational state (interface status,
   BGP neighbor state) supplementing CONFIG_DB.

4. **Config persistence** -- Documented in HLD (Section 7), HOWTO (Section 16), and
   e2e-howto (planned). Newtron writes to runtime Redis only; `config save -y` is the
   operator's responsibility.

5. **labgen pipeline** -- Documented in HLD (Section 14), labgen/hld.md (all sections),
   labgen/lld.md (all sections), labgen/howto.md (all sections), and e2e-hld
   (Section 4.1). Single topology YAML produces all deployment artifacts.

6. **Baseline reset** -- Documented in HLD (Section 15.4), e2e-hld (Section 6.1, 9.8),
   and e2e-lld (Section 12.2). TestMain deletes stale CONFIG_DB keys via SSH before
   every test run to prevent orchagent crashes.
