#!/usr/bin/env bash
#
# getting-started.sh — guided walkthrough from zero to running newtron
#
# Downloads the SONiC community image, builds the project, deploys a
# single-switch lab, and demonstrates service operations — step by step.
#
set -euo pipefail

SONIC_VS_URL="https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=master&platform=vs&target=target/sonic-vs.img.gz"
IMAGE_DIR="$HOME/.newtlab/images"
IMAGE_PATH="$IMAGE_DIR/sonic-vs.qcow2"
SPEC_DIR="newtrun/topologies/1node/specs"
SERVER_PID=""

# Colors (if terminal supports them)
if [ -t 1 ]; then
    BOLD='\033[1m'
    DIM='\033[2m'
    RESET='\033[0m'
    GREEN='\033[32m'
    YELLOW='\033[33m'
    CYAN='\033[36m'
else
    BOLD='' DIM='' RESET='' GREEN='' YELLOW='' CYAN=''
fi

cleanup() {
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

header() {
    echo ""
    echo -e "${BOLD}═══════════════════════════════════════════════════════════════${RESET}"
    echo -e "${BOLD} $1${RESET}"
    echo -e "${BOLD}═══════════════════════════════════════════════════════════════${RESET}"
    echo ""
}

run_cmd() {
    echo -e " ${CYAN}\$${RESET} $*"
    echo ""
    "$@" 2> >(grep -v "Could not initialize audit" >&2)
}

# run_ssh executes a command on switch1 via SSH and displays it
run_ssh() {
    local desc="$1"
    shift
    echo -e " ${DIM}# $desc${RESET}"
    echo -e " ${CYAN}\$${RESET} ssh switch1 \"$*\""
    sshpass -p YourPaSsWoRd ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR -p 13000 admin@127.0.0.1 "$@" 2>/dev/null | sed 's/^/   /' || true
    echo ""
}

pause() {
    echo ""
    echo -e " ${DIM}Press Enter to continue...${RESET}"
    read -r
}

# Ensure we're on Linux x86_64 (SONiC VM images are Intel-only)
if [ "$(uname -s)" != "Linux" ] || [ "$(uname -m)" != "x86_64" ]; then
    echo "Error: SONiC VM images require Linux x86_64 with KVM." >&2
    exit 1
fi

# Ensure we're in the project root
if [ ! -f "Makefile" ] || [ ! -d "cmd/newtron" ]; then
    echo "Error: run this script from the newtron project root." >&2
    exit 1
fi

echo ""
echo -e "${BOLD}newtron — Getting Started${RESET}"
echo ""
echo " newtron is a programmatic configuration system for SONiC switches."
echo ""
echo " On a traditional SONiC switch, you configure things with CLI commands"
echo " (config vlan add, config interface ip add, vtysh) or by editing"
echo " CONFIG_DB directly. That works for one switch. For a fleet of switches"
echo " with consistent services, you need something that can:"
echo ""
echo "   1. Express network intent as specs (\"transit peering on this port\")"
echo "   2. Translate intent to device config (CONFIG_DB entries)"
echo "   3. Validate before writing (catch bad values before they hit Redis)"
echo "   4. Verify after writing (re-read every entry to confirm)"
echo "   5. Clean up completely when removing (no orphaned config)"
echo ""
echo " That's what newtron does. This walkthrough shows the full cycle on"
echo " a single virtual SONiC switch."
echo ""
echo -e " ${DIM}Prerequisites: Linux x86_64 with KVM, Go, make, QEMU, sshpass${RESET}"
echo -e " ${DIM}Total time: ~10 minutes (mostly waiting for the VM to boot)${RESET}"

pause

# ─── Step 1: Download SONiC image ─────────────────────────────────────────────

header "Step 1: Download SONiC image"

echo " The VM runs real SONiC — the same software stack as production:"
echo " Redis, FRR, orchagent, syncd, and all the *mgrd daemons."
echo ""
echo " The community sonic-vs image uses a virtual switch ASIC (no real"
echo " hardware), but CONFIG_DB operations and FRR behavior are identical"
echo " to a physical switch."
echo ""
echo -e " Destination: ${BOLD}$IMAGE_PATH${RESET}"
echo ""

if [ -f "$IMAGE_PATH" ]; then
    echo -e " ${GREEN}Image already exists.${RESET}"
    echo ""
    echo -n " Use existing image? [Y/n] "
    read -r answer
    if [ "${answer,,}" = "n" ]; then
        rm -f "$IMAGE_PATH"
    else
        echo " Keeping existing image."
    fi
fi

if [ ! -f "$IMAGE_PATH" ]; then
    echo -n " Download sonic-vs image? [Y/n] "
    read -r answer
    if [ "${answer,,}" = "n" ]; then
        echo ""
        echo " Cannot continue without a SONiC image."
        echo " Place a sonic-vs qcow2 image at: $IMAGE_PATH"
        exit 0
    fi

    mkdir -p "$IMAGE_DIR"
    echo ""
    echo " Downloading and decompressing..."
    echo -e " ${DIM}(sonic-vs.img.gz is ~1.2 GB; the .img inside is already qcow2 format)${RESET}"
    echo ""
    curl -fSL "$SONIC_VS_URL" | gunzip > "$IMAGE_PATH"
    echo ""
    echo -e " ${GREEN}Done.${RESET} Image saved to $IMAGE_PATH"
fi

echo ""
echo -e " ${YELLOW}Note:${RESET} The community sonic-vs image supports L2/L3 switching, BGP, and"
echo " CONFIG_DB operations — enough for this walkthrough."
echo ""
echo " For EVPN VXLAN overlay (multi-switch fabrics with VXLAN tunneling),"
echo " you need a dataplane-capable image like Cisco Silicon One (NGDP)."

pause

# ─── Step 2: Build ─────────────────────────────────────────────────────────────

header "Step 2: Build"

echo " newtron has five binaries, each with a distinct role:"
echo ""
echo -e "   ${BOLD}newtron${RESET}        CLI — the command you type to configure switches"
echo -e "   ${BOLD}newtron-server${RESET} API server — manages SSH connections to switches,"
echo "                    loads specs, validates and applies config"
echo -e "   ${BOLD}newtlab${RESET}        Lab manager — creates/destroys QEMU VMs, wires"
echo "                    virtual links between them"
echo -e "   ${BOLD}newtrun${RESET}        Test runner — executes YAML test scenarios"
echo "                    against a deployed topology"
echo -e "   ${BOLD}newtlink${RESET}       Link agent — runs on each host to manage virtual"
echo "                    Ethernet bridges between VMs"
echo ""

run_cmd make build

pause

# ─── Step 3: Deploy the lab ───────────────────────────────────────────────────

header "Step 3: Deploy the lab"

echo " newtlab boots a QEMU VM running SONiC and wires it to the host."
echo ""
echo " Inside the VM, the full SONiC stack starts up:"
echo "   - Redis (database container) — CONFIG_DB, APP_DB, ASIC_DB, STATE_DB"
echo "   - FRR via frrcfgd (bgp container) — watches CONFIG_DB for BGP config"
echo "   - intfmgrd, vrfmgrd (swss container) — configure kernel interfaces"
echo "   - orchagent (swss container) — programs the virtual ASIC"
echo ""
echo " This is identical to what runs on a physical switch. The only"
echo " difference is the ASIC is simulated."
echo ""
echo " The --monitor flag shows live status during deployment."
echo -e " ${DIM}Boot takes 2-5 minutes depending on your machine.${RESET}"

pause

run_cmd bin/newtlab deploy 1node --monitor --force

pause

# ─── Step 4: Start newtron-server ─────────────────────────────────────────────

header "Step 4: Start newtron-server"

echo " The architecture is:"
echo ""
echo "   You type:  bin/newtron switch1 service apply ..."
echo "        |"
echo "        v"
echo "   newtron CLI  --(HTTP)--> newtron-server  --(SSH tunnel)--> Redis"
echo "                               |                                |"
echo "                          loads specs,                    CONFIG_DB on"
echo "                          validates,                      the switch"
echo "                          computes entries"
echo ""
echo " The server manages SSH connections (tunneled to Redis on port 6379)"
echo " and holds the spec files that define your network's services."
echo " The CLI is stateless — it sends requests and displays results."

# Kill any leftover newtron-server from a previous run
existing_pid=$(pgrep -f "newtron-server.*--spec-dir" || true)
if [ -n "$existing_pid" ]; then
    echo ""
    echo -e " ${DIM}Stopping leftover newtron-server (PID $existing_pid)...${RESET}"
    kill "$existing_pid" 2>/dev/null || true
    sleep 1
fi

echo ""
echo -e " ${CYAN}\$${RESET} bin/newtron-server --spec-dir $SPEC_DIR &"
echo ""
bin/newtron-server --spec-dir "$SPEC_DIR" > /tmp/newtron-server.log 2>&1 &
SERVER_PID=$!
sleep 2

if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo " Error: newtron-server failed to start." >&2
    SERVER_PID=""
    exit 1
fi

echo -e " ${GREEN}Server started${RESET} (PID $SERVER_PID)"

pause

# ─── Step 5: Look at the specs ────────────────────────────────────────────────

header "Step 5: Understand the spec files"

echo " newtron separates ${BOLD}what${RESET} (network intent) from ${BOLD}where${RESET} (device identity)."
echo ""
echo -e " ${BOLD}network.json${RESET} defines services — abstract descriptions of what"
echo " a port should do. This one defines a transit peering service:"
echo ""
echo -e " ${DIM}$SPEC_DIR/network.json${RESET}"
cat "$SPEC_DIR/network.json" | sed 's/^/   /'
echo ""
echo " 'routed' means L3 — an IP address on the interface with a BGP peer."
echo " 'peer_as: request' means the operator provides the peer AS at apply"
echo " time (it varies per customer/upstream)."
echo ""
echo " Other service types: 'bridged' (L2 VLAN access), 'irb' (L2+L3 with"
echo " SVI), 'evpn-bridged' (VXLAN L2), 'evpn-irb' (VXLAN L2+L3)."
echo ""
echo -e " ${BOLD}profiles/switch1.json${RESET} identifies the device — its ASN, loopback,"
echo " management IP, and credentials:"
echo ""
echo -e " ${DIM}$SPEC_DIR/profiles/switch1.json${RESET}"
cat "$SPEC_DIR/profiles/switch1.json" | sed 's/^/   /'
echo ""
echo " When you say 'apply transit to Ethernet0 with IP 10.1.0.0/31 and"
echo " peer AS 65002', newtron combines the service spec + device profile"
echo " + your parameters to compute the exact CONFIG_DB entries needed."

pause

# ─── Step 6: Dry-run a service operation ──────────────────────────────────────

header "Step 6: Preview — see what newtron would write"

echo " Let's apply a transit service to Ethernet0. Without -x, newtron"
echo " computes the CONFIG_DB entries but doesn't write them — a dry run."
echo ""

run_cmd bin/newtron switch1 service apply Ethernet0 transit \
    --ip 10.1.0.0/31 --peer-as 65002

echo ""
echo " Read the output top to bottom — newtron is telling you exactly what"
echo " it will write to CONFIG_DB:"
echo ""
echo "   INTERFACE|Ethernet0              Enable IP routing on this port"
echo "   INTERFACE|Ethernet0|10.1.0.0/31  Assign the /31 address"
echo "   BGP_NEIGHBOR|default|10.1.0.1    Create a BGP peer at 10.1.0.1"
echo "                                    (the other end of the /31)"
echo "   BGP_NEIGHBOR_AF|...|ipv4_unicast Enable IPv4 unicast for the peer"
echo "   NEWTRON_SERVICE_BINDING|Ethernet0  Record what was applied (so"
echo "                                    'remove' knows what to clean up)"
echo ""
echo " These are the same entries you'd create manually with 'config interface'"
echo " and 'vtysh' commands — but computed from the spec, validated against"
echo " SONiC's YANG schema, and applied atomically."

pause

# ─── Step 7: Execute ──────────────────────────────────────────────────────────

header "Step 7: Apply — write to the switch"

echo " Add -x to execute. newtron will:"
echo "   1. Validate all entries against SONiC YANG constraints"
echo "   2. Write to CONFIG_DB via Redis pipeline (atomic batch)"
echo "   3. Re-read every entry to verify it was written correctly"
echo "   4. Save the running config to disk (config save)"
echo ""

run_cmd bin/newtron switch1 service apply Ethernet0 transit \
    --ip 10.1.0.0/31 --peer-as 65002 -x

pause

echo " The config is now live on the switch. SONiC daemons have already"
echo " reacted to the CONFIG_DB changes:"
echo ""
echo "   - frrcfgd saw BGP_NEIGHBOR → configured FRR with the BGP peer"
echo "   - intfmgrd saw INTERFACE → configured the kernel interface + IP"
echo ""
echo " Let's look at the actual device state:"
echo ""

run_ssh "CONFIG_DB entry for the BGP peer (what newtron wrote to Redis)" \
    "redis-cli -n 4 hgetall 'BGP_NEIGHBOR|default|10.1.0.1'"

run_ssh "Interface IP (intfmgrd processed the CONFIG_DB entry)" \
    "ip addr show Ethernet0 | grep 'inet ' || echo 'IP not yet assigned (intfmgrd still processing)'"

run_ssh "BGP neighbor state (frrcfgd configured FRR from CONFIG_DB)" \
    "vtysh -c 'show bgp neighbors 10.1.0.1' 2>/dev/null | head -3 || echo 'FRR still initializing'"

echo " The BGP peer shows 'Connect' or 'Active' — it's trying to reach"
echo " 10.1.0.1 (which doesn't exist in this single-switch lab). On a real"
echo " network with a peer at that address, the session would come up."

pause

# ─── Step 8: Remove — clean teardown ─────────────────────────────────────────

header "Step 8: Remove — operational symmetry"

echo " Every apply has an equal and opposite remove. This is critical for"
echo " network operations — orphaned config (stale BGP peers, leftover IPs,"
echo " ghost VLAN members) is a constant source of outages."
echo ""
echo " newtron reads the NEWTRON_SERVICE_BINDING to know exactly what was"
echo " applied, then removes every entry in reverse dependency order:"
echo " BGP neighbor AF first, then BGP neighbor, then interface IP, then"
echo " the interface routing config, then the binding record itself."
echo ""

run_cmd bin/newtron switch1 service remove Ethernet0 -x

pause

echo " Verify the switch is clean:"
echo ""

run_ssh "BGP neighbor entry — should be empty (deleted)" \
    "redis-cli -n 4 exists 'BGP_NEIGHBOR|default|10.1.0.1' | sed 's/0/(gone)/'"

run_ssh "Interface IP — should be empty (removed)" \
    "ip addr show Ethernet0 | grep 'inet ' || echo '(no IPv4 address — clean)'"

echo " Every entry that 'apply' created has been removed. The switch is"
echo " back to its pre-service state."

pause

# ─── Step 9: Run the test suite ───────────────────────────────────────────────

header "Step 9: Automated testing with newtrun"

echo " newtrun executes YAML test scenarios that exercise the full stack."
echo " The 1node-basic suite runs 4 scenarios with 25 steps:"
echo ""
echo "   1. boot-ssh         Verify the switch is reachable"
echo "   2. service-lifecycle Apply transit → verify CONFIG_DB → remove → verify clean"
echo "   3. vlan-vrf          Create VLAN 100, add member, create VRF, tear down"
echo "   4. verify-clean      Assert zero leftover entries from any test"
echo ""
echo " Each step calls newtron-server via HTTP (same API the CLI uses)."
echo " The verify steps read CONFIG_DB entries and assert expected values."
echo " The final scenario confirms no test left orphaned config behind."
echo ""
echo " The --monitor flag shows a live dashboard as steps execute."

pause

run_cmd bin/newtrun start 1node-basic --server http://localhost:8080 --monitor

pause

# ─── Step 10: Tear down ──────────────────────────────────────────────────────

header "Step 10: Tear down"

echo " Stop the server and destroy the VM."
echo ""

# Stop server first
if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo -e " ${DIM}Stopping newtron-server...${RESET}"
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
fi

run_cmd bin/newtlab destroy 1node

echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${RESET}"
echo -e "${BOLD} Done!${RESET}"
echo -e "${BOLD}═══════════════════════════════════════════════════════════════${RESET}"
echo ""
echo " What you just did:"
echo ""
echo "   1. Booted a real SONiC switch (Redis, FRR, orchagent — all of it)"
echo "   2. Defined a transit peering service as a spec"
echo "   3. Applied it to Ethernet0 — newtron computed the CONFIG_DB entries,"
echo "      validated them against YANG constraints, and wrote them atomically"
echo "   4. Saw the SONiC daemons react in real time (FRR configured the peer,"
echo "      intfmgrd set up the interface)"
echo "   5. Removed the service — every entry cleaned up, zero orphans"
echo "   6. Ran automated tests that verify the whole lifecycle"
echo ""
echo " Next steps:"
echo ""
echo "   Multi-switch fabric:"
echo "     bin/newtlab deploy 2node"
echo "     bin/newtrun start 2node-primitive --server http://localhost:8080"
echo "     (20 scenarios: BGP, EVPN, VLANs, VRFs, ACLs, QoS, PortChannels)"
echo ""
echo "   EVPN dataplane (requires Cisco Silicon One image):"
echo "     bin/newtlab deploy 3node"
echo "     bin/newtrun start 3node-dataplane --server http://localhost:8080"
echo "     (L3 routing + EVPN L2 bridged + IRB with host-to-host ping)"
echo ""
echo "   Documentation:"
echo "     docs/newtron/howto.md    Operational patterns and provisioning flow"
echo "     docs/newtlab/howto.md    Deploying topologies, troubleshooting"
echo "     docs/newtrun/howto.md    Writing test scenarios"
echo ""
