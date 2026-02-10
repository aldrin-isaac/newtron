# RCA-007: QEMU -nographic adds implicit serial, breaking console access

## Symptom

Serial console access via the explicit `-serial tcp::30000,server,nowait`
argument didn't work. Connecting to port 30000 showed no output or showed
the wrong console. Inside the VM, the SONiC login prompt appeared on `ttyS1`
instead of the expected `ttyS0`.

## Root Cause

The `-nographic` QEMU flag does two things:

1. Disables graphical display (intended)
2. **Implicitly adds** `-serial mon:stdio` (unintended)

This implicit serial takes `ttyS0`, pushing our explicit `-serial tcp::30000`
to `ttyS1`. Since SONiC's getty and login prompt are configured for `ttyS0`,
they appear on QEMU's implicit stdio serial — which is connected to nothing
useful in a daemonized process — instead of our TCP serial port.

## Fix

Replaced `-nographic` with `-display none`, which disables the graphical
display **without** adding an implicit serial device:

```go
// Before (broken):
args = append(args, "-nographic")

// After (correct):
args = append(args, "-display", "none")
```

With `-display none`, our explicit `-serial tcp::30000,server,nowait` gets
`ttyS0` as expected, and SONiC's login prompt appears on the TCP console.

## Lesson

`-nographic` is a convenience shortcut that bundles multiple behaviors.
For programmatic QEMU control, use the individual flags (`-display none`,
explicit `-serial`, explicit `-monitor`) to avoid implicit side effects.
Always check what implicit devices QEMU adds by looking at the QEMU monitor
`info chardev` output.
