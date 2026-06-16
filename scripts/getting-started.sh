#!/usr/bin/env bash
#
# getting-started.sh — guided walkthrough from zero to running newtron
#
# Downloads the SONiC community image, builds the project, deploys a
# single-switch lab, and demonstrates service operations — step by step.
#
set -euo pipefail

SONIC_VS_URL="https://sonic-build.azurewebsites.net/api/sonic/artifacts?branchName=202505&platform=vs&buildId=1057313&target=target/sonic-vs.img.gz"
IMAGE_DIR="$HOME/.newtlab/images"
IMAGE_PATH="$IMAGE_DIR/sonic-vs.qcow2"
SPEC_DIR="newtrun/topologies/1node-vs/specs"
SERVER_PID=""        # newt-server PID (single process running all three engines)
TOTAL_STEPS=12

# ── Colors and formatting ────────────────────────────────────────────────────

if [ -t 1 ]; then
    BOLD='\033[1m'
    DIM='\033[2m'
    RESET='\033[0m'
    WHITE='\033[97m'
    BLUE='\033[34m'
    BLUE_BOLD='\033[1;34m'
    GREEN='\033[32m'
    GREEN_BOLD='\033[1;32m'
    YELLOW='\033[33m'
    CYAN='\033[36m'
    GRAY='\033[90m'
    MAGENTA='\033[35m'
    MAGENTA_BOLD='\033[1;35m'
else
    BOLD='' DIM='' RESET='' WHITE='' BLUE='' BLUE_BOLD=''
    GREEN='' GREEN_BOLD='' YELLOW='' CYAN='' GRAY=''
    MAGENTA='' MAGENTA_BOLD=''
fi

# Box-drawing characters
H='─'  # horizontal
TL='╭' TR='╮' BL='╰' BR='╯' V='│'

