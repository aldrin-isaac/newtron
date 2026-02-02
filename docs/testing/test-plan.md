# Newtron Test Plan

Formal test cases with success criteria covering functional, performance,
scale, and chaos testing scenarios.

## Table of Contents

- [1. Test Tiers](#1-test-tiers)
- [2. Functional Test Cases](#2-functional-test-cases)
  - [2.1 Connectivity](#21-connectivity)
  - [2.2 VLAN Operations](#22-vlan-operations)
  - [2.3 LAG Operations](#23-lag-operations)
  - [2.4 Interface Operations](#24-interface-operations)
  - [2.5 ACL Operations](#25-acl-operations)
  - [2.6 VRF and EVPN Operations](#26-vrf-and-evpn-operations)
  - [2.7 Service Operations](#27-service-operations)
  - [2.8 Multi-Device Operations](#28-multi-device-operations)
  - [2.9 Data-Plane Verification](#29-data-plane-verification)
- [3. Negative Test Cases](#3-negative-test-cases)
- [4. Performance Test Cases](#4-performance-test-cases)
- [5. Scale Test Cases](#5-scale-test-cases)
- [6. Chaos and Resilience Test Cases](#6-chaos-and-resilience-test-cases)
- [7. Regression Test Cases](#7-regression-test-cases)
- [8. Platform Capability Matrix](#8-platform-capability-matrix)
- [9. Test ID and Resource Allocation](#9-test-id-and-resource-allocation)
- [10. Success Criteria Summary](#10-success-criteria-summary)

---

## 1. Test Tiers

| Tier | Build Tag | Infrastructure | Scope | Run Time |
|---|---|---|---|---|
| Unit | (none) | None | Logic, parsing, validation | <10s |
| Integration | `integration` | Redis container | CONFIG_DB/STATE_DB read/write, device operations | <30s |
| E2E | `e2e` | Containerlab + SONiC VMs | Full stack against real SONiC | 1-2 min |
| Performance | `e2e,perf` | Containerlab | Throughput and latency benchmarks | 5-10 min |
| Scale | `e2e,scale` | Large topology | Resource limits, many objects | 10-30 min |
| Chaos | `e2e,chaos` | Containerlab | Failure injection, recovery | 5-15 min |

### Failure Mode Convention

| Category | Assertion | When |
|---|---|---|
| Control-plane (CONFIG_DB) | `t.Fatal` | Entry missing, wrong value, operation error |
| ASIC convergence | `t.Skip` on VS, `t.Fatal` on HW | ASIC_DB doesn't converge |
| Data-plane (ping) | `t.Skip` on VS, `t.Fatal` on HW | Ping fails |
| STATE_DB operational | `t.Skip` on VS for ASIC-dependent, `t.Fatal` for FRR-backed | State doesn't match |
| Performance threshold | `t.Fatal` if >2x baseline | Latency/throughput regression |

---

## 2. Functional Test Cases

### 2.1 Connectivity

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| CONN-01 | ConnectAllNodes | Lab running | Connect to each SONiC node via network.ConnectDevice | `IsConnected()` returns true, no error |
| CONN-02 | VerifyStartupConfig | CONN-01 | Read DEVICE_METADATA\|localhost | `hostname` matches node name from topology |
| CONN-03 | VerifyLoopbackInterface | CONN-01 | Read LOOPBACK_INTERFACE | `Loopback0` key exists |
| CONN-04 | ListInterfaces | CONN-01 | Call ListInterfaces() | Returns non-empty list, GetInterface() works on each |
| CONN-05 | VerifyBGPConfig | CONN-01 | Read BGP_GLOBALS\|default | `local_asn` and `router_id` match topology |
| CONN-06 | VerifyVTEPConfig | Leaf nodes | Read VXLAN_TUNNEL | `src_ip` matches loopback IP |
| CONN-07 | DisconnectReconnect | CONN-01 | Disconnect, reconnect | Second connect succeeds, state reloaded |

### 2.2 VLAN Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| VLAN-01 | CreateVLAN | Locked device | CreateVLANOp(ID=500) | CONFIG_DB: VLAN\|Vlan500 exists with correct fields |
| VLAN-02 | CreateVLAN_Idempotent | VLAN-01 | CreateVLANOp(ID=500) again | Validate returns error (already exists) |
| VLAN-03 | DeleteVLAN | VLAN-01 | DeleteVLANOp(ID=501) | CONFIG_DB: VLAN\|Vlan501 absent |
| VLAN-04 | DeleteVLAN_NonExistent | Clean state | DeleteVLANOp(ID=999) | Validate returns error (not found) |
| VLAN-05 | AddVLANMember_Tagged | VLAN exists | AddVLANMemberOp(tagged=true) | VLAN_MEMBER entry with tagging_mode=tagged |
| VLAN-06 | AddVLANMember_Untagged | VLAN exists | AddVLANMemberOp(tagged=false) | VLAN_MEMBER entry with tagging_mode=untagged |
| VLAN-07 | RemoveVLANMember | VLAN-05 | RemoveVLANMemberOp | VLAN_MEMBER entry absent |
| VLAN-08 | DeleteVLAN_WithMembers | VLAN+member | DeleteVLANOp | Validate fails (has members) |
| VLAN-09 | ConfigureSVI | VLAN exists, VRF exists | ConfigureSVIOp | VLAN_INTERFACE entries correct |
| VLAN-10 | ConfigureSVI_AnycastGW | VLAN+VRF | ConfigureSVIOp with anycast | VLAN_INTERFACE has ip, vrf_name |

### 2.3 LAG Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| LAG-01 | CreateLAG | Locked device | CreateLAGOp(PortChannel200) | PORTCHANNEL\|PortChannel200 exists |
| LAG-02 | DeleteLAG | LAG-01 | DeleteLAGOp | PORTCHANNEL entry absent |
| LAG-03 | AddLAGMember | LAG exists | AddLAGMemberOp | PORTCHANNEL_MEMBER entry exists |
| LAG-04 | AddLAGMember_AlreadyMember | LAG-03 | AddLAGMemberOp same port | Validate fails (already member) |
| LAG-05 | RemoveLAGMember | LAG-03 | RemoveLAGMemberOp | PORTCHANNEL_MEMBER absent |
| LAG-06 | DeleteLAG_WithMembers | LAG+member | DeleteLAGOp | Validate fails (has members) |
| LAG-07 | AddLAGMember_PortInVLAN | Port in VLAN | AddLAGMemberOp | Validate fails (port has VLAN) |

### 2.4 Interface Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| INTF-01 | ConfigureInterface | Locked device | ConfigureInterfaceOp(MTU, desc) | CONFIG_DB PORT entry updated |
| INTF-02 | SetInterfaceVRF | VRF exists | SetInterfaceVRFOp | INTERFACE\|port has vrf_name |
| INTF-03 | SetInterfaceIP | Locked device | SetInterfaceIPOp | INTERFACE\|port\|ip/mask exists |
| INTF-04 | SetInterfaceVRF_WithIP | VRF exists | SetInterfaceVRFOp with IP | Both VRF and IP entries correct |
| INTF-05 | ConfigureMTU_Verify | INTF-01 | Read CONFIG_DB PORT | mtu field matches written value |
| INTF-06 | SetInterfaceVRF_NonExistent | No VRF | SetInterfaceVRFOp | Validate fails (VRF not found) |

**Note on INTF-05:** On virtual switches, verify CONFIG_DB directly (not
`Interface.MTU()` which reads STATE_DB). STATE_DB may not reflect CONFIG_DB
for MTU because the ASIC simulator doesn't apply MTU to kernel TAP.

### 2.5 ACL Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| ACL-01 | CreateACLTable | Locked device | CreateACLTableOp | ACL_TABLE entry exists |
| ACL-02 | DeleteACLTable | ACL-01 | DeleteACLTableOp | ACL_TABLE entry absent |
| ACL-03 | AddACLRule | ACL exists | AddACLRuleOp | ACL_RULE entry with correct fields |
| ACL-04 | DeleteACLRule | ACL-03 | DeleteACLRuleOp | ACL_RULE entry absent |
| ACL-05 | BindACL | ACL exists | BindACLOp | ACL_TABLE ports field includes interface |
| ACL-06 | DeleteACLTable_WithRules | ACL+rules | DeleteACLTableOp | Validate fails (has rules) |
| ACL-07 | BindACL_NonExistent | No ACL | BindACLOp | Validate fails |
| ACL-08 | ACLRule_Priority | ACL exists | Add rules with different priorities | All rules present, correct ordering |

### 2.6 VRF and EVPN Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| VRF-01 | CreateVRF | Locked device | CreateVRFOp | VRF entry exists |
| VRF-02 | DeleteVRF | VRF-01 | DeleteVRFOp | VRF entry absent |
| VRF-03 | DeleteVRF_InUse | VRF+interface | DeleteVRFOp | Validate fails |
| EVPN-01 | CreateVTEP | Locked device | CreateVTEPOp | VXLAN_TUNNEL entry exists |
| EVPN-02 | MapL2VNI | VTEP+VLAN exist | MapL2VNIOp | VXLAN_TUNNEL_MAP entry correct |
| EVPN-03 | UnmapL2VNI | EVPN-02 | UnmapL2VNIOp | VXLAN_TUNNEL_MAP absent |
| EVPN-04 | MapL2VNI_NoVTEP | No VTEP | MapL2VNIOp | Validate fails |
| EVPN-05 | MapL2VNI_NoVLAN | VTEP, no VLAN | MapL2VNIOp | Validate fails |

### 2.7 Service Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| SVC-01 | ApplyService | Network+locked device | ApplyServiceOp | NEWTRON_SERVICE_BINDING, VRF, ACL entries |
| SVC-02 | RemoveService | SVC-01 | RemoveServiceOp | All service entries cleaned |
| SVC-03 | ApplyService_AlreadyBound | SVC-01 | ApplyServiceOp same interface | Validate fails |
| SVC-04 | RemoveService_NotBound | Clean interface | RemoveServiceOp | Validate fails |
| SVC-05 | ApplyService_SharedVRF | Two interfaces, shared VRF | Apply same service twice | Single VRF, both interfaces |
| SVC-06 | RemoveService_SharedVRF_LastUser | SVC-05, remove second | RemoveServiceOp | VRF deleted (last user) |
| SVC-07 | RemoveService_SharedVRF_NotLast | SVC-05, remove first | RemoveServiceOp | VRF preserved |

### 2.8 Multi-Device Operations

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| MULTI-01 | BGPNeighborState | Lab running, FRR configured | Poll STATE_DB for BGP state | All neighbors reach Established (soft-fail on VS) |
| MULTI-02 | VLANAcrossTwoLeaves | Two leaf devices | Create VLAN 600 on both | Both have VLAN\|Vlan600 |
| MULTI-03 | EVPNFabricHealth | Lab running | Run health checks on all | No critical failures |
| MULTI-04 | CrossLeafVNI | Two leaves, VTEP | Map same VNI on both | Both have VXLAN_TUNNEL_MAP |
| MULTI-05 | BGPRouteExchange | MULTI-01 passed | Check EVPN routes in FRR | Type-2/Type-5 routes present |

### 2.9 Data-Plane Verification

| ID | Test | Preconditions | Steps | Success Criteria |
|---|---|---|---|---|
| DP-01 | L2Bridged | Servers, VLAN+VNI on both leaves | Ping server1↔server2 | CONFIG_DB correct (hard); ping works (soft on VS) |
| DP-02 | IRBSymmetric | Servers, VRF+VLAN+SVI+VNI | Ping through anycast GW | CONFIG_DB correct (hard); ASIC converges (soft on VS); ping (soft) |
| DP-03 | L3Routed | Servers, VRF+L3 interfaces | Ping inter-subnet | CONFIG_DB correct (hard); ping (soft on VS) |
| DP-04 | L2_LocalSwitching | Server on same leaf, same VLAN | Ping within VLAN | L2 switching should work even on VS |
| DP-05 | ARPResolution | DP-01 setup | Check ARP table on servers | ARP entries present (if data plane works) |

---

## 3. Negative Test Cases

| ID | Test | Input | Expected Behavior |
|---|---|---|---|
| NEG-01 | InvalidVLANID | CreateVLAN(ID=0) | Validate returns error |
| NEG-02 | InvalidVLANID_High | CreateVLAN(ID=4095) | Validate returns error |
| NEG-03 | DuplicateVLAN | CreateVLAN twice | Second validate fails |
| NEG-04 | NonExistentDevice | Connect("nonexistent") | Connection error |
| NEG-05 | UnlockedWrite | Execute without Lock | Error (not locked) |
| NEG-06 | DisconnectedWrite | Execute without Connect | Error (not connected) |
| NEG-07 | InvalidInterface | SetInterfaceVRF("eth99") | Validate fails |
| NEG-08 | LAGMemberConflict | Add port to LAG when in VLAN | Validate fails |
| NEG-09 | VRFDeletionConflict | Delete VRF with bound interfaces | Validate fails |
| NEG-10 | ACLDeletionConflict | Delete ACL table with rules | Validate fails |
| NEG-11 | InvalidIPAddress | SetInterfaceIP("not-an-ip") | Validate fails |
| NEG-12 | OverlappingSubnet | Assign overlapping IPs | Validate fails |

---

## 4. Performance Test Cases

| ID | Test | Metric | Baseline | Threshold |
|---|---|---|---|---|
| PERF-01 | SingleVLANCreate | Latency | <500ms | <1000ms |
| PERF-02 | BulkVLANCreate_10 | Total time for 10 VLANs | <5s | <10s |
| PERF-03 | BulkVLANCreate_100 | Total time for 100 VLANs | <50s | <120s |
| PERF-04 | DeviceConnect | Connection + state load | <2s | <5s |
| PERF-05 | DeviceReconnect | Disconnect + reconnect | <2s | <5s |
| PERF-06 | FullStateLoad | Load all CONFIG_DB + STATE_DB | <3s | <8s |
| PERF-07 | ServiceApply | Apply complex service | <2s | <5s |
| PERF-08 | HealthCheckSuite | All health checks on one device | <5s | <15s |
| PERF-09 | RedisRoundTrip | Single HGETALL latency | <10ms | <50ms |
| PERF-10 | SSHTunnelSetup | Create SSH tunnel | <2s | <5s |

### Performance Test Implementation

```go
//go:build e2e && perf

func TestPerf_BulkVLANCreate(t *testing.T) {
    testutil.SkipIfNoLab(t)
    dev := testutil.LabLockedDevice(t, leafNodeName(t))
    ctx := testutil.LabContext(t)

    start := time.Now()
    for i := 0; i < 100; i++ {
        op := &operations.CreateVLANOp{ID: 900 + i}
        if err := op.Execute(ctx, dev); err != nil {
            t.Fatalf("VLAN %d: %v", 900+i, err)
        }
    }
    elapsed := time.Since(start)

    t.Logf("100 VLANs created in %v (avg %v/VLAN)", elapsed, elapsed/100)
    if elapsed > 120*time.Second {
        t.Fatalf("Too slow: %v > 120s threshold", elapsed)
    }

    // Cleanup
    t.Cleanup(func() { /* delete VLANs 900-999 */ })
}
```

---

## 5. Scale Test Cases

| ID | Test | Scale | Success Criteria |
|---|---|---|---|
| SCALE-01 | MaxVLANs | 1000 VLANs on one device | All created, all readable |
| SCALE-02 | MaxACLRules | 1000 ACL rules in one table | All created, correct priority |
| SCALE-03 | MaxLAGMembers | 8 members in one LAG | All joined, CONFIG_DB correct |
| SCALE-04 | MaxVRFs | 100 VRFs on one device | All created, all readable |
| SCALE-05 | MaxVNIMappings | 100 L2VNI mappings | All mapped, tunnel maps correct |
| SCALE-06 | MaxBGPNeighbors | 20 BGP neighbors | All configured in CONFIG_DB |
| SCALE-07 | MaxServices | 32 services on 32 interfaces | All applied, no conflicts |
| SCALE-08 | LargeConfigDB | 10,000+ keys in CONFIG_DB | Device connect < 10s |
| SCALE-09 | ConcurrentDevices | Connect to 4 devices simultaneously | All succeed within timeout |
| SCALE-10 | RapidCreateDelete | Create/delete 100 VLANs in loop | No stale state, no leaks |

### Scale Test Notes

- Scale tests require more generous timeouts (use 10-minute context)
- Monitor Redis memory usage during scale tests: `redis-cli INFO memory`
- Watch for ASIC_DB convergence delays at scale
- On VS, ASIC convergence may not happen for all objects at scale
- Resource cleanup must be reliable (use raw Redis DEL for bulk cleanup)

---

## 6. Chaos and Resilience Test Cases

| ID | Test | Fault Injection | Success Criteria |
|---|---|---|---|
| CHAOS-01 | RedisRestart | Restart Redis inside VM | Reconnect succeeds, state restored |
| CHAOS-02 | SSHTunnelDrop | Kill SSH tunnel process | Auto-reconnect or clear error |
| CHAOS-03 | PartialWrite | Kill connection mid-write | No partial CONFIG_DB state |
| CHAOS-04 | ConcurrentLock | Two processes lock same device | One succeeds, one fails cleanly |
| CHAOS-05 | StaleCleanup | Leave stale state, re-run tests | Tests handle pre-existing state |
| CHAOS-06 | DeviceDisconnect | Disconnect during operation | Clear error, no panic |
| CHAOS-07 | TimeoutDuringPoll | Set very short timeout for PollStateDB | Context cancelled error |
| CHAOS-08 | CorruptedStateDB | Write invalid data to STATE_DB | Device loads without crash |
| CHAOS-09 | MissingConfigDB | Empty CONFIG_DB (no tables) | Device connects, reports empty state |
| CHAOS-10 | NetworkPartition | Block management IP briefly | Operations fail with timeout |

### Chaos Test Implementation

```go
//go:build e2e && chaos

func TestChaos_RedisRestart(t *testing.T) {
    testutil.SkipIfNoLab(t)
    nodeName := leafNodeName(t)

    // Step 1: Connect and verify
    dev := testutil.LabConnectedDevice(t, nodeName)
    if !dev.VLANExists(100) {
        t.Skip("no baseline VLAN to verify")
    }

    // Step 2: Restart Redis inside VM
    ip := testutil.LabNodeIP(t, nodeName)
    cmd := fmt.Sprintf("sshpass -p cisco123 ssh cisco@%s 'sudo systemctl restart redis'", ip)
    exec.Command("bash", "-c", cmd).Run()

    // Step 3: Wait for Redis to come back
    time.Sleep(5 * time.Second)

    // Step 4: Reconnect and verify state persisted
    dev2 := testutil.LabConnectedDevice(t, nodeName)
    if !dev2.VLANExists(100) {
        t.Fatal("VLAN 100 missing after Redis restart")
    }
}
```

---

## 7. Regression Test Cases

These tests target specific bugs discovered during development:

| ID | Bug | Test | Success Criteria |
|---|---|---|---|
| REG-01 | tc binary path | Verify `lab_bridge_nics` uses `/usr/sbin/tc` | tc rules actually applied |
| REG-02 | MTU STATE_DB divergence | ConfigureInterface verifies CONFIG_DB not STATE_DB | MTU matches written value |
| REG-03 | stdin consumption | SSH commands don't consume stdin | Script completes normally |
| REG-04 | IRB ASIC convergence | IRBSymmetric test soft-fails on ASIC timeout | Test SKIPs, not FAILs |
| REG-05 | Stale device cache | Verify after execute uses fresh connection | Entry visible on fresh read |
| REG-06 | Cleanup ordering | Delete in reverse dependency order | No orphaned entries |
| REG-07 | ebtables packet drop | Check ebtables rules are flushed | No DROP rules on bridge |
| REG-08 | FRR config persistence | FRR config survives container restart | BGP sessions re-establish |
| REG-09 | SSH tunnel pool | Multiple tests share tunnels | No "too many connections" |
| REG-10 | Profile IP patching | Profiles have real IPs, not PLACEHOLDER | Device connects with patched IP |

---

## 8. Platform Capability Matrix

Tests should adapt behavior based on platform:

| Capability | SONiC-VS (NGDP) | SONiC HW | Test Behavior |
|---|---|---|---|
| CONFIG_DB writes | Yes | Yes | Hard fail on both |
| ASIC_DB convergence | Partial | Yes | Soft fail on VS |
| STATE_DB for MTU | No | Yes | Verify CONFIG_DB on VS |
| STATE_DB for BGP | Yes (FRR) | Yes | Hard fail on both |
| VXLAN data-plane | No | Yes | Skip on VS |
| L2 bridging | No (across VNI) | Yes | Skip on VS |
| L3 routing (data) | No | Yes | Skip on VS |
| ARP suppression | No | Yes | Skip on VS |
| tc mirred redirect | Yes (requires /usr/sbin/tc) | N/A | VS-only concern |

### Platform Detection

```go
func isVirtualSwitch(t *testing.T, name string) bool {
    entry := testutil.LabStateDBEntry(t, name, "DEVICE_METADATA", "localhost")
    return entry["platform"] == "x86_64-kvm_x86_64-r0"
}
```

---

## 9. Test ID and Resource Allocation

### VLAN IDs

| Range | Owner | Purpose |
|---|---|---|
| 100-199 | Seed data | Pre-existing baseline |
| 500-509 | operations_test.go | VLAN CRUD tests |
| 600-609 | multidevice_test.go | Cross-device VLAN |
| 700-709 | dataplane L2Bridged | L2 data-plane |
| 800-809 | dataplane IRBSymmetric | IRB data-plane |
| 900-999 | Performance/scale tests | Bulk create tests |

### PortChannel IDs

| Range | Owner |
|---|---|
| 100 | Seed data |
| 200-209 | operations_test.go |
| 300-399 | Scale tests |

### VRF Names

| Name | Owner |
|---|---|
| `Vrf_CUST1` | Seed data |
| `Vrf_e2e_test` | operations_test.go |
| `Vrf_e2e_svi` | operations_test.go (SVI) |
| `Vrf_e2e_irb` | dataplane IRBSymmetric |
| `Vrf_e2e_l3` | dataplane L3Routed |
| `Vrf_perf_*` | Performance tests |

### Subnets

| Subnet | Owner |
|---|---|
| `10.70.0.0/24` | L2Bridged |
| `10.80.0.0/24` | IRBSymmetric |
| `10.90.1.0/30`, `10.90.2.0/30` | L3Routed |
| `10.91.0.0/16` | Performance tests |

---

## 10. Success Criteria Summary

### E2E Suite (Current: 34 tests)

| Outcome | Criteria |
|---|---|
| **PASS** | All control-plane tests pass, all data-plane tests skip on VS |
| **Expected on VS** | 31 PASS + 3 SKIP + 0 FAIL |
| **Expected on HW** | 34 PASS + 0 SKIP + 0 FAIL |
| **FAIL** | Any control-plane test fails |

### Per-Test Success Criteria

| Test Type | PASS | SKIP | FAIL |
|---|---|---|---|
| Connectivity | Device connects, correct metadata | Lab not running | Connection error |
| Operations | CONFIG_DB matches expected state | Lab not running | Wrong state or operation error |
| Multi-device | All nodes consistent | BGP not converged (VS) | Inconsistent state |
| Data-plane | Ping succeeds | Ping fails on VS | CONFIG_DB wrong |

### CI Gate Criteria

- All unit tests pass
- All integration tests pass
- E2E: 0 FAIL tests (SKIPs allowed on VS)
- No new lint warnings
- Coverage does not decrease
