# RCA-040: CiscoVS PFE Mode Selection via HWSKU

**Date:** 2026-02-21
**Platform:** SONiC CiscoVS (all HWSKUs)
**Status:** DOCUMENTED

## Summary

The CiscoVS image ships with three Silicon One ASIC models (PFE modes).
The active mode is selected by the `hwsku` field in `DEVICE_METADATA|localhost`.
SONiC bind-mounts the corresponding HWSKU directory into each container as
`/usr/share/sonic/hwsku/`, and the syncd container's `ngdp_inst.py` reads
`nsim_config.json` from that directory to start the correct NGDP simulator.

## Available PFE Modes

| HWSKU | ASIC Revision | NGDP Library | Hardware Reference | Board Type |
|-------|---------------|--------------|-------------------|------------|
| `cisco-p200-32x100-vs` | PALLADIUM2_A0 | libngdp-pl2 | Cisco 8223-x | palladium2 |
| `cisco-8101-p4-32x100-vs` | GIBRALTAR_A1 | libngdp-gib | Cisco 8101-32FH-O | churchill_p4 |
| `cisco-gr2-32x100-vs` | GR2_A0 | libngdp-gr2 | Cisco 8122-64EH-O | gr2 |

All three are 32x100G port configurations. They differ in ASIC simulation
fidelity, SAI feature support, and lane mapping.

**Factory default:** `cisco-p200-32x100-vs` (Palladium2), set by
`/usr/share/sonic/device/x86_64-kvm_x86_64-r0/default_sku`.

## How PFE Mode Selection Works

### Boot Sequence

1. SONiC reads `hwsku` from `/etc/sonic/config_db.json` → `DEVICE_METADATA.localhost.hwsku`
2. If no config_db.json, falls back to `default_sku` file (Palladium2)
3. Systemd starts containers with bind mount:
   `/usr/share/sonic/device/x86_64-kvm_x86_64-r0/<hwsku>/` → `/usr/share/sonic/hwsku/`
4. Inside syncd, supervisor starts `ngdp_inst.py` which:
   - Reads `/usr/share/sonic/hwsku/nsim_config.json`
   - Extracts `revision` field (e.g., `PALLADIUM2_A0`)
   - Selects matching NGDP library (`libngdp-pl2`, `libngdp-gib`, or `libngdp-gr2`)
   - Starts `ngdpd` with the correct library and interface mappings
5. syncd then starts with `syncd_ciscovs_start.sh`, connecting to the running NGDP

### Key Files per HWSKU

Each HWSKU directory (`/usr/share/sonic/device/x86_64-kvm_x86_64-r0/<hwsku>/`)
contains:

| File | Purpose |
|------|---------|
| `nsim_config.json` | NGDP revision + interface-to-slice/ifg/serdes mapping |
| `sai_init_config.json` | SAI initialization: board-type, device type, port mix |
| `port_config.ini` | Lane mapping (differs between ASICs) |
| `ciscovs_sai.profile` | SAI config file paths |
| `sai.profile` | VS SAI profile (switch type, hostif, lane map paths) |
| `fabriclanemap.ini` | Fabric lane mapping |

### Differences Between Modes

**Palladium2** (`cisco-p200-32x100-vs`):
- `nsim_config.json`: `revision: "PALLADIUM2_A0"`, `datapath: "ngdp"`
- `sai_init_config.json`: `board-type: "palladium2"`, device type `"palladium2"`
- Sequential lane numbering (0-based, stride 8 per IFG, stride 256 per slice)
- Known limitation: `VNI_TO_VIRTUAL_ROUTER_ID` SAI failure (RCA-039)

**Gibraltar** (`cisco-8101-p4-32x100-vs`):
- `nsim_config.json`: `revision: "GIBRALTAR_A1"`, no `datapath` field
- `sai_init_config.json`: `board-type: "churchill_p4"`, device type `"gibraltar"`
- Non-sequential lane numbering (matches 8101-32FH-O physical layout)
- Additional files: `buffers.json.j2`, `qos.json.j2`, `ciscovs_idprom.yaml`

**GR2** (`cisco-gr2-32x100-vs`):
- `nsim_config.json`: `revision: "GR2_A0"`, no `datapath` field
- `sai_init_config.json`: `board-type: "gr2"`, device type `"gr2"`
- Non-sequential lane numbering (matches 8122-64EH-O physical layout)

## How to Select a PFE Mode

### Method 1: newtlab Boot Patch (Recommended)

Create or update the `hwsku` boot patch in
`pkg/newtlab/patches/ciscovs/always/`. The patch writes the desired HWSKU
to `config_db.json` before SONiC containers start reading it.

