#!/usr/bin/env bash
# Newtron integration test lab management script.
# Usage: ./testlab/setup.sh <command>
# Commands: redis-start, redis-stop, redis-seed, redis-ip, status
#           lab-start, lab-stop, lab-status

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
CONTAINER_NAME="newtron-test-redis"
SEED_DIR="${SCRIPT_DIR}/seed"
GENERATED_DIR="${SCRIPT_DIR}/.generated"
LAB_STATE_FILE="${GENERATED_DIR}/.lab-state"

redis_start() {
    echo "Starting test Redis container..."
    # Remove stale container if it exists but is stopped
    if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
        if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
            docker rm "${CONTAINER_NAME}" >/dev/null 2>&1 || true
        else
            echo "Redis container already running."
            return 0
        fi
    fi
    docker run -d --name "${CONTAINER_NAME}" \
        redis:7-alpine \
        redis-server --databases 16 --save "" >/dev/null
    echo "Waiting for Redis to be healthy..."
    for i in $(seq 1 30); do
        if docker exec "${CONTAINER_NAME}" redis-cli ping 2>/dev/null | grep -q PONG; then
            echo "Redis is ready."
            return 0
        fi
        sleep 1
    done
    echo "ERROR: Redis did not become healthy in time." >&2
    return 1
}

redis_stop() {
    echo "Stopping test Redis container..."
    docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}

redis_ip() {
    docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${CONTAINER_NAME}" 2>/dev/null
}

redis_seed() {
    local ip
    ip=$(redis_ip)
    if [[ -z "$ip" ]]; then
        echo "ERROR: Cannot determine Redis container IP. Is the container running?" >&2
        return 1
    fi

    echo "Seeding CONFIG_DB (DB 4) on ${ip}:6379..."
    seed_db "$ip" 4 "${SEED_DIR}/configdb.json"

    echo "Seeding STATE_DB (DB 6) on ${ip}:6379..."
    seed_db "$ip" 6 "${SEED_DIR}/statedb.json"

    echo "Seed complete."
}

seed_db() {
    local host="$1"
    local db="$2"
    local json_file="$3"

    # Flush the target database first
    redis-cli -h "$host" -n "$db" FLUSHDB >/dev/null

    # Parse JSON and insert each TABLE|key with HSET
    # The JSON structure is: { "TABLE": { "key": { "field": "value", ... }, ... }, ... }
    python3 -c "
import json, subprocess, sys

with open('${json_file}') as f:
    data = json.load(f)

for table, entries in data.items():
    for key, fields in entries.items():
        redis_key = f'{table}|{key}'
        if not fields:
            # Empty hash - use a dummy field to create the key
            cmd = ['redis-cli', '-h', '${host}', '-n', '${db}', 'HSET', redis_key, '_placeholder', '']
            subprocess.run(cmd, capture_output=True, check=True)
            subprocess.run(['redis-cli', '-h', '${host}', '-n', '${db}', 'HDEL', redis_key, '_placeholder'],
                          capture_output=True, check=True)
            # For empty hashes, just set and delete to create key structure
            # Actually, just HSET an empty string field
            cmd = ['redis-cli', '-h', '${host}', '-n', '${db}', 'HSET', redis_key, 'NULL', '']
            subprocess.run(cmd, capture_output=True, check=True)
            continue
        args = []
        for field, value in fields.items():
            args.extend([field, str(value)])
        cmd = ['redis-cli', '-h', '${host}', '-n', '${db}', 'HSET', redis_key] + args
        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode != 0:
            print(f'ERROR: failed to seed {redis_key}: {result.stderr}', file=sys.stderr)
            sys.exit(1)

print(f'Seeded {len(data)} tables from ${json_file}')
"
}

status() {
    echo "=== Newtron Test Lab Status ==="
    if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "${CONTAINER_NAME}"; then
        local ip
        ip=$(redis_ip)
        echo "Redis: RUNNING at ${ip}:6379"
        local keys4 keys6
        keys4=$(redis-cli -h "$ip" -n 4 DBSIZE 2>/dev/null || echo "?")
        keys6=$(redis-cli -h "$ip" -n 6 DBSIZE 2>/dev/null || echo "?")
        echo "  CONFIG_DB (4): ${keys4}"
        echo "  STATE_DB  (6): ${keys6}"
    else
        echo "Redis: STOPPED"
    fi
}

