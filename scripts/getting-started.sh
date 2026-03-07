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
    echo -e " ${CYAN}Running:${RESET} $*"
    echo ""
    "$@" 2> >(grep -v "Could not initialize audit" >&2)
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
echo " This script walks you through deploying a single SONiC virtual switch"
echo " and using newtron to configure it. Each step explains what it does,"
echo " then runs the command."

# ─── Step 1: Download SONiC image ─────────────────────────────────────────────

header "Step 1: Download SONiC image"

echo " newtron uses QEMU virtual machines running real SONiC software."
echo " The community sonic-vs image is a free download (~1.2 GB compressed)"
echo " from the SONiC project's Azure Pipelines build."
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
echo " CONFIG_DB operations — enough for this walkthrough and the 1node topology."
echo ""
echo " For EVPN VXLAN overlay (multi-switch fabrics with VXLAN tunneling), you"
echo " need a dataplane-capable image:"
echo "   - Cisco NGDP (Silicon One virtual PFE) — available via Cisco engagement"
echo "   - SONiC VPP — community project, VXLAN support in progress"

pause

# ─── Step 2: Build ─────────────────────────────────────────────────────────────

header "Step 2: Build"

echo " Compile the five newtron binaries into bin/."
echo ""

run_cmd make build

echo ""
echo " Binaries:"
ls -1 bin/ | sed 's/^/   /'

pause

# ─── Step 3: Deploy the lab ───────────────────────────────────────────────────

header "Step 3: Deploy the lab"

echo " newtlab creates a QEMU virtual machine running SONiC and wires"
echo " it according to the topology in $SPEC_DIR/."
echo ""
echo " This starts one VM (switch1) with 2 vCPUs, 4 GB RAM."
echo " Boot takes 2–5 minutes depending on your machine."
echo " The --monitor flag shows live status during deployment."
echo ""

run_cmd bin/newtlab deploy 1node --monitor --force

pause

# ─── Step 4: Start newtron-server ─────────────────────────────────────────────

header "Step 4: Start newtron-server"

echo " The server loads specs from the topology directory and exposes"
echo " all operations as HTTP endpoints on port 8080."
echo ""
echo " newtron (the CLI) sends HTTP requests to this server."
echo ""

# Kill any leftover newtron-server from a previous run
existing_pid=$(pgrep -f "newtron-server.*--spec-dir" || true)
if [ -n "$existing_pid" ]; then
    echo -e " ${DIM}Stopping leftover newtron-server (PID $existing_pid)...${RESET}"
    kill "$existing_pid" 2>/dev/null || true
    sleep 1
fi

echo -e " ${CYAN}Running:${RESET} bin/newtron-server --spec-dir $SPEC_DIR &"
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

header "Step 5: Look at the specs"

echo " newtron reads two kinds of spec files:"
echo ""
echo -e " ${BOLD}network.json${RESET} — defines services, VPNs, filters, and routing policy."
echo " This one defines a single service called 'transit':"
echo ""
echo -e " ${DIM}$SPEC_DIR/network.json${RESET}"
cat "$SPEC_DIR/network.json" | sed 's/^/   /'
echo ""
echo " The service type is 'routed' (L3 BGP peering). 'peer_as: request'"
echo " means the caller provides the peer AS number at apply time."
echo ""
echo -e " ${BOLD}profiles/switch1.json${RESET} — per-device identity: ASN, loopback IP,"
echo " platform, and SSH credentials."
echo ""
echo -e " ${DIM}$SPEC_DIR/profiles/switch1.json${RESET}"
cat "$SPEC_DIR/profiles/switch1.json" | sed 's/^/   /'
echo ""
echo " When you apply the transit service, newtron combines these:"
echo " the service spec says 'BGP peer', the profile says 'AS 65001',"
echo " and the CLI provides the interface IP and peer AS."

pause

# ─── Step 6: Dry-run a service operation ──────────────────────────────────────

header "Step 6: Dry-run a service operation"

echo " Apply a transit service to Ethernet0. By default, newtron shows"
echo " what it would write to CONFIG_DB — every table, key, and field."
echo ""
echo " No changes are made to the device in dry-run mode."
echo ""

run_cmd bin/newtron switch1 service apply Ethernet0 transit \
    --ip 10.1.0.0/31 --peer-as 65002

echo ""
echo " Each entry names a real CONFIG_DB table and key:"
echo "   INTERFACE|Ethernet0           → enables IP routing on the port"
echo "   BGP_NEIGHBOR|default|10.1.0.1 → frrcfgd subscribes to this and"
echo "                                   configures FRR with a BGP peer"
echo ""
echo " There is no template engine — newtron computed these entries using"
echo " the device's AS (65001), the interface IP, and the service spec."

pause

# ─── Step 7: Execute ──────────────────────────────────────────────────────────

header "Step 7: Execute"

echo " Add -x to execute. newtron writes to CONFIG_DB via Redis pipeline,"
echo " re-reads every entry to verify, then saves the config."
echo ""

run_cmd bin/newtron switch1 service apply Ethernet0 transit \
    --ip 10.1.0.0/31 --peer-as 65002 -x

pause

# ─── Step 8: Run the test suite ───────────────────────────────────────────────

header "Step 8: Run the test suite"

echo " newtrun runs YAML test scenarios against the server. The 1node-basic"
echo " suite tests service apply/remove, VLAN/VRF lifecycle, and cleanup"
echo " verification — all against the switch you just deployed."
echo " The --monitor flag shows a live status dashboard during the run."
echo ""

run_cmd bin/newtrun start 1node-basic --server http://localhost:8080 --monitor

pause

# ─── Step 9: Tear down ────────────────────────────────────────────────────────

header "Step 9: Tear down"

echo " Stop the VM and clean up."
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
echo " You've deployed a SONiC switch, applied a service, run the E2E test
 suite, and torn it down."
echo ""
echo " Next steps:"
echo "   - Deploy the 2node topology for multi-switch testing"
echo "   - Run the E2E test suite: bin/newtrun start --dir newtrun/suites/2node-primitive"
echo "   - Read the docs: docs/newtron/howto.md, docs/newtlab/howto.md"
echo ""