cleanup() {
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# ── Visual helpers ───────────────────────────────────────────────────────────

# Print a horizontal rule of a given width
hrule() {
    local width=${1:-76}
    local line=""
    for ((i=0; i<width; i++)); do line+="$H"; done
    echo "$line"
}

# Step header with progress indicator
#   header <step_number> <title>
header() {
    local step=$1
    local title=$2
    local bar=""
    local progress=""

    # Build progress dots: ● for completed, ◉ for current, ○ for remaining
    for ((i=1; i<=TOTAL_STEPS; i++)); do
        if [ "$i" -lt "$step" ]; then
            progress+="${GREEN}●${RESET}"
        elif [ "$i" -eq "$step" ]; then
            progress+="${MAGENTA_BOLD}◉${RESET}"
        else
            progress+="${GRAY}○${RESET}"
        fi
        if [ "$i" -lt "$TOTAL_STEPS" ]; then
            progress+=" "
        fi
    done

    echo ""
    echo -e "  ${GRAY}$(hrule 76)${RESET}"
    echo ""
    echo -e "  ${MAGENTA_BOLD}STEP ${step}${RESET}${GRAY}/${TOTAL_STEPS}${RESET}  ${BOLD}${WHITE}${title}${RESET}"
    echo -e "  ${progress}"
    echo ""
    echo -e "  ${GRAY}$(hrule 76)${RESET}"
    echo ""
}

run_cmd() {
    echo -e "  ${GRAY}\$${RESET} ${CYAN}$*${RESET}"
    echo ""
    "$@" 2> >(grep -v "Could not initialize audit" >&2)
}

# run_ssh executes a command on switch1 via SSH and displays it
run_ssh() {
    local desc="$1"
    shift
    echo -e "  ${GRAY}# $desc${RESET}"
    echo -e "  ${GRAY}\$${RESET} ${CYAN}ssh switch1 \"$*\"${RESET}"
    sshpass -p YourPaSsWoRd ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR -p 13000 admin@127.0.0.1 "$@" 2>/dev/null | sed 's/^/    /' || true
    echo ""
}

pause() {
    echo ""
    echo -e "  ${GRAY}Press Enter to continue ${DIM}▸${RESET}"
    read -r
}

note() {
    echo -e "  ${YELLOW}▸${RESET} $*"
}

# ── Preconditions ────────────────────────────────────────────────────────────

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

# Clean up any leftover state from a previous run. newt-server must be up
# for `newtrun stop` to reach the runner registry; if it's down, fall
# through to newtlab destroy which doesn't depend on the server.
pgrep -f "bin/newt-server" >/dev/null 2>&1 && \
    bin/newtrun stop 1node-vs-basic &>/dev/null || true
bin/newtlab destroy 1node-vs &>/dev/null || true

# ── Title card ───────────────────────────────────────────────────────────────

echo ""
W=76  # box inner width
box_line() {
    local text="$1"
    local pad=$((W - ${#text}))
    printf "  ${BLUE}${V}${RESET}%s%${pad}s${BLUE}${V}${RESET}\n" "$text" ""
}
echo -e "  ${BLUE}${TL}$(hrule $W)${TR}${RESET}"
box_line ""
echo -e "  ${BLUE}${V}${RESET}   ${BLUE_BOLD}newtron${RESET}$(printf '%66s' '')${BLUE}${V}${RESET}"
echo -e "  ${BLUE}${V}${RESET}   ${DIM}Getting Started${RESET}$(printf '%58s' '')${BLUE}${V}${RESET}"
box_line ""
echo -e "  ${BLUE}${BL}$(hrule $W)${BR}${RESET}"
echo ""
echo -e "  ${BLUE_BOLD}newtron${RESET} is a programmatic configuration system for SONiC switches."
echo ""
echo "  On a traditional SONiC switch, you configure things with CLI commands"
echo "  (config vlan add, config interface ip add, vtysh) or by editing"
echo "  CONFIG_DB directly. That works for one switch. For a fleet of switches"
echo "  with consistent services, you need something that can:"
echo ""
echo -e "    ${WHITE}1.${RESET} Express network intent as specs (\"transit peering on this port\")"
echo -e "    ${WHITE}2.${RESET} Translate intent to device config (CONFIG_DB entries)"
echo -e "    ${WHITE}3.${RESET} Validate before writing (catch bad values before they hit CONFIG_DB)"
echo -e "    ${WHITE}4.${RESET} Verify after writing (re-read every entry to confirm)"
echo -e "    ${WHITE}5.${RESET} Clean up completely when removing (no orphaned config)"
echo ""
echo -e "  That's what ${BLUE_BOLD}newtron${RESET} does. This walkthrough shows the full cycle on"
echo "  a single virtual SONiC switch."
echo ""
echo -e "  ${GRAY}Prerequisites: Linux x86_64 with KVM, Go, make, QEMU, sshpass${RESET}"
echo -e "  ${GRAY}Total time: ~10 minutes (mostly waiting for the VM to boot)${RESET}"

pause

# ─── Step 1: Download SONiC image ─────────────────────────────────────────────

header 1 "Download SONiC image"

echo "  The VM runs real SONiC -- the same software stack as production:"
echo "  Redis, FRR, orchagent, syncd, and all the *mgrd daemons."
echo ""
echo "  The community sonic-vs image uses a virtual switch ASIC (no real"
echo "  hardware), but CONFIG_DB operations and FRR behavior are identical"
echo "  to a physical switch."
echo ""
echo -e "  Destination: ${BOLD}$IMAGE_PATH${RESET}"
echo ""

if [ -f "$IMAGE_PATH" ]; then
    echo -e "  ${GREEN}Image already exists.${RESET}"
    echo ""
    echo -n "  Use existing image? [Y/n] "
    read -r answer
    if [ "${answer,,}" = "n" ]; then
        rm -f "$IMAGE_PATH"
    else
        echo "  Keeping existing image."
    fi
fi

if [ ! -f "$IMAGE_PATH" ]; then
    echo -n "  Download sonic-vs image? [Y/n] "
    read -r answer
    if [ "${answer,,}" = "n" ]; then
        echo ""
        echo "  Cannot continue without a SONiC image."
        echo "  Place a sonic-vs qcow2 image at: $IMAGE_PATH"
        exit 0
    fi

    mkdir -p "$IMAGE_DIR"
    echo ""
    echo "  Downloading and decompressing..."
    echo -e "  ${GRAY}(sonic-vs.img.gz is ~1.2 GB; the .img inside is already qcow2 format)${RESET}"
    echo ""
    curl -fSL "$SONIC_VS_URL" | gunzip > "$IMAGE_PATH"
    echo ""
    echo -e "  ${GREEN}Done.${RESET} Image saved to $IMAGE_PATH"
fi

echo ""
note "The community sonic-vs image supports L2/L3 switching, BGP, and"
echo "    CONFIG_DB operations -- enough for this walkthrough."
echo ""
echo "    For EVPN VXLAN overlay (multi-switch fabrics with VXLAN tunneling),"
echo "    you need a dataplane-capable image like Cisco Silicon One (NGDP)."

pause

# ─── Step 2: Build ─────────────────────────────────────────────────────────────

header 2 "Build"

echo -e "  ${BLUE_BOLD}newtron${RESET} ships eight binaries; the five you'll touch in this walkthrough are:"
echo ""
echo -e "    ${BLUE_BOLD}newtron${RESET}        CLI -- the command you type to configure switches"
echo -e "    ${BLUE_BOLD}newt-server${RESET}    Aggregated HTTP server -- runs newtron, newtrun,"
echo "                     and newtlab engines in one process on :18080"
echo -e "    ${BLUE_BOLD}newtlab${RESET}        Lab manager -- creates/destroys QEMU VMs, wires"
echo "                     virtual links between them"
echo -e "    ${BLUE_BOLD}newtrun${RESET}        Test runner -- executes YAML test scenarios"
echo "                     against a deployed topology"
echo -e "    ${BLUE_BOLD}newtlink${RESET}       Link agent -- runs on each host to manage virtual"
echo "                     Ethernet bridges between VMs"
echo ""

run_cmd make build

pause

# ─── Step 3: Start newt-server ────────────────────────────────────────────────

header 3 "Start ${BLUE_BOLD}newt-server${RESET}"

echo "  The architecture is:"
echo ""
echo -e "  ${GRAY}  You type:  bin/newtron switch1 service apply ...${RESET}"
echo -e "  ${GRAY}       |${RESET}"
echo -e "  ${GRAY}       v${RESET}"
echo -e "  ${GRAY}  newtron CLI  --(HTTP /newtron/v1/)-->  newt-server  --(in-process)-->  newtron engine${RESET}"
echo -e "  ${GRAY}                                            |                                |${RESET}"
echo -e "  ${GRAY}                                       one port,                       loads specs,${RESET}"
echo -e "  ${GRAY}                                       three engines                   SSH-tunnels to Redis${RESET}"
echo ""
echo "  newt-server runs newtron, newtrun, and newtlab in one process on"
echo "  port 18080. Routes are dispatched by URL prefix: /newtron/v1/...,"
echo "  /newtrun/v1/..., /newtlab/v1/.... See docs/newt-server.md for the"
echo "  composition rationale."
echo ""
echo "  We start it before the lab so the next step's deploy monitor can"
echo "  read live link telemetry: newtlink (the bridge agent) pushes"
echo "  per-link byte counters to newt-server every 5 seconds, and"
echo -e "  ${BOLD}newtlab status${RESET} reads them via HTTP to populate the link table."

# Kill any leftover newt-server from a previous run
existing_pid=$(pgrep -f "newt-server.*-spec-dir" || true)
if [ -n "$existing_pid" ]; then
    echo ""
    echo -e "  ${GRAY}Stopping leftover newt-server (PID $existing_pid)...${RESET}"
    kill "$existing_pid" 2>/dev/null || true
    sleep 1
fi

# Check that port 18080 is free
if ss -tlnH 'sport = :18080' 2>/dev/null | grep -q 8080; then
    echo ""
    echo "  Error: port 18080 is already in use." >&2
    echo "  newt-server needs port 18080. Stop whatever is using it and re-run." >&2
    exit 1
fi

echo ""
echo -e "  ${GRAY}\$${RESET} ${CYAN}bin/newt-server --spec-dir $SPEC_DIR &${RESET}"
echo ""
bin/newt-server --spec-dir "$SPEC_DIR" > /tmp/newt-server.log 2>&1 &
SERVER_PID=$!
sleep 2

if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "  Error: newt-server failed to start." >&2
    SERVER_PID=""
    exit 1
fi

echo -e "  ${GREEN}Server started${RESET} (PID $SERVER_PID)"

pause

# ─── Step 4: Deploy the lab ───────────────────────────────────────────────────

header 4 "Deploy the lab"

echo -e "  ${BLUE_BOLD}newtlab${RESET} boots a QEMU VM running SONiC and wires it to the host."
echo ""
echo "  Inside the VM, the full SONiC stack starts up:"
echo -e "    ${GRAY}${H}${RESET} Redis (database container) -- CONFIG_DB, APP_DB, ASIC_DB, STATE_DB"
echo -e "    ${GRAY}${H}${RESET} FRR via frrcfgd (bgp container) -- watches CONFIG_DB for BGP config"
echo -e "    ${GRAY}${H}${RESET} intfmgrd, vrfmgrd (swss container) -- configure kernel interfaces"
echo -e "    ${GRAY}${H}${RESET} orchagent (swss container) -- programs the virtual ASIC"
echo ""
echo "  This is identical to what runs on a physical switch. The only"
echo "  difference is the ASIC is simulated."
echo ""
echo -e "  The ${BOLD}--monitor${RESET} flag shows live status during deployment."
echo "  Since newt-server is already up, the link table populates with"
echo "  byte counters and session state as soon as bridges come online."
echo -e "  ${GRAY}Boot takes 2-5 minutes depending on your machine.${RESET}"

pause

run_cmd bin/newtlab deploy 1node-vs --monitor --force

pause

# ─── Step 5: Initialize the device ────────────────────────────────────────────

header 5 "Initialize the device"

echo "  SONiC ships with bgpcfgd, which silently ignores dynamic CONFIG_DB"
echo -e "  entries (BGP_NEIGHBOR, VRF, etc.). ${BLUE_BOLD}newtron${RESET} requires frrcfgd (unified"
echo "  config mode) so all CONFIG_DB writes are processed by FRR."
echo ""
echo -e "  ${BLUE_BOLD}newtron init${RESET} enables frrcfgd, restarts the bgp container, and"
echo "  saves the config. It's idempotent -- safe to run multiple times."
echo ""
echo -e "  In this walkthrough, ${BLUE_BOLD}newtlab${RESET}'s boot patch already enabled frrcfgd"
echo -e "  during deploy. On a production device without ${BLUE_BOLD}newtlab${RESET}, this is the"
echo -e "  required first step before any ${BLUE_BOLD}newtron${RESET} operations."
echo ""

run_cmd bin/newtron switch1 init

echo ""
note "On a production device with active BGP sessions, init requires"
echo "    --force because it restarts the bgp container (dropping all sessions)"
echo "    and replaces frr.conf (losing any vtysh-only configuration)."

pause

# ─── Step 6: Look at the specs ────────────────────────────────────────────────

header 6 "Understand the spec files"

echo -e "  ${BLUE_BOLD}newtron${RESET} separates ${BOLD}what${RESET} (network intent) from ${BOLD}where${RESET} (device identity)."
echo ""
echo -e "  ${BOLD}network.json${RESET} defines services -- abstract descriptions of what"
echo "  a port should do. This one defines a transit peering service:"
echo ""
echo -e "  ${GRAY}$SPEC_DIR/network.json${RESET}"
cat "$SPEC_DIR/network.json" | sed 's/^/    /'
echo ""
echo "  'routed' means L3 -- an IP address on the interface with a BGP peer."
echo "  'peer_as: request' means the operator provides the peer AS at apply"
echo "  time (it varies per customer/upstream)."
echo ""
echo "  Other service types: 'bridged' (L2 VLAN access), 'irb' (L2+L3 with"
echo "  SVI), 'evpn-bridged' (VXLAN L2), 'evpn-irb' (VXLAN L2+L3)."
echo ""
echo -e "  ${BOLD}profiles/switch1.json${RESET} identifies the device -- its ASN, loopback,"
echo "  management IP, and credentials:"
echo ""
echo -e "  ${GRAY}$SPEC_DIR/profiles/switch1.json${RESET}"
cat "$SPEC_DIR/profiles/switch1.json" | sed 's/^/    /'
echo ""
echo "  When you say 'apply transit to Ethernet0 with IP 10.1.0.0/31 and"
echo -e "  peer AS 65002', ${BLUE_BOLD}newtron${RESET} combines the service spec + device profile"
echo "  + your parameters to compute the exact CONFIG_DB entries needed."

pause

# ─── Step 7: Set up the device ────────────────────────────────────────────────

header 7 "Set up the device"

echo -e "  ${BLUE_BOLD}newtron init${RESET} enabled frrcfgd. Now ${BLUE_BOLD}newtron device setup${RESET} creates the"
echo "  baseline infrastructure: device metadata, loopback, and BGP."
echo ""
echo "  This creates the 'device' root intent -- the anchor of the intent DAG."
echo "  All subsequent operations (service apply, VRF create, EVPN setup) attach"
echo "  their intents as children of this root. Without it, service operations"
echo "  would fail because the parent intent doesn't exist."
echo ""
echo "  Values come from the device profile (ASN, loopback IP) and flags."
echo ""

run_cmd bin/newtron switch1 device setup \
    --bgp-asn 65001 --type LeafRouter --hwsku Force10-S6000 -x

pause

# ─── Step 8: Dry-run a service operation ──────────────────────────────────────

header 8 "Preview -- see what ${BLUE_BOLD}newtron${RESET}${BOLD}${WHITE} would write"

echo -e "  Let's apply a transit service to Ethernet0. Without -x, ${BLUE_BOLD}newtron${RESET}"
echo "  computes the CONFIG_DB entries but doesn't write them -- a dry run."
echo ""

run_cmd bin/newtron switch1 service apply Ethernet0 transit \
    --ip 10.1.0.0/31 --peer-as 65002

echo ""
echo -e "  Read the output top to bottom -- ${BLUE_BOLD}newtron${RESET} is telling you exactly what"
echo "  it will write to CONFIG_DB:"
echo ""
echo -e "    ${GRAY}INTERFACE|Ethernet0${RESET}               Enable IP routing on this port"
echo -e "    ${GRAY}INTERFACE|Ethernet0|10.1.0.0/31${RESET}   Assign the /31 address"
echo -e "    ${GRAY}BGP_PEER_GROUP|default|TRANSIT${RESET}    Create a peer group for the service"
echo -e "    ${GRAY}BGP_NEIGHBOR|default|10.1.0.1${RESET}     Create a BGP peer at 10.1.0.1"
echo "                                      (the other end of the /31)"
echo -e "    ${GRAY}BGP_NEIGHBOR_AF|...|ipv4_unicast${RESET}  Enable IPv4 unicast for the peer"
echo -e "    ${GRAY}NEWTRON_INTENT|service|TRANSIT${RESET}    Record what was applied (so"
echo "                                      'remove' knows what to clean up)"
echo ""
echo "  Device-level entries (metadata, loopback, BGP globals) don't appear"
echo -e "  here -- ${BLUE_BOLD}device setup${RESET} already created those. Only the service-specific"
echo "  entries are shown: the interface IP, the BGP peer, and the intent record."
echo ""
echo "  These are the same entries you'd create manually with 'config interface'"
echo "  and 'vtysh' commands -- but computed from the spec, validated against"
echo "  SONiC's YANG schema, and applied atomically."

pause

# ─── Step 9: Execute ──────────────────────────────────────────────────────────

header 9 "Apply -- write to the switch"

echo -e "  Add ${BOLD}-x${RESET} to execute. ${BLUE_BOLD}newtron${RESET} will:"
echo -e "    ${WHITE}1.${RESET} Validate all entries against SONiC YANG constraints"
echo -e "    ${WHITE}2.${RESET} Write to CONFIG_DB via Redis pipeline (atomic batch)"
echo -e "    ${WHITE}3.${RESET} Re-read every entry to verify it was written correctly"
echo -e "    ${WHITE}4.${RESET} Save the running config to disk (config save)"
echo ""

run_cmd bin/newtron switch1 service apply Ethernet0 transit \
    --ip 10.1.0.0/31 --peer-as 65002 -x

pause

echo "  The config is now live on the switch. SONiC daemons react to"
echo "  CONFIG_DB changes in real time -- intfmgrd programs the kernel"
echo "  interface, frrcfgd configures FRR with the BGP peer."
echo ""
echo "  Let's look at the actual device state:"
echo ""

# Wait for frrcfgd to process CONFIG_DB entries and program the BGP neighbor.
echo -e "  ${GRAY}Waiting for SONiC daemons to process CONFIG_DB changes...${RESET}"
for i in $(seq 1 30); do
    if sshpass -p YourPaSsWoRd ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR -p 13000 admin@127.0.0.1 \
        "docker exec bgp vtysh -c 'show bgp neighbors 10.1.0.1' 2>/dev/null | grep -q 'BGP neighbor'" 2>/dev/null; then
        break
    fi
    sleep 1
done
echo ""

run_ssh "CONFIG_DB: BGP peer entry (what newtron wrote)" \
    "redis-cli -n 4 hgetall 'BGP_NEIGHBOR|default|10.1.0.1'"

run_ssh "Kernel: interface IP (intfmgrd processed the CONFIG_DB entry)" \
    "ip addr show Ethernet0 | grep 'inet ' | grep -v inet6 || echo '(intfmgrd still processing)'"

run_ssh "FRR: BGP neighbor (frrcfgd read CONFIG_DB and configured FRR)" \
    "docker exec bgp vtysh -c 'show bgp neighbors 10.1.0.1' 2>/dev/null | head -5"

run_ssh "CONFIG_DB: intent record (newtron's record of what was applied)" \
    "redis-cli -n 4 hgetall 'NEWTRON_INTENT|Ethernet0'"

echo -e "  The chain: ${BLUE_BOLD}newtron${RESET} writes CONFIG_DB --> frrcfgd reads it -->"
echo "  FRR configures the BGP peer. The intent record captures what"
echo "  was applied so 'remove' knows what to clean up, even if the"
echo "  service spec changes between apply and remove."

pause

# ─── Step 10: Remove — clean teardown ────────────────────────────────────────

header 10 "Remove -- operational symmetry"

echo "  Every apply has an equal and opposite remove. This is critical for"
echo "  network operations -- orphaned config (stale BGP peers, leftover IPs,"
echo "  ghost VLAN members) is a constant source of outages."
echo ""
echo -e "  ${BLUE_BOLD}newtron${RESET} reads the NEWTRON_INTENT record to know exactly what was"
echo "  applied, then removes every entry in reverse dependency order:"
echo "  BGP neighbor AF first, then BGP neighbor, then interface IP, then"
echo "  the interface routing config, then the binding record itself."
echo ""

run_cmd bin/newtron switch1 service remove Ethernet0 -x

pause

echo "  Verify the switch is clean:"
echo ""

run_ssh "BGP neighbor entry -- should be empty (deleted)" \
    "redis-cli -n 4 exists 'BGP_NEIGHBOR|default|10.1.0.1' | sed 's/0/(gone)/'"

run_ssh "Interface IP -- should be empty (removed)" \
    "ip addr show Ethernet0 | grep 'inet ' || echo '(no IPv4 address -- clean)'"

echo "  Every entry that 'apply' created has been removed. The switch is"
echo "  back to its pre-service state."

pause

# ─── Step 11: Run the test suite ──────────────────────────────────────────────

header 11 "Automated testing with ${BLUE_BOLD}newtrun${RESET}"

echo -e "  ${BLUE_BOLD}newtrun${RESET} executes YAML test scenarios that exercise the full stack."
echo "  The 1node-vs-basic suite runs 9 scenarios:"
echo ""
echo -e "    ${WHITE}1.${RESET} ${BOLD}boot-ssh${RESET}                   Verify the switch is reachable"
echo -e "    ${WHITE}2.${RESET} ${BOLD}setup-device${RESET}               Create device root intent (metadata, loopback, BGP)"
echo -e "    ${WHITE}3.${RESET} ${BOLD}service-lifecycle${RESET}          Apply transit --> verify --> remove --> verify clean"
echo -e "    ${WHITE}4.${RESET} ${BOLD}vlan-vrf${RESET}                   Create VLAN 100, add member, create VRF, tear down"
echo -e "    ${WHITE}5.${RESET} ${BOLD}verify-clean${RESET}               Assert zero leftover entries from any test"
echo -e "    ${WHITE}6.${RESET} ${BOLD}configdb-snapshot-actuated${RESET} Read CONFIG_DB snapshot in actuated mode"
echo -e "    ${WHITE}7.${RESET} ${BOLD}perwrite-actuated${RESET}          Per-write actuated-mode verification"
echo -e "    ${WHITE}8.${RESET} ${BOLD}service-projection-actuated${RESET} Service projection after actuation"
echo -e "    ${WHITE}9.${RESET} ${BOLD}projection-diff-actuated${RESET}   Diff actuated-mode projection vs intent"
echo ""
echo -e "  Each step calls into newt-server via HTTP (same API the CLI uses)."
echo "  The verify steps read CONFIG_DB entries and assert expected values."
echo "  The final scenario confirms no test left orphaned config behind."
echo ""
echo "  newt-server hosts the run registry for newtrun — no separate server"
echo "  process is needed; the newt-server started in Step 4 already serves"
echo "  the /newtrun/v1/ routes that the CLI dials."
echo ""
echo -e "  The ${BOLD}--monitor${RESET} flag shows a live dashboard as steps execute."

pause

run_cmd bin/newtrun start 1node-vs-basic --server http://localhost:18080 --monitor

echo ""
run_cmd bin/newtrun status --suite 1node-vs-basic

pause

# ─── Step 12: Tear down ──────────────────────────────────────────────────────

header 12 "Tear down"

echo "  Stop the servers and destroy the VM."
echo ""

# `newtrun stop <suite>` cancels the runner (if any), destroys the
# topology, and clears suite state. It needs newt-server up: the CLI
# dials newt-server, which routes /newtrun/v1/ to the in-process
# newtrun engine.
echo -e "  ${GRAY}\$${RESET} ${CYAN}bin/newtrun stop 1node-vs-basic${RESET}"
echo ""
bin/newtrun stop 1node-vs-basic 2>/dev/null || true

# Stop newt-server (single process; all three engines drain together
# via the OnShutdown hooks registered in cmd/newt-server/main.go).
if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo -e "  ${GRAY}Stopping newt-server...${RESET}"
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
fi

echo ""
echo "  Verify everything is cleaned up:"
echo ""
# newtrun status now goes through newt-server, which is down. The
# CLI prints "newtrun-server is not running" — the correct end state.
run_cmd bin/newtrun status --suite 1node-vs-basic || true
run_cmd bin/newtlab status 1node-vs || true

# ── Completion ───────────────────────────────────────────────────────────────

gbox() {
    local text="$1"
    local pad=$((W - ${#text}))
    printf "  ${GREEN}${V}${RESET}%s%${pad}s${GREEN}${V}${RESET}\n" "$text" ""
}
echo ""
echo -e "  ${GREEN}${TL}$(hrule $W)${TR}${RESET}"
gbox ""
echo -e "  ${GREEN}${V}${RESET}   ${GREEN_BOLD}Complete${RESET}$(printf '%65s' '')${GREEN}${V}${RESET}"
gbox ""
gbox "   What you just did:"
gbox ""
gbox "    1. Booted a real SONiC switch (Redis, FRR, orchagent)"
gbox "    2. Defined a transit peering service as a spec"
gbox "    3. Applied it -- validated, wrote atomically, verified"
gbox "    4. Saw SONiC daemons react in real time"
gbox "    5. Removed the service -- zero orphans"
gbox "    6. Ran automated tests verifying the full lifecycle"
gbox ""
echo -e "  ${GREEN}${BL}$(hrule $W)${BR}${RESET}"
echo ""
echo -e "  ${BOLD}Next steps${RESET}"
echo ""
echo -e "  ${WHITE}Multi-switch fabric:${RESET}"
echo -e "    ${CYAN}bin/newtlab deploy 2node-ngdp${RESET}"
echo -e "    ${CYAN}bin/newtrun start 2node-ngdp-primitive --server http://localhost:18080${RESET}"
echo -e "    ${GRAY}(21 scenarios: BGP, EVPN, VLANs, VRFs, ACLs, QoS, PortChannels)${RESET}"
echo ""
echo -e "  ${WHITE}EVPN dataplane (requires Cisco Silicon One image):${RESET}"
echo -e "    ${CYAN}bin/newtlab deploy 3node-ngdp${RESET}"
echo -e "    ${CYAN}bin/newtrun start 3node-ngdp-dataplane --server http://localhost:18080${RESET}"
echo -e "    ${GRAY}(L3 routing + EVPN L2 bridged + IRB with host-to-host ping)${RESET}"
echo ""
echo -e "  ${WHITE}Documentation:${RESET}"
echo -e "    docs/newtron/howto.md    ${GRAY}Operational patterns and provisioning${RESET}"
echo -e "    docs/newtlab/howto.md    ${GRAY}Deploying topologies, troubleshooting${RESET}"
echo -e "    docs/newtrun/howto.md    ${GRAY}Writing test scenarios${RESET}"
echo ""
