# RCA-019: BGP ASN Mismatch After Provisioning

## Symptom

`ApplyFRRDefaults` fails with:

```
BGP instance name and AS number mismatch. BGP instance is already running; AS is 65100
```

The provisioner writes `bgp_asn: 65011` to CONFIG_DB, but FRR is still running `router bgp 65100`.

## Root Cause

The SONiC VPP image ships with a pre-baked FRR configuration containing `router bgp 65100`. When the provisioner writes a different ASN to `DEVICE_METADATA|localhost.bgp_asn` and `BGP_GLOBALS|default.local_asn`, `bgpcfgd` (which translates CONFIG_DB to FRR) cannot change the ASN of a **running** BGP instance. FRR requires the old instance to be deleted first (`no router bgp 65100`) before creating a new one.

Without a BGP container restart, `bgpcfgd` never gets a chance to regenerate `frr.conf` from scratch. It only processes incremental CONFIG_DB changes, and an ASN change on a running instance is not an incremental operation.

## Evidence

```
# CONFIG_DB has the correct ASN
redis-cli -n 4 hget 'DEVICE_METADATA|localhost' bgp_asn   → "65011"
redis-cli -n 4 hget 'BGP_GLOBALS|default' local_asn        → "65011"

# But FRR is running the factory-default ASN
vtysh -c 'show running-config' | grep 'router bgp'        → "router bgp 65100"
```

## Fix

Restart the BGP Docker container **after** provisioning CONFIG_DB and **before** calling `ApplyFRRDefaults`. The restart kills `bgpd` + `bgpcfgd`, and on startup `bgpcfgd` regenerates `frr.conf` from the current CONFIG_DB state (which now has the correct ASN).

```yaml
# Correct ordering in newtest scenarios:
- name: provision-leafs
  action: provision
  devices: [leaf1, leaf2]

- name: restart-bgp
  action: restart-service
  devices: [leaf1, leaf2]
  service: bgp

- name: wait-bgp-restart
  action: wait
  duration: 15s

- name: apply-frr-defaults
  action: apply-frr-defaults
  devices: [leaf1, leaf2]
```

The `config reload` alternative is **not safe** on SONiC VPP — it breaks VPP syncd (see RCA-001).

## Affected Topologies

Any topology where the device profile's `underlay_asn` differs from the SONiC VPP image's factory-default ASN (65100). This includes the 2node and 3node topologies which use ASN 65011/65012.

## Related

- RCA-001: config reload breaks VPP syncd
- RCA-008: frrcfgd template gaps
- RCA-017: systemctl restart fails on VPP (use `docker restart` instead)
