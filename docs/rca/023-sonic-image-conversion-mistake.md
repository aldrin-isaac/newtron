# SONiC Image Format Misconception

**Date:** 2026-02-16
**Image:** sonic-vs.img.gz (910MB compressed, 2.36GB decompressed)
**Affected Component:** newtlab image preparation

---

## Problem

Incorrectly converted `sonic-vs.img.gz` to qcow2 format by passing the compressed .gz file directly to `qemu-img convert`, resulting in a corrupted 910 MiB image with invalid virtual size.

## Symptom

```bash
$ qemu-img convert -f raw -O qcow2 sonic-vs.img.gz sonic-ciscovs.qcow2
$ qemu-img info sonic-ciscovs.qcow2
virtual size: 910 MiB (953917952 bytes)  # WRONG - same as compressed size
```

Expected output after correct conversion:
```bash
virtual size: 16 GiB (17179869184 bytes)
disk size: 2.36 GiB
```

## Root Cause

**Misconception:** Assumed `sonic-vs.img.gz` was a gzipped raw disk image that needed decompression + qcow2 conversion.

**Reality:** The decompressed `sonic-vs.img` is **already in qcow2 format**. SONiC build produces qcow2 images by default for VS platform, then gzips them for distribution.

Running `qemu-img convert` on the compressed .gz file caused it to:
1. Read the gzip magic bytes as "raw" disk data
2. Create a qcow2 wrapper around the compressed stream
3. Set virtual size to the compressed file size (910 MiB)

The resulting image was structurally valid qcow2 (passed `qemu-img info`) but contained garbage data.

## Correct Procedure

For SONiC VS images distributed as `.img.gz`:

```bash
# Method 1: Decompress and copy (no conversion needed)
gunzip -c sonic-vs.img.gz > sonic-ciscovs.qcow2

# Method 2: Decompress in-place
gunzip sonic-vs.img.gz  # produces sonic-vs.img
mv sonic-vs.img sonic-ciscovs.qcow2

# Verify format
qemu-img info sonic-ciscovs.qcow2
# Should show: format=qcow2, virtual size=16 GiB
```

**DO NOT** use `qemu-img convert -f raw` on the .gz file. The decompressed image is already qcow2.

## Detection

Corrupted image has:
- Virtual size equals compressed .gz file size (~910 MiB)
- Disk size equals compressed .gz file size
- Will not boot in QEMU (reads garbage MBR)

Correct image has:
- Virtual size: 16 GiB (SONiC VS standard)
- Disk size: ~2.4 GiB (actual used space, larger than .gz due to decompression)

## Fix Applied

```bash
# Decompress the source .gz
gunzip -c /path/to/sonic-vs.img.gz > /tmp/sonic-vs.img

# Verify it's already qcow2
qemu-img info /tmp/sonic-vs.img  # format: qcow2 ✓

# Copy (not convert) to final location
cp /tmp/sonic-vs.img ~/.newtlab/images/sonic-ciscovs.qcow2
```

## Lesson Learned

Always verify the format of a decompressed disk image before attempting conversion. Many virtualization platforms (SONiC, OpenStack, etc.) distribute images as **gzipped qcow2**, not **gzipped raw**.

Use `qemu-img info` on the decompressed file first to determine if conversion is actually needed.

## References

- SONiC buildimage produces qcow2 for VS platform: `target/sonic-vs.img` (qcow2) → `sonic-vs.img.gz`
- qemu-img does not auto-detect compression; `-f raw` reads .gz as raw bytes
- newtlab expects qcow2 images at `~/.newtlab/images/*.qcow2`
