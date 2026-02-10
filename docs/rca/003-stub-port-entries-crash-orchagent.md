# RCA-003: Stub PORT entries crash orchagent on SONiC-VPP

## Symptom

After provisioning a device with stub interfaces (topology ports with no link
and no service), `orchagent` crashes with SIGABRT and enters a restart loop.
All dataplane programming stops.

## Root Cause

The topology provisioner created PORT entries in CONFIG_DB for every interface
defined in topology.json, including stub interfaces that had no backing
physical or virtual port. In SONiC-VPP, orchagent attempts to program each
PORT entry into the SAI/VPP dataplane. When it encounters a PORT entry for
a non-existent VPP interface (no QEMU NIC was allocated because there's no
link), orchagent hits a fatal assertion and SIGABRTs.

In standard SONiC-VS, this doesn't crash because there's no real SAI — the
mock syncd ignores unknown ports. But VPP's SAI implementation requires
every PORT to map to a real interface.

## Impact

- Device completely unusable after provisioning
- Required reprovisioning with the fix and a VM restart

## Fix

Updated the topology provisioner to skip PORT entry creation for stub
interfaces — interfaces that have no topology link AND no service
configured:

```go
// In topology provisioner: skip interfaces with no link and no service
if iface.Link == "" && iface.Service == "" {
    continue // stub port — no QEMU NIC, no PORT entry
}
```

## Lesson

On SONiC-VPP, every CONFIG_DB PORT entry must correspond to a real
dataplane interface. Never create PORT entries for interfaces that don't
have a backing QEMU NIC. When designing provisioners that support multiple
SONiC platforms, test with the strictest platform (VPP) — behaviors that
are silently tolerated by VS may be fatal on VPP.
