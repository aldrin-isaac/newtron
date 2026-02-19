# RCA-031: Topology Provisioner Skips INTERFACE IP Entries for Non-Service Interfaces

**Status**: Fixed
**Component**: `pkg/newtron/network/topology.go`
**Affected**: Any topology where a non-service interface has an `ip` field
**Discovered**: 2026-02-19

---

## Symptom

After `newtlab provision`, host-facing switch interfaces have the base CONFIG_DB entry but
no IP address entry:

```
# Expected after provision
CONFIG_DB> KEYS INTERFACE|Ethernet1*
INTERFACE|Ethernet1
INTERFACE|Ethernet1|10.1.1.1/31      ← MISSING

# Actual
CONFIG_DB> KEYS INTERFACE|Ethernet1*
INTERFACE|Ethernet1                   ← base entry only
```

Host cannot reach the switch gateway IP:

```
host1# ping -c 3 10.1.1.1
connect: Network is unreachable
```

---

## Root Cause

`addDeviceEntries` in `topology.go` iterated topology interfaces and wrote INTERFACE entries:

```go
for intfName, ti := range topoDev.Interfaces {
    if ti.Service == "" {
        continue   // ← skipped non-service interfaces entirely
    }
    // ... write INTERFACE base entry ...
}
```

Interfaces with a `service` field are handled by `GenerateServiceEntries`, which writes both
the base entry and IP entry. But interfaces WITHOUT a service (host-facing, VRF-bound) were
skipped by the early `continue`. Their `ip` field in `topology.json` was silently ignored.

### VRF Field Also Lost

`TopologyInterface` in `pkg/newtron/spec/types.go` was missing the `VRF` field:

```go
// Before
type TopologyInterface struct {
    Link    string            `json:"link,omitempty"`
    Service string            `json:"service"`
    IP      string            `json:"ip,omitempty"`
    // ← no VRF field
    Params  map[string]string `json:"params,omitempty"`
}
```

So `"vrf": "CUSTOMER"` in `topology.json` was silently dropped during JSON unmarshal —
the field had nowhere to go.

---

## Impact

Non-service, non-loopback interfaces with explicit IPs in topology.json did not get
CONFIG_DB INTERFACE entries written. This affected:

- `switch1:Ethernet1` (host1 gateway, `10.1.1.1/31`)
- `switch1:Ethernet2` (host2 gateway, from the topology)
- `switch1:Ethernet3` (host3 gateway, from the topology)
- `switch2:Ethernet2` (CUSTOMER VRF gateway, `10.10.1.1/31`)

Loopback interfaces are provisioned via a separate path (not affected).
Transit interfaces have `service: transit-bgp` and went through `GenerateServiceEntries` (not affected).

---

## Fix

### 1. Add VRF to TopologyInterface (`spec/types.go`)

```go
type TopologyInterface struct {
    Link    string            `json:"link,omitempty"`
    Service string            `json:"service"`
    IP      string            `json:"ip,omitempty"`
    VRF     string            `json:"vrf,omitempty"`     // ← added
    Params  map[string]string `json:"params,omitempty"`
}
```

### 2. Provision INTERFACE + IP for non-service interfaces (`topology.go`)

```go
// Write INTERFACE base + IP entries for non-service interfaces with explicit IPs.
for intfName, ti := range topoDev.Interfaces {
    if ti.Service != "" || ti.IP == "" {
        continue
    }
    if !strings.HasPrefix(intfName, "Ethernet") {
        continue
    }
    intfBase := map[string]string{}
    if ti.VRF != "" {
        intfBase["vrf_name"] = ti.VRF
    }
    cb.AddEntry("INTERFACE", intfName, intfBase)
    cb.AddEntry("INTERFACE", fmt.Sprintf("%s|%s", intfName, ti.IP), map[string]string{})
    if ti.VRF != "" {
        cb.AddEntry("VRF", ti.VRF, map[string]string{})
    }
}
```

The `Loopback` prefix is excluded (loopbacks use `LOOPBACK_INTERFACE`, handled separately).

---

## Why This Wasn't Caught Earlier

The 2node topology was added after the basic 3node validation. In 3node:

- All transit links have `service: transit-bgp` → go through `GenerateServiceEntries`
- Hosts connect to switches but the host-facing interfaces had no IP configured in topology.json
  (hosts were separate VMs; host IPs were configured by the host-exec provision steps)

In 2node, host-facing switch ports needed IPs in topology.json so the namespace routing would work.
This was a new pattern not exercised by 3node.

---

## Verification

After fix and reprovision:

```
CONFIG_DB> HGETALL 'INTERFACE|Ethernet1|10.1.1.1/31'
(empty hash — entry exists, fields are empty for IP sub-entries)

CONFIG_DB> HGETALL 'INTERFACE|Ethernet1'
vrf_name: ""    ← base entry present

host1# ping -c 3 10.1.1.1
3 packets transmitted, 3 received, 0% packet loss
```