**Example boot patch** (`03-select-hwsku.json`):
```json
{
    "description": "Select CiscoVS PFE mode via HWSKU (platforms.json)",
    "pre_commands": [
        "for i in $(seq 1 30); do [ -f /etc/sonic/config_db.json ] && break; sleep 2; done"
    ],
    "post_commands": [
        "python3 -c \"import json; f='/etc/sonic/config_db.json'; d=json.load(open(f)); d.setdefault('DEVICE_METADATA',{}).setdefault('localhost',{})['hwsku']='{{.HWSKU}}'; json.dump(d,open(f,'w'),indent=4)\"",
        "redis-cli -n 4 HSET 'DEVICE_METADATA|localhost' hwsku '{{.HWSKU}}'"
    ]
}
```

The `{{.HWSKU}}` template variable is derived from `platforms.json`
(resolved via `buildPatchVars` → `HWSkuDir`). The HWSKU name is the
last path component of `HWSkuDir`.

Then set the desired HWSKU in `platforms.json`:
```json
{
    "sonic-ciscovs": {
        "hwsku": "cisco-8101-p4-32x100-vs",
        ...
    }
}
```

**Important:** Changing the HWSKU after containers have started requires
restarting syncd (`systemctl restart syncd`) to remount the HWSKU directory.
If the boot patch runs early enough (before syncd starts), no restart is
needed.

### Method 2: Modify default_sku in the Image

Edit `/usr/share/sonic/device/x86_64-kvm_x86_64-r0/default_sku` before
first boot. This file is read by `sonic-cfggen` when no `config_db.json`
exists (first boot only).

```
cisco-8101-p4-32x100-vs l1
```

This requires modifying the qcow2 image and is not practical for
per-topology selection.

### Method 3: Manual (Testing/Debugging)

SSH into the device and:

```bash
# 1. Update config_db.json on disk
sudo python3 -c "
import json
f = '/etc/sonic/config_db.json'
d = json.load(open(f))
d['DEVICE_METADATA']['localhost']['hwsku'] = 'cisco-8101-p4-32x100-vs'
json.dump(d, open(f, 'w'), indent=4)
"

# 2. Update running CONFIG_DB
redis-cli -n 4 HSET 'DEVICE_METADATA|localhost' hwsku 'cisco-8101-p4-32x100-vs'

# 3. Reboot to remount HWSKU directory into all containers
sudo reboot
```

A `config reload` may suffice instead of a full reboot, but a reboot is
the safest path since the HWSKU directory bind mount happens at container
start time.

## Relationship to newtlab

### platforms.json

The `hwsku` field in `platforms.json` serves dual purpose:

1. **newtlab**: Used in `buildPatchVars()` to compute `HWSkuDir`
   (`/usr/share/sonic/device/x86_64-kvm_x86_64-r0/<hwsku>`), available
   to boot patch templates
2. **newtron topology provisioner**: Written to expected DEVICE_METADATA
   entries for health check verification

### Health Check Implications

The topology provisioner's `GenerateDeviceComposite` includes `hwsku` in
the expected DEVICE_METADATA. The health check (`VerifyChangeSet`) verifies
that the actual CONFIG_DB `hwsku` matches. If the boot patch doesn't set
the HWSKU, the factory default (Palladium2) will be in CONFIG_DB, and a
mismatch will occur if platforms.json specifies a different HWSKU.

**Rule:** `platforms.json` HWSKU must match what's actually running on
the device — either the factory default or the value set by a boot patch.

## Current State

- **Default PFE:** Palladium2 (`cisco-p200-32x100-vs`)
- **platforms.json:** All topologies (2node, 2node-service, 3node) set to
  `cisco-p200-32x100-vs` (matches factory default)
- **Boot patch for HWSKU switching:** Not yet implemented
- **All test suites pass on Palladium2**

## Feature Compatibility Notes

From the CiscoVS release notes, Gibraltar and GR2 have documented feature
sets at the Cisco SONiC release notes page. Palladium2 (8223-x) is the
newest ASIC model. All three share the same SAI version (1.16.1) and SDK
(25.9.1000.2) in this release.

Known Palladium2 limitation: `VNI_TO_VIRTUAL_ROUTER_ID` SAI call fails
(RCA-039), blocking EVPN L3VPN type-5. EVPN IRB type-2 works. This
limitation has not been tested on Gibraltar or GR2.

## References

- CiscoVS SAI tarball: `~/Downloads/ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1.tar.gz`
- NGDP instantiation: `/etc/scripts/ngdp_inst.py` (inside syncd container)
- HWSKU directories: `/usr/share/sonic/device/x86_64-kvm_x86_64-r0/cisco-{p200,8101-p4,gr2}-32x100-vs/`
- Default SKU: `/usr/share/sonic/device/x86_64-kvm_x86_64-r0/default_sku`
- Container HWSKU mount: bind-mount `<hwsku-dir>` → `/usr/share/sonic/hwsku/`
- RCA-039: EVPN L3VPN SAI limitation on Palladium2
- Cisco SONiC release notes: https://www.cisco.com/c/en/us/td/docs/iosxr/cisco8000/sonic/b-release-notes-sonic-202405-1-x.html