# =============================================================================
# Containerlab / E2E lab management
# =============================================================================

# Get all container "name ip" pairs directly from Docker (more reliable than
# containerlab inspect --format json, which can return partial results).
clab_inspect_nodes() {
    local clab_file="$1"
    python3 -c "
import yaml, subprocess
with open('${clab_file}') as f:
    data = yaml.safe_load(f)
topo_name = data.get('name', '')
for name in data.get('topology', {}).get('nodes', {}):
    container = f'clab-{topo_name}-{name}'
    result = subprocess.run(
        ['docker', 'inspect', '--format',
         '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',
         container],
        capture_output=True, text=True)
    ip = result.stdout.strip()
    if ip:
        print(f'{container} {ip}')
" 2>/dev/null || true
}

# Extract SSH credentials (USERNAME/PASSWORD) from the first SONiC node in the
# clab YAML. Returns "user pass".
clab_ssh_creds() {
    local clab_file="$1"
    python3 -c "
import yaml
with open('${clab_file}') as f:
    data = yaml.safe_load(f)
for name, node in data.get('topology', {}).get('nodes', {}).items():
    if node.get('kind', '') != 'linux':
        env = node.get('env', {})
        user = env.get('USERNAME', '')
        passwd = env.get('PASSWORD', '')
        if user and passwd:
            print(f'{user} {passwd}')
            break
"
}

# Return only SONiC nodes (exclude kind: linux servers) as "name ip" pairs.
# Gets node type from the clab YAML and IPs from Docker inspect.
clab_sonic_nodes() {
    local clab_file="$1"
    python3 -c "
import yaml, subprocess
with open('${clab_file}') as f:
    data = yaml.safe_load(f)
topo_name = data.get('name', '')
for name, node in data.get('topology', {}).get('nodes', {}).items():
    if node.get('kind', '') == 'linux':
        continue
    container = f'clab-{topo_name}-{name}'
    result = subprocess.run(
        ['docker', 'inspect', '--format',
         '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',
         container],
        capture_output=True, text=True)
    ip = result.stdout.strip()
    if ip:
        print(f'{container} {ip}')
" 2>/dev/null || true
}

lab_start() {
    local topo_name="${1:-spine-leaf}"
    local topo_file="${SCRIPT_DIR}/topologies/${topo_name}.yml"

    if [[ ! -f "$topo_file" ]]; then
        echo "ERROR: topology file not found: ${topo_file}" >&2
        echo "Available topologies:" >&2
        ls "${SCRIPT_DIR}/topologies/"*.yml 2>/dev/null | xargs -I{} basename {} .yml >&2
        return 1
    fi

    echo "=== Starting lab: ${topo_name} ==="

    # Step 1: Build tools and generate artifacts
    echo "Building labgen..."
    (cd "${PROJECT_ROOT}" && go build -o "${GENERATED_DIR}/labgen" ./cmd/labgen/)

    mkdir -p "${GENERATED_DIR}"
    echo "Generating lab artifacts..."
    "${GENERATED_DIR}/labgen" \
        -topology "${topo_file}" \
        -output "${GENERATED_DIR}"

    # Step 2: Boot devices with minimal startup config
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"
    echo ""
    echo "Deploying containerlab topology..."
    (cd "${GENERATED_DIR}" && containerlab deploy -t "${clab_file}" --reconfigure)
    echo "${topo_name}" > "${LAB_STATE_FILE}"

    echo ""
    echo "Waiting for SONiC nodes to boot..."
    lab_wait_healthy "${topo_name}"

    echo ""
    echo "Waiting for Redis to be ready..."
    lab_wait_redis "${topo_name}"

    # Step 3: Apply all config via newtron (topology, BGP, MACs — everything)
    echo ""
    echo "Patching profiles with management IPs..."
    lab_patch_profiles "${topo_name}"

    echo ""
    echo "Provisioning devices via newtron..."
    lab_provision "${topo_name}"

    # Step 4: Save config on all nodes (persists to /etc/sonic/config_db.json)
    echo ""
    echo "Saving config on all nodes..."
    lab_config_save "${topo_name}"

    # Step 5: Reboot all nodes to come up fresh with complete saved config
    # This applies system MACs, interface IPs, BGP config cleanly from boot.
    echo ""
    echo "Rebooting all nodes for clean startup..."
    lab_reboot_nodes "${topo_name}"

    echo ""
    echo "Waiting for SONiC nodes to boot..."
    lab_wait_healthy "${topo_name}"

    echo ""
    echo "Waiting for Redis to be ready..."
    lab_wait_redis "${topo_name}"

    # Step 6: Verify
    echo ""
    echo "=== Lab ${topo_name} is ready ==="
    lab_status
}

