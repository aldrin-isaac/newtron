# RCA-045: sonic-vs frrcfgd Runtime Notification Failure and Template Gaps

## Summary

Community sonic-vs (202505 stable, Force10-S6000 HWSKU) has two frrcfgd issues
that prevent BGP configuration from working correctly:

1. **Runtime CONFIG_DB notifications are silently dropped.** frrcfgd's startup
   replay works (processes entries already in CONFIG_DB at boot), but entries
   written after frrcfgd starts are never programmed into FRR.

2. **The frrcfgd Jinja2 template (`bgpd.conf.db.j2`) does not handle
   `ebgp_requires_policy`.** This CONFIG_DB field is supported by the
   sonic-cfggen template (`bgpd.main.conf.j2`) used in split mode, but
   frrcfgd's template in unified mode omits it. FRR defaults to
   `bgp ebgp-requires-policy` enabled, blocking all eBGP route exchange.

## Root Cause

### Issue 1: Runtime Notifications

frrcfgd in unified mode uses `ExtConfigDBConnector.subscribe()` for runtime
CONFIG_DB keyspace notifications. On sonic-vs 202505, the subscription handler
(`sub_msg_handler`) receives `swsscommon.FieldValueMap` (C++ SWIG object) from
`pubsub.get_message()` instead of a Python dict. The type check
`msg_item['type'] == 'pmessage'` fails silently, dropping all notifications.

The startup replay path (`config_db.get_table()` → `bgp_message.put()` →
`__update_bgp()`) works correctly because it reads entries directly, bypassing
the pubsub mechanism.

CiscoVS does not exhibit this issue — its `ExtConfigDBConnector` uses a
compatible swsscommon version where `pubsub.get_message()` returns Python dicts.

### Issue 2: ebgp_requires_policy Template Gap

The sonic-cfggen template (`bgpd.main.conf.j2`) includes:
```
  no bgp ebgp-requires-policy
```
in the `bgp_init` block. This template is used in split mode (bgpcfgd).

frrcfgd's template (`/usr/local/sonic/frrcfgd/bgpd.conf.db.j2`) does NOT
include any handling for `ebgp_requires_policy`. It handles `default_ipv4_unicast`,
`log_nbr_state_changes`, `always_compare_med`, and many other BGP_GLOBALS fields,
but `ebgp_requires_policy` is missing.

### Issue 3: Factory CONFIG_DB Entries

sonic-vs ships with factory CONFIG_DB entries that conflict with newtron:
- `LOOPBACK_INTERFACE|Loopback0|10.1.0.1/32` — same on ALL switches, conflicts
  with inter-switch link IPs (FRR refuses to add a neighbor with a local IP)
- `INTERFACE|Ethernet*|10.0.0.*/31` — factory IPs on all 32 ports
- `BGP_NEIGHBOR|*` — 32 legacy bgpcfgd-format entries

## Workarounds

### Boot Patches (newtlab)

Three boot patches in `pkg/newtlab/patches/sonic-vs/always/`:

1. **`01-clean-factory-config.json`** — Removes factory BGP_NEIGHBOR, INTERFACE
   IP, and LOOPBACK_INTERFACE IP entries from CONFIG_DB via redis-cli DEL.

2. **`02-frrcfgd-ebgp-policy.json`** — Patches the frrcfgd Jinja2 template
   (`bgpd.conf.db.j2`) to handle `ebgp_requires_policy`. Adds the snippet:
   ```jinja2
   {% if 'ebgp_requires_policy' in bgp_sess and bgp_sess['ebgp_requires_policy'] == 'false' %}
    no bgp ebgp-requires-policy
   {% endif %}
   ```
   Uses `docker cp` + `docker exec python3` to apply the patch inside the BGP
   container.

### Test Suite Ordering (newtrun)

The `2node-vs-primitive` suite uses a different step ordering than `2node-ngdp-primitive`
(CiscoVS) to work around the runtime notification failure:

- **BGP_NEIGHBOR is written BEFORE `restart-bgp`**, not after. This ensures
  frrcfgd's startup replay processes the neighbor entries.
- **`evpn-setup` includes a `restart-bgp` step** after writing overlay
  BGP_NEIGHBOR entries, triggering another startup replay.

CiscoVS (`2node-ngdp-primitive`) writes BGP_NEIGHBOR AFTER restart because CiscoVS
runtime notifications work.

## Impact

- BGP underlay and overlay sessions
- EVPN VXLAN (depends on overlay BGP)
- Any service with BGP routing (transit, routed)
- Cross-switch L3 routing (depends on underlay BGP route exchange)

## Resolution Path

1. **Upstream fix for `ExtConfigDBConnector.sub_msg_handler`** — fix the pubsub
   message type handling in swsscommon/sonic-py-swsssdk.
2. **Upstream fix for `bgpd.conf.db.j2`** — add `ebgp_requires_policy` handling
   to the frrcfgd template.
3. Until fixed upstream, the boot patches and test ordering workarounds are
   sufficient for full test coverage.

## Validation

`2node-vs-primitive`: 21/21 PASS (sonic-vs 202505 stable, Mar 2026)
