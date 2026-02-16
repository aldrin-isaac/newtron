# CiscoVS Factory FRR Config Invalid - BGP Crash Loop

**Date:** 2026-02-16
**Platform:** SONiC CiscoVS (cisco-p200-32x100-vs, Palladium2)
**Image:** sonic-vs.img.gz from ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1
**Affected Component:** BGP container, FRR configuration

---

## Problem

BGP container enters crash-loop on first boot. The container starts, bgpd rejects the configuration, supervisord exits vtysh_b with status 13, and systemd restarts the service every ~15-20 seconds indefinitely.

## Symptom

```bash
$ docker ps -a | grep bgp
a98c0cf59de0   docker-fpm-frr:latest   ...   Exited (137) 31 seconds ago   bgp

$ systemctl status bgp
Active: activating (auto-restart) since Mon 2026-02-16 23:20:39 UTC; 20s ago

$ docker logs bgp | grep -i error
vtysh_b % Cannot have local-as same as BGP AS number
vtysh_b line 56: Failure to communicate[13] to bgpd, line:  neighbor 10.1.0.1 local-as 65011
vtysh_b [86|bgpd] Configuration file[/etc/frr/frr.conf] processing failure: 13
```

Impact: All `vtysh` commands fail because the BGP container is down. Tests and automation that rely on BGP fail immediately.

## Root Cause

The factory `/etc/frr/frr.conf` in the CiscoVS image contains an invalid BGP configuration:

```frr
router bgp 65011
 bgp router-id 10.0.0.11
 ...
 neighbor 10.1.0.1 remote-as 65012
 neighbor 10.1.0.1 local-as 65011   ← INVALID
```

**Error:** The `local-as 65011` command uses the same ASN (65011) as the router's own ASN. This is prohibited by FRR/BGP RFC compliance.

**Why it's invalid:**
The `local-as` command is used for AS path manipulation, allowing a router in AS X to present itself as AS Y to a specific neighbor. Setting `local-as` to the same value as the router's actual ASN is redundant and semantically invalid - the router already announces that ASN by default.

FRR bgpd rejects this configuration and returns error code 13, causing vtysh_b to exit with status 13, triggering supervisord to mark the startup as failed.

## Why This Exists

This appears to be a bug in the SONiC CiscoVS image's factory configuration or config generation templates. The config is managed by `sonic-cfggen` using `bgpd.conf.db.j2`, suggesting the issue is either:

1. **Template bug:** The Jinja2 template unconditionally adds `local-as` even when it matches the router ASN
2. **Factory CONFIG_DB:** The initial CONFIG_DB in the image has an incorrect BGP neighbor config entry
3. **HWSKU-specific defaults:** The cisco-p200-32x100-vs HWSKU ships with a sample config that has this error

**Evidence:** The config header says "Managed by sonic-cfggen DO NOT edit manually" and references `templates/frr.conf.j2` and `bgpd.conf.db.j2`, indicating this was generated from CONFIG_DB data during image build or first boot.

## Workaround

### Option 1: Manual config fix (one-time)

SSH to the device and fix the FRR config:

```bash
$ docker exec -it bgp vtysh
# configure terminal
# router bgp 65011
# no neighbor 10.1.0.1 local-as 65011
# end
# write memory
# exit

$ sudo systemctl restart bgp
```

### Option 2: Fix CONFIG_DB (persistent)

Remove the invalid local-as from CONFIG_DB before BGP starts:

```bash
$ sonic-db-cli CONFIG_DB HDEL "BGP_NEIGHBOR|10.1.0.1" local-asn
$ sudo systemctl restart bgp
```

### Option 3: newtlab boot patch (automated)

Add to newtlab's CiscoVS platform bootstrap sequence:

```go
// In pkg/newtlab/deploy.go, ciscovs platform boot patch:
if platform == "ciscovs" {
    // Remove invalid local-as from factory config
    cmd := "sonic-db-cli CONFIG_DB HDEL 'BGP_NEIGHBOR|10.1.0.1' local_asn 2>/dev/null || true"
    session.Run(cmd)
    session.Run("sudo systemctl restart bgp")
}
```

This removes the bad config before provisioning starts.

## Upstream Fix

File a bug against:
- **Repository:** `CiscoDevNet/sonic-buildimage` (or wherever CiscoVS SAI images are maintained)
- **Component:** Factory configuration / HWSKU defaults
- **Fix:** Remove the `neighbor 10.1.0.1 local-as 65011` line from the factory `/etc/sonic/config_db.json` template for cisco-p200-32x100-vs

Alternatively, fix the `bgpd.conf.db.j2` template to skip rendering `local-as` when it equals the router's own ASN:

```jinja2
{% if neighbor.local_asn is defined and neighbor.local_asn != bgp_asn %}
 neighbor {{ neighbor_addr }} local-as {{ neighbor.local_asn }}
{% endif %}
```

## Detection

Check for this issue in new SONiC virtual platforms:

```bash
$ docker exec bgp grep "local-as" /etc/frr/frr.conf | grep -v "^!"
neighbor 10.1.0.1 local-as 65011

$ docker exec bgp sh -c 'grep "^router bgp" /etc/frr/frr.conf'
router bgp 65011

# If local-as value matches router bgp ASN → invalid config
```

## Related Issues

- May affect other Cisco virtual HWSKU variants (Gibraltar, GR2) if they share the same factory config template
- Does not affect SONiC VPP platform (different factory config)
- Likely affects any CiscoVS image built from the 202505 branch at commit cb27941b

## Lesson Learned

Always validate factory configs in virtual platform images before using them as deployment baselines. The presence of `sonic-cfggen` headers doesn't guarantee the generated config is valid - template bugs or bad CONFIG_DB seed data can produce syntactically correct but semantically invalid FRR configs.

For automation/testing frameworks (newtlab, newtest), add health checks that verify BGP container stability before proceeding with provisioning steps that depend on `vtysh` being available.
