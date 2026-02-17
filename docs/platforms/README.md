# Platform Guides

Platform-specific documentation capturing idiosyncrasies, limitations, and operational learnings.

## Available Platforms

| Platform | Description | EVPN Support | Status |
|----------|-------------|--------------|--------|
| [sonic-vpp](sonic-vpp.md) | SONiC with VPP dataplane (202505) | ❌ No | Tested |
| [sonic-ciscovs](sonic-ciscovs.md) | SONiC with Cisco Silicon One VS (Palladium2) | ✅ Yes | In Progress |

## When to Use Which Platform

### sonic-vpp
- **Use for:** L3 routing, BGP testing, basic SONiC validation
- **Avoid for:** EVPN/VXLAN, ACLs, production-like scenarios
- **Best for:** Fast iteration, basic connectivity tests

### sonic-ciscovs
- **Use for:** EVPN/VXLAN, full SONiC feature testing, realistic scenarios
- **Avoid for:** Quick tests (slower boot), resource-constrained environments
- **Best for:** Production-like testing, EVPN fabrics, full feature validation

## Adding a New Platform

When adding a new platform, create a guide documenting:

1. **Overview**: What it is, hardware model, use cases
2. **Setup**: Build process, image requirements, dependencies
3. **Limitations**: Known unsupported features, bugs
4. **Quirks**: Unexpected behaviors, workarounds
5. **Configuration**: Required settings, boot patches
6. **Testing**: What works, what doesn't, test considerations
7. **References**: Related RCAs, upstream issues

See existing guides for template structure.
