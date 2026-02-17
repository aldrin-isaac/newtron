# Platform Capability System

## Overview

The platform capability system allows features to be declared as unsupported on specific platforms. Both **configuration operations** and **test scenarios** respect these capabilities, ensuring clean failures or skips rather than silent configuration errors.

## Architecture

### 1. Platform Specification (`platforms.json`)

Each platform declares its `unsupported_features` as a string array:

```json
{
  "platforms": {
    "sonic-vpp": {
      "hwsku": "Force10-S6000",
      "unsupported_features": ["acl", "macvpn", "evpn-vxlan"]
    },
    "ciscovs": {
      "hwsku": "cisco-8101-p4-32x100-vs",
      "unsupported_features": []
    }
  }
}
```

### 2. Feature Support Check (`spec/types.go`)

The `PlatformSpec` type provides a `SupportsFeature()` method:

```go
func (p *PlatformSpec) SupportsFeature(feature string) bool {
    for _, f := range p.UnsupportedFeatures {
        if f == feature {
            return false
        }
    }
    return true
}
```

### 3. Configuration Operations

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

### 4. Test Scenario Filtering

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

| Feature | Description | Unsupported On |
|---------|-------------|----------------|
| `acl` | ACL table configuration and rule application | sonic-vpp |
| `macvpn` | MAC-VPN (EVPN L2VNI) overlay services | sonic-vpp |
| `evpn-vxlan` | EVPN VXLAN tunnel encapsulation/decapsulation | sonic-vpp |

## Usage Guidelines

### Adding a New Unsupported Feature

1. **Update platform spec** (`topologies/*/specs/platforms.json`):
   ```json
   "unsupported_features": ["acl", "macvpn", "new-feature"]
   ```

2. **Add platform check to operations** (if applicable):
   ```go
   if platform, err := n.GetPlatform(resolved.Platform); err == nil {
       if !platform.SupportsFeature("new-feature") {
           return nil, fmt.Errorf("platform %s does not support new-feature", resolved.Platform)
       }
   }
   ```

3. **Declare in test scenarios** (if applicable):
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
