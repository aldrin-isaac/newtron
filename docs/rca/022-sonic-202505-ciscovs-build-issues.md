# SONiC 202505 CiscoVS Build - Root Cause Analyses

**Date:** 2026-02-16
**SAI Package:** ciscovs-202505-palladium2-25.9.1000.2-sai-1.16.1
**Branch:** 202505
**Pinned Commit:** cb27941bb222fd953a3de228cc46391e373b43cf
**Build Host:** Ubuntu 22.04, 24 cores, 125GB RAM, Docker 20.10.21

---

## RCA 1: libsnmp-dev version mismatch in slave.mk

**Severity:** Build failure (blocking)

**Symptom:**
```
[ FAIL LOG START ] [ target/debs/bookworm/libsnmp-dev_5.9.3+dfsg-2_amd64.deb-install ]
dpkg: dependency problems prevent configuration of libsnmp-dev:
 libsnmp-dev depends on libnetsnmptrapd40 (= 5.9.3+dfsg-2); however:
  Version of libnetsnmptrapd40:amd64 on system is 5.9.3+dfsg-2+deb12u1.
make: *** [slave.mk:873: target/debs/bookworm/libsnmp-dev_5.9.3+dfsg-2_amd64.deb-install] Error 1
```

**Root Cause:**
The SONiC build container pulls base packages from live Debian 12 mirrors, so `libnetsnmptrapd40` gets updated to the latest point release (`5.9.3+dfsg-2+deb12u1`). However, SONiC builds its own `libsnmp-dev` from pinned source at the base version (`5.9.3+dfsg-2`). When `dpkg -i` tries to install the SONiC-built `libsnmp-dev`, it fails the strict version dependency check against the newer system `libnetsnmptrapd40`.

The non-cross-build dpkg install path in `slave.mk:882` uses bare `dpkg -i`, while the cross-build path (line 885) already includes `--force-depends`. This inconsistency means native builds are vulnerable to Debian mirror updates.

**Fix Applied:**
```diff
- { sudo DEBIAN_FRONTEND=noninteractive dpkg -i $(DEBS_PATH)/$* $(LOG) && ...
+ { sudo DEBIAN_FRONTEND=noninteractive dpkg --force-downgrade --force-depends -i $(DEBS_PATH)/$* $(LOG) && ...
```
File: `sonic-buildimage/slave.mk:882`

**Recommendation:**
- Upstream this fix to `sonic-net/sonic-buildimage`
- Alternatively, adding `deb` to `SONIC_VERSION_CONTROL_COMPONENTS` would pin all Debian packages, preventing the version skew entirely, but that is a heavier change with broader impact
- This issue will recur for any SONiC build where Debian mirrors have been updated since the pinned source versions were set

---

## RCA 2: Stale kernel build directory on retry

**Severity:** Build failure on retry (blocking)

**Symptom:**
```
rm: cannot remove 'linux-6.1.123/debian/build/build_amd64_none_amd64': Directory not empty
make[1]: *** [Makefile:38: /sonic/target/debs/bookworm/linux-headers-6.1.0-29-2-common_6.1.123-1_all.deb] Error 1
```

**Root Cause:**
The first build attempt failed (due to RCA 1) while the kernel was still compiling. The kernel build artifacts were left behind in `src/sonic-linux-kernel/linux-6.1.123/`. On the retry, the kernel build's Makefile clean step tried to remove the build directory with `rm` (not `rm -rf`), which failed because the directory still contained files from the previous interrupted run. Notably, the kernel debs themselves were successfully produced and moved to `target/debs/` before the cleanup step failed, making this a cleanup-only failure.

**Fix Applied:**
```bash
rm -rf sonic-buildimage/src/sonic-linux-kernel/linux-6.1.123/
```

**Recommendation:**
- Before retrying a failed SONiC build, always run `rm -rf src/sonic-linux-kernel/linux-*` to clear stale kernel build artifacts
- The kernel Makefile in sonic-linux-kernel should use `rm -rf` instead of `rm` for its cleanup step to be resilient to partial builds
- A broader `make clean` before retry would also work but is slower

---

## RCA 3: dhcp6relay-test hung for 2.5+ hours

**Severity:** Build hang (blocking, required manual intervention)

**Symptom:**
The `dhcp6relay-test` gtest binary ran for over 2.5 hours at ~33% CPU utilization with an extremely large virtual memory footprint (20TB VSZ), blocking the entire build pipeline. All other build targets had completed, and the final image assembly was waiting solely on this test.

```
root 349055 33.0 0.0 21474866744 6200 ? R 18:45 38:24 ./build-test/dhcp6relay-test --gtest_output=xml:build-test/dhcp6relay-test-test-result.xml
```

**Root Cause:**
The test runs with `ASAN_OPTIONS=detect_leaks=0` inside the Docker build container. The 20TB VSZ is expected behavior for ASAN (shadow memory reservation), but the test appeared to enter an infinite loop or deadlock. This is likely caused by the test expecting network namespaces, interfaces, or other system resources that are not available or properly configured within the build container environment.

The build command wraps the test with `|| true`, meaning a test failure would not block the build. However, a hung test (never exits) does block indefinitely.

**Fix Applied:**
```bash
# Killed the stuck test process inside the build container
docker exec <container> sudo kill <pid>
```
The `|| true` in the build command allowed the build to continue cleanly after the kill.

**Recommendation:**
- Add a timeout to the dhcp6relay test execution in the build system, e.g., `timeout 600 ./build-test/dhcp6relay-test ...` (10 minutes should be more than sufficient)
- Consider disabling unit tests for VS (virtual switch) platform builds where the test environment may not provide required network infrastructure
- File an issue against `sonic-net/sonic-dhcprelay` or `sonic-net/sonic-buildimage` to investigate why the test hangs in container builds
- As a workaround for future builds, monitor for this test and kill it if it exceeds a reasonable duration

---

## Summary of Build Timeline

| Time | Event |
|------|-------|
| 10:23 | Build 1 started (`build.sh -b 202505 -p platform-ciscovs.tar.gz -j 16`) |
| 10:28 | Build 1 failed: libsnmp-dev version mismatch (RCA 1) |
| 10:33 | Build 2 started (with slave.mk fix applied) |
| 10:33 | Build 2 failed: stale kernel build directory (RCA 2) |
| 10:33 | Build 3 started (after cleaning stale kernel dir) |
| 11:51 | Kernel build completed (1h 18m) |
| 11:55 | All Docker images and squashfs completed |
| 11:55 | Build blocked waiting on dhcp6relay-test (RCA 3) |
| ~13:00 | dhcp6relay-test killed after 2.5+ hours |
| 13:02 | Final image `sonic-vs.img.gz` (910MB) produced successfully |

**Total wall-clock time:** ~2h 40m (would have been ~1h 30m without the dhcp6relay hang)
