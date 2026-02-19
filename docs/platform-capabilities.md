# Platform Capability System

## Overview

The platform capability system allows features to be declared as unsupported on specific platforms. Both **configuration operations** and **test scenarios** respect these capabilities, ensuring clean failures or skips rather than silent configuration errors.

## Architecture

### 1. Platform Specification (`platforms.json`)

Each platform declares its `unsupported_features` as a string array. **Only list base features** - dependent features are automatically unsupported via the dependency graph.

```json
{
  "platforms": {
    "sonic-vpp": {
      "hwsku": "Force10-S6000",
      "unsupported_features": ["acl", "evpn-vxlan"]
      // Note: macvpn/ipvpn auto-unsupported (depend on evpn-vxlan)
    },
    "ciscovs": {
      "hwsku": "cisco-8101-p4-32x100-vs",
      "unsupported_features": []
    }
  }
}
```

### 2. Feature Dependency Graph (`spec/types.go`)

Features can depend on other features. The dependency graph is defined in code:

```go
var featureDependencies = map[string][]string{
    // MAC-VPN requires VXLAN dataplane support
    "macvpn": {"evpn-vxlan"},

    // IP-VPN (L3 EVPN) requires VXLAN dataplane support
    "ipvpn": {"evpn-vxlan"},

    // Base features with no dependencies
    "evpn-vxlan": {},
    "acl":        {},
}
```

**Dependency Resolution:**
- If `evpn-vxlan` is unsupported, `macvpn` and `ipvpn` are automatically unsupported
- Platforms only need to list base features (e.g., `evpn-vxlan`)
- Dependent features inherit the unsupported status via the dependency chain

### 3. Feature Support Check (`spec/types.go`)

The `PlatformSpec` type provides a `SupportsFeature()` method that checks both direct unsupported features and dependencies:

```go
func (p *PlatformSpec) SupportsFeature(feature string) bool {
    // Check if feature is directly unsupported
    if p.isUnsupported(feature) {
        return false
    }

    // Check if any dependencies are unsupported (recursive)
    for _, dep := range featureDependencies[feature] {
        if !p.SupportsFeature(dep) {
            return false
        }
    }

    return true
}
```

**Helper Functions:**
- `GetUnsupportedDueTo(baseFeature)` - Returns features disabled by a base feature
- `GetFeatureDependencies(feature)` - Returns dependencies for a feature

### 4. Configuration Operations

Operations that depend on specific features check platform support and return errors:

**Example** (`pkg/newtron/network/node/macvpn_ops.go`):
```go
func (i *Interface) BindMACVPN(ctx context.Context, ...) (*ChangeSet, error) {
    // ... preconditions ...

    // Check platform support for MACVPN (EVPN VXLAN)
    resolved := n.Resolved()
    if resolved.Platform != "" {
        if platform, err := n.GetPlatform(resolved.Platform); err == nil {
            if !platform.SupportsFeature("macvpn") {
                return nil, fmt.Errorf("platform %s does not support MAC-VPN (EVPN VXLAN)", resolved.Platform)
            }
        }
    }

    // ... proceed with configuration ...
}
```

**Operations with platform checks**:
- `BindMACVPN()` - checks `"macvpn"`
- `UnbindMACVPN()` - checks `"macvpn"`
- `MapL2VNI()` - checks `"evpn-vxlan"`
- Service generation with ACLs - checks `"acl"` (existing)

### 5. Test Scenario Filtering

Test scenarios declare `requires_features` in YAML:

```yaml
name: evpn-l2-irb
description: EVPN L2VNI with IRB testing
topology: 3node
platform: ciscovs
requires: [boot-provision]
requires_features: [evpn-vxlan, macvpn]
```

**Automatic skipping** (`pkg/newtest/runner.go`):
- Before running a scenario, `checkPlatformFeatures()` validates all required features
- If any feature is unsupported, the scenario is skipped with a clear reason
- Skip reason example: `"platform 'sonic-vpp' does not support required features: [macvpn, evpn-vxlan]"`

## Currently Defined Features

| Feature | Description | Dependencies | When to Mark Unsupported |
|---------|-------------|--------------|--------------------------|
| `acl` | ACL table configuration and rule application | None | ACL ASIC rules not programmed |
| `evpn-vxlan` | VXLAN tunnel encapsulation/decapsulation (dataplane) | None | VXLAN tunnels don't work in hardware/SAI |
| `macvpn` | MAC-VPN (EVPN L2VNI) overlay - Type-2/3 routes | `evpn-vxlan` | L2 EVPN control plane broken (even if VXLAN works) |
| `ipvpn` | IP-VPN (EVPN L3VNI) overlay - Type-5 routes | `evpn-vxlan` | L3 EVPN control plane broken (even if VXLAN works) |

