# RCA-011: newtlab provision fails — newtron binary not found in PATH

## Symptom

`newtlab provision` failed with "exec: newtron: executable file not found in
$PATH" even though the newtron binary was built and present in `bin/`.

## Root Cause

`Lab.Provision()` called `exec.Command("newtron", ...)` using the bare binary
name, relying on `$PATH` resolution. When running from a development checkout
(`bin/newtlab`), the `bin/` directory is not in `$PATH`, so the OS cannot find
the `newtron` binary.

The same issue affected `newtlink` (bridge agent binary) in `startBridgeProcess`.

## Impact

- Provisioning from newtlab completely blocked
- Bridge process startup on remote hosts also affected (newtlink not found)

## Fix

Added `findSiblingBinary()` helper in `pkg/newtlab/newtlab.go` that looks for
binaries adjacent to the current executable before falling back to `$PATH`:

```go
func findSiblingBinary(name string) string {
    if exe, err := os.Executable(); err == nil {
        p := filepath.Join(filepath.Dir(exe), name)
        if _, err := os.Stat(p); err == nil {
            return p
        }
    }
    return name
}
```

Used in `Lab.Provision()` for newtron and in `startBridgeProcess` for newtlink.

## Lesson

When one binary spawns another, never assume `$PATH` contains the right
directory. Always check the directory of the current executable first. This
pattern is especially important for multi-binary tools that are built together
and co-located in the same `bin/` directory.
