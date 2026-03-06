# Newtron Troubleshooting Methodology

## Core Principle

Always start from the most primitive level and work upward. Never assume a higher layer is broken until the lower layers are verified.

## Layered Troubleshooting Order

### 1. Physical/Link Layer
- Verify topology links are up: `newtlab status` shows link status
- Check interface oper-status: STATE_DB PORT_TABLE oper_status
- Verify QEMU NIC ordering matches expected topology (RCA-027)

### 2. L2 / MAC / ARP
- Check MAC learning: `redis-cli -n 1 keys 'ASIC_STATE:SAI_OBJECT_TYPE_FDB_ENTRY:*'`
- Verify ARP entries: `ip neigh show` on hosts, `redis-cli -n 0 keys 'NEIGH_TABLE:*'` on switches
- Check VLAN membership: CONFIG_DB VLAN_MEMBER table
- Verify bridge forwarding: `bridge fdb show` for local entries

### 3. L3 / IP Routing / Reachability
- Check routing table: `ip route show`, `vtysh -c 'show ip route'`
- Verify CONFIG_DB INTERFACE table has correct IPs
- Check APP_DB routes: `redis-cli -n 0 keys 'ROUTE_TABLE:*'`
- Check ASIC_DB routes: `redis-cli -n 1 keys 'ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:*'`
- Test basic reachability: `ping` between directly connected interfaces

### 4. Transport / TCP Connectivity
- For BGP: verify TCP port 179 is reachable: `nc -zv <peer-ip> 179` or `telnet <peer-ip> 179`
- For overlay: verify VXLAN UDP 4789 connectivity
- Check that firewalls/ACLs aren't blocking

### 5. Protocol / Application Layer
- BGP: `vtysh -c 'show bgp summary json'`, check neighbor state
- EVPN: `vtysh -c 'show evpn vni json'`, check VNI/VTEP discovery
- FRR config: `vtysh -c 'show running-config'`

## SONiC-Specific Troubleshooting

### CONFIG_DB -> Daemon -> APP_DB -> ASIC_DB Pipeline
Each CONFIG_DB write triggers a daemon processing pipeline. When something doesn't work:
1. Verify CONFIG_DB has the correct entries (redis-cli -n 4)
2. Check if the responsible daemon processed it (orchagent, fpmsyncd, vrfmgrd, intfmgrd, bgpcfgd/frrcfgd)
3. Check APP_DB for the derived entries (redis-cli -n 0)
4. Check ASIC_DB for the SAI objects (redis-cli -n 1)

### FRR/vtysh Dual-Format JSON (RCA-042)
`show bgp summary json` returns TWO different formats intermittently:
- AF-keyed: `{"ipv4Unicast": {"peers": {…}}}` — standard format
- Flat: `{"routerId": "…", "peers": {…}}` — appears during FRR init and intermittently

Always parse both formats. Never assume vtysh JSON output is stable.

### Config Reload vs Service Restart
- `config reload -y` is the safest way to apply a full CONFIG_DB change. It stops all daemons, reloads config_db.json, restarts them.
- Individual service restarts (`systemctl restart bgp`) are faster but only affect one daemon.
- After config reload, allow 45-60s for all daemons to stabilize before verification.

## HTTP API Troubleshooting

### Start from the Server
When newtrun or CLI operations appear to do nothing:
1. **Check server logs** — are HTTP requests arriving at all?
2. **Test endpoints directly** — `curl http://localhost:8080/network/default/...`
3. **Verify device classification** — `curl .../host/{name}` should return 404 for switches (RCA-043)
4. **Check actor serialization** — operations on the same device are serialized through NodeActor

### Common API Issues
- No POST requests in server log -> operations never reach the server (check client construction)
- 200 but wrong behavior -> check if the response body contains the expected data
- 500 errors -> check server log for SSH connection failures or Redis timeouts

## Key Debugging Commands

```bash
# Redis (on switch via SSH)
redis-cli -n 4 keys '*'                    # CONFIG_DB all keys
redis-cli -n 4 hgetall 'BGP_NEIGHBOR|default|10.0.0.2'  # specific entry
redis-cli -n 0 keys 'ROUTE_TABLE:*'        # APP_DB routes
redis-cli -n 1 keys 'ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:*'  # ASIC routes
redis-cli -n 6 keys '*'                    # STATE_DB

# FRR
sudo vtysh -c 'show bgp summary json'
sudo vtysh -c 'show ip route json'
sudo vtysh -c 'show evpn vni json'
sudo vtysh -c 'show running-config'

# SONiC containers
docker ps                                   # container status
docker logs bgp                             # FRR/bgpcfgd logs
docker logs swss                            # orchagent logs

# Host-level
ip netns exec <host> ip addr show           # host IP config (VM coalescing)
ip netns exec <host> ping <target>          # host-level connectivity
bridge fdb show                             # bridge forwarding database
```

## Lessons Learned

1. **Silent skips are the worst bugs.** When operations appear to "pass" too quickly (provision in <1s), that's a red flag — the operation was likely skipped, not fast.

2. **vtysh output is not stable.** FRR can return different JSON formats for the same command within seconds. Always parse defensively.

3. **Start with server logs.** The absence of expected HTTP requests is more informative than the presence of unexpected errors.

4. **Test the simplest thing first.** Before debugging code, verify with `curl` that the server responds correctly to basic requests.

5. **Device classification matters.** If a device is misclassified (switch as host), all subsequent operations silently skip it.