### Feature Independence

**Important:** `macvpn` and `ipvpn` are **independent** of each other:
- Marking `evpn-vxlan` unsupported → disables BOTH (cascade via dependency)
- Marking `macvpn` unsupported → only disables L2 EVPN, `ipvpn` still works
- Marking `ipvpn` unsupported → only disables L3 EVPN, `macvpn` still works

### Real-World Examples

**VPP Platform** (no VXLAN dataplane):
```json
"unsupported_features": ["evpn-vxlan"]
// Result: macvpn ✗, ipvpn ✗ (both cascaded)
```

**Platform with broken L2 EVPN** (VXLAN works, only L2 control plane broken):
```json
"unsupported_features": ["macvpn"]
// Result: macvpn ✗, ipvpn ✓, evpn-vxlan ✓
```

**Platform with broken L3 EVPN** (VXLAN works, only L3 control plane broken):
```json
"unsupported_features": ["ipvpn"]
// Result: ipvpn ✗, macvpn ✓, evpn-vxlan ✓
```

## Usage Guidelines

### Adding a New Feature

1. **Define feature dependencies** (`pkg/newtron/spec/types.go`):
   ```go
   var featureDependencies = map[string][]string{
       "new-feature": {"base-feature"},  // If it has dependencies
       "new-feature": {},                 // If it's a base feature
   }
   ```

2. **Update platform spec** (`topologies/*/specs/platforms.json`):
   ```json
   "unsupported_features": ["acl", "evpn-vxlan", "new-feature"]
   ```
   **Note:** Only list the feature if it's directly unsupported. If it's unsupported due to a dependency, don't list it.

3. **Add platform check to operations** (if applicable):
   ```go
   if platform, err := n.GetPlatform(resolved.Platform); err == nil {
       if !platform.SupportsFeature("new-feature") {
           return nil, fmt.Errorf("platform %s does not support new-feature", resolved.Platform)
       }
   }
   ```

4. **Declare in test scenarios** (if applicable):
   ```yaml
   requires_features: [new-feature]
   ```

### When to Use Platform Checks

**Configuration operations**:
- ✅ Check when operation writes CONFIG_DB entries that won't be honored
- ✅ Check when operation depends on dataplane offload (VXLAN, ACL ASIC rules)
- ❌ Don't check for informational/read operations (GetRoute, VerifyHealth)

**Test scenarios**:
- ✅ Declare when testing platform-specific features
- ✅ Declare when testing dataplane forwarding behavior
- ❌ Don't declare for basic provisioning/control-plane tests

## Benefits

1. **Fail Fast**: Operations fail immediately with clear errors rather than silently writing dead CONFIG_DB entries
2. **Auto-Skip Tests**: Test scenarios automatically skip on incompatible platforms
3. **Single Source of Truth**: Platform file declares capabilities once, used everywhere
4. **Clear Feedback**: Users get explicit messages about what's not supported and why

## Example Workflow

```bash
# Run test suite on VPP - EVPN test auto-skips
$ bin/newtest suite 3node-dataplane --platform sonic-vpp
✓ boot-provision (12.3s)
✓ l3-routing (8.1s)
⊘ evpn-l2-irb (skipped: platform 'sonic-vpp' does not support required features: [evpn-vxlan, macvpn])

# Run same suite on CiscoVS - EVPN test runs
$ bin/newtest suite 3node-dataplane --platform ciscovs
✓ boot-provision (18.4s)
✓ l3-routing (10.2s)
✓ evpn-l2-irb (22.7s)
```

## SONiC VPP Platform Limitations

The VPP platform has several known limitations documented in RCAs:

- **No EVPN VXLAN** (RCA-022): VPP SAI tunnel offload not merged ([sonic-platform-vpp#99](https://github.com/sonic-net/sonic-platform-vpp/issues/99))
- **No ACL support** (RCA-009): ICMP matching not resolved, rules not programmed
- **No swss restart** (RCA-001): Restarting swss/syncd breaks VM permanently

These limitations are encoded in the `unsupported_features` list to prevent incorrect configuration and wasted test cycles.