lab_stop() {
    local topo_name
    if [[ -f "${LAB_STATE_FILE}" ]]; then
        topo_name=$(cat "${LAB_STATE_FILE}")
    else
        echo "No lab state file found. Attempting cleanup..."
        topo_name="spine-leaf"
    fi

    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"

    echo "=== Stopping lab: ${topo_name} ==="
    if [[ -f "${clab_file}" ]]; then
        (cd "${GENERATED_DIR}" && containerlab destroy -t "${clab_file}" --cleanup) || true
    fi

    rm -f "${LAB_STATE_FILE}"
    echo "Lab stopped."
}

lab_status() {
    echo "=== Containerlab Status ==="

    if [[ ! -f "${LAB_STATE_FILE}" ]]; then
        echo "No lab is running (no state file)."
        return 0
    fi

    local topo_name
    topo_name=$(cat "${LAB_STATE_FILE}")
    echo "Topology: ${topo_name}"
    echo ""

    # Get node info from containerlab inspect
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"
    if [[ -f "${clab_file}" ]]; then
        (cd "${GENERATED_DIR}" && containerlab inspect -t "${clab_file}" 2>/dev/null) || echo "(containerlab inspect failed)"
    fi

    echo ""
    echo "Redis connectivity (SONiC nodes only, via SSH):"
    # Check Redis on SONiC nodes via SSH (Redis port is not forwarded)
    local nodes
    nodes=$(clab_sonic_nodes "${clab_file}")

    local creds
    creds=$(clab_ssh_creds "${clab_file}")
    local ssh_user="${creds%% *}"
    local ssh_pass="${creds##* }"

    if [[ -n "$nodes" && -n "$ssh_user" ]]; then
        while IFS=' ' read -r name ip; do
            if [[ -n "$ip" ]]; then
                if sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
                    -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
                    "$ssh_user@$ip" "redis-cli -n 4 PING" < /dev/null 2>/dev/null | grep -q PONG; then
                    echo "  ${name}: ${ip} (SSH→Redis) OK"
                else
                    echo "  ${name}: ${ip} (SSH→Redis) UNREACHABLE"
                fi
            fi
        done <<< "$nodes"
    fi
}

lab_wait_healthy() {
    local topo_name="$1"
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"
    local timeout=300  # 5 minutes
    local start_time
    start_time=$(date +%s)

    echo -n "  Waiting for SONiC containers to be healthy (skipping linux)..."
    while true; do
        local now
        now=$(date +%s)
        local elapsed=$(( now - start_time ))
        if [[ $elapsed -ge $timeout ]]; then
            echo " TIMEOUT"
            echo "WARNING: Not all containers became healthy within ${timeout}s" >&2
            break
        fi

        # Check if any non-linux container is still in "starting" state
        local status
        status=$(cd "${GENERATED_DIR}" && containerlab inspect -t "${clab_file}" --format json 2>/dev/null | \
            python3 -c "
import json, yaml, sys

# Load linux node names from clab YAML
with open('${clab_file}') as f:
    clab = yaml.safe_load(f)
linux_nodes = set()
for name, node in clab.get('topology', {}).get('nodes', {}).items():
    if node.get('kind', '') == 'linux':
        linux_nodes.add(name)

data = json.load(sys.stdin)
starting = 0
for topo, containers in data.items():
    for c in containers:
        # Strip clab-<topo>- prefix to get node name
        cname = c.get('name', '')
        short = cname.split('-', 2)[-1] if '-' in cname else cname
        if short in linux_nodes:
            continue
        if 'starting' in c.get('status', ''):
            starting += 1
print(starting)
" 2>/dev/null) || true

        if [[ "$status" == "0" ]]; then
            echo " READY"
            return 0
        fi
        sleep 10
    done
}

lab_wait_redis() {
    local topo_name="$1"
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"
    local timeout=300  # 5 minutes
    local start_time
    start_time=$(date +%s)

    # Get only SONiC node IPs (skip linux servers which have no Redis)
    local nodes
    nodes=$(clab_sonic_nodes "${clab_file}")

    if [[ -z "$nodes" ]]; then
        echo "WARNING: Could not discover nodes from containerlab inspect" >&2
        return 0
    fi

    # Get SSH credentials from clab YAML
    local creds
    creds=$(clab_ssh_creds "${clab_file}")
    local ssh_user="${creds%% *}"
    local ssh_pass="${creds##* }"

    if [[ -z "$ssh_user" ]]; then
        echo "WARNING: No SSH credentials found in clab YAML" >&2
        return 0
    fi

    while IFS=' ' read -r name ip; do
        if [[ -z "$ip" ]]; then
            continue
        fi
        echo -n "  Waiting for ${name} (${ip}, via SSH)..."
        while true; do
            local now
            now=$(date +%s)
            local elapsed=$(( now - start_time ))
            if [[ $elapsed -ge $timeout ]]; then
                echo " TIMEOUT"
                echo "ERROR: Redis on ${name} did not respond within ${timeout}s" >&2
                return 1
            fi
            if sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
                -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
                "$ssh_user@$ip" "redis-cli -n 4 PING" < /dev/null 2>/dev/null | grep -q PONG; then
                echo " READY"
                break
            fi
            sleep 5
        done
    done <<< "$nodes"
}

lab_config_save() {
    local topo_name="$1"
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"

    local nodes
    nodes=$(clab_sonic_nodes "${clab_file}")
    if [[ -z "$nodes" ]]; then
        return 0
    fi

    local creds
    creds=$(clab_ssh_creds "${clab_file}")
    local ssh_user="${creds%% *}"
    local ssh_pass="${creds##* }"

    if [[ -z "$ssh_user" ]]; then
        return 0
    fi

    while IFS=' ' read -r name ip; do
        if [[ -z "$ip" ]]; then
            continue
        fi
        local node_name
        node_name=$(echo "$name" | sed "s/^clab-${topo_name}-//")
        echo -n "  ${node_name} (${ip})..."
        sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
            "$ssh_user@$ip" "sudo config save -y" < /dev/null 2>/dev/null
        echo " saved"
    done <<< "$nodes"
}

lab_reboot_nodes() {
    local topo_name="$1"
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"

    local nodes
    nodes=$(clab_sonic_nodes "${clab_file}")
    if [[ -z "$nodes" ]]; then
        return 0
    fi

    local creds
    creds=$(clab_ssh_creds "${clab_file}")
    local ssh_user="${creds%% *}"
    local ssh_pass="${creds##* }"

    if [[ -z "$ssh_user" ]]; then
        return 0
    fi

    # Reboot all nodes in parallel (fire-and-forget via nohup)
    while IFS=' ' read -r name ip; do
        if [[ -z "$ip" ]]; then
            continue
        fi
        local node_name
        node_name=$(echo "$name" | sed "s/^clab-${topo_name}-//")
        echo -n "  ${node_name} (${ip})..."
        sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
            "$ssh_user@$ip" "sudo reboot" < /dev/null 2>/dev/null || true
        echo " rebooting"
    done <<< "$nodes"

    # Wait for containers to go unhealthy (reboot in progress)
    echo "  Waiting for reboot to take effect..."
    sleep 15
}

lab_bridge_nics() {
    local topo_name="$1"
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"

    # Get only SONiC node IPs
    local nodes
    nodes=$(clab_sonic_nodes "${clab_file}")

    if [[ -z "$nodes" ]]; then
        return 0
    fi

    # Get SSH credentials
    local creds
    creds=$(clab_ssh_creds "${clab_file}")
    local ssh_user="${creds%% *}"
    local ssh_pass="${creds##* }"

    if [[ -z "$ssh_user" ]]; then
        return 0
    fi

    while IFS=' ' read -r name ip; do
        if [[ -z "$ip" ]]; then
            continue
        fi
        local node_name
        node_name=$(echo "$name" | sed "s/^clab-${topo_name}-//")

        echo -n "  ${node_name} (${ip}):"
        # Bridge each QEMU NIC (ethN) to the corresponding NGDP ASIC switch port
        # (swvethN) using tc mirred redirect. The NGDP ASIC simulator (ngdpd)
        # uses vethN as its data-plane ports; swvethN is the other end of the
        # veth pair. Without this bridge, traffic cannot flow between the
        # external container network and the simulated ASIC.
        local count=0
        if sshpass -p "$ssh_pass" ssh -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
            "$ssh_user@$ip" "
for i in \$(seq 1 64); do
  if ip link show eth\$i >/dev/null 2>&1 && ip link show swveth\$i >/dev/null 2>&1; then
    sudo /usr/sbin/tc qdisc add dev swveth\$i clsact 2>/dev/null || sudo /usr/sbin/tc qdisc replace dev swveth\$i clsact
    sudo /usr/sbin/tc filter add dev swveth\$i ingress flower action mirred egress redirect dev eth\$i 2>/dev/null
    sudo /usr/sbin/tc qdisc add dev eth\$i clsact 2>/dev/null || sudo /usr/sbin/tc qdisc replace dev eth\$i clsact
    sudo /usr/sbin/tc filter add dev eth\$i ingress flower action mirred egress redirect dev swveth\$i 2>/dev/null
    echo \$i
  else
    break
  fi
done
" < /dev/null 2>/dev/null; then
            echo " OK"
        else
            echo " FAILED"
        fi
    done <<< "$nodes"
}

lab_provision() {
    local topo_name="$1"
    echo "  Building newtron..."
    (cd "${PROJECT_ROOT}" && go build -o "${GENERATED_DIR}/newtron" ./cmd/newtron/)

    echo "  Provisioning all devices from topology.json..."
    "${GENERATED_DIR}/newtron" \
        -S "${GENERATED_DIR}/specs" \
        provision -x

    echo "  Waiting for config to converge..."
    sleep 15
}

lab_patch_profiles() {
    local topo_name="$1"
    local clab_file="${GENERATED_DIR}/${topo_name}.clab.yml"
    local profiles_dir="${GENERATED_DIR}/specs/profiles"

    # Get only SONiC node IPs (servers don't have profiles)
    local nodes
    nodes=$(clab_sonic_nodes "${clab_file}")

    if [[ -z "$nodes" ]]; then
        echo "WARNING: Could not discover nodes from containerlab inspect" >&2
        return 0
    fi

    # Build per-node SSH credentials map from the clab YAML
    local node_ssh_creds_json
    node_ssh_creds_json=$(python3 -c "
import yaml, json
with open('${clab_file}') as f:
    data = yaml.safe_load(f)
creds = {}
for name, node in data.get('topology', {}).get('nodes', {}).items():
    if node.get('kind', '') != 'linux':
        env = node.get('env', {})
        user = env.get('USERNAME', '')
        passwd = env.get('PASSWORD', '')
        if user and passwd:
            creds[name] = {'user': user, 'pass': passwd}
print(json.dumps(creds))
" 2>/dev/null)

    while IFS=' ' read -r name ip; do
        if [[ -z "$ip" ]]; then
            continue
        fi
        # containerlab prefixes names with "clab-<topo>-", strip that to get node name
        local node_name
        node_name=$(echo "$name" | sed "s/^clab-${topo_name}-//")
        local profile="${profiles_dir}/${node_name}.json"
        if [[ -f "$profile" ]]; then
            python3 -c "
import json, sys
node_ssh_creds = json.loads('${node_ssh_creds_json}')
with open('${profile}') as f:
    data = json.load(f)
data['mgmt_ip'] = '${ip}'
creds = node_ssh_creds.get('${node_name}', {})
if creds.get('user'):
    data['ssh_user'] = creds['user']
    data['ssh_pass'] = creds['pass']
with open('${profile}', 'w') as f:
    json.dump(data, f, indent=2)
    f.write('\n')
"
            echo "  Patched ${node_name} → ${ip}"
        fi
    done <<< "$nodes"
}

case "${1:-help}" in
    redis-start) redis_start ;;
    redis-stop)  redis_stop ;;
    redis-seed)  redis_seed ;;
    redis-ip)    redis_ip ;;
    status)      status ;;
    lab-start)   lab_start "${2:-spine-leaf}" ;;
    lab-stop)    lab_stop ;;
    lab-status)  lab_status ;;
    *)
        echo "Usage: $0 {redis-start|redis-stop|redis-seed|redis-ip|status|lab-start|lab-stop|lab-status}"
        exit 1
        ;;
esac
