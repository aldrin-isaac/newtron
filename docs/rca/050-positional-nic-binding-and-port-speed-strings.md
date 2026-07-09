# RCA-050: two authoring-time defects that silently kill a CiscoVS fabric — human-form port speeds and sparse NIC indexes

**Severity**: Fabric-dead in both cases; neither produces an error anywhere.
**Component**: `pkg/newtron/spec` (PortConfig), `pkg/newtlab` (NIC allocation, host netns provisioning).
**Discovered**: 2026-07-08, debugging a consumer-authored (newtcon) 3-switch cisco-p200 fabric whose BGP never established and whose hosts never came up. Four distinct faults were found; the two newtron/newtlab-owned ones are this RCA.

## Fault A — the schema advertised speeds SONiC cannot parse

`PortConfig.Speed` declared `enum:"1G,10G,…,400G"` — schema metadata that
consumers (newtcon's form UI) faithfully render — and `Fields()` passed the
authored value **verbatim** into the CONFIG_DB `PORT` table. SONiC requires
numeric Mbps (`"100000"`); orchagent's `parsePortSpeed` hard-fails on `1G`,
and on cisco-p200 **one unparseable port stalls the entire port-init
pipeline**: every port oper-down, RX punt dead, ARP impossible, BGP `Active`
forever.

The kill is delayed and reload-triggered: first boot is healthy (factory
PORT table), the poison arrives with provisioning, and the next `config
reload` — including newtron's own full Reconcile — replays it. The
consumer was **not** at fault: the schema told them `1G` was the vocabulary.

**Fix (§36 normalize-at-the-boundary, §15 shared validator):**
- `portSpeedMbps` — one owner of the human→Mbps translation; `Fields()`
  renders `"100G"` as `"100000"` exactly as it already rendered `mtu 9100`
  as `"9100"`.
- `PortConfig.ValidateConstraints` — enforced at **both** topology load
  (`validateTopology`) and topology write (`UpdateTopologyDevice`): the
  writer rejects what the loader rejects, from one validator. Out-of-enum
  speeds never reach a device again.
- Deliberately out of scope: per-platform supported-speed checking — the
  platform files carry no speed data; a well-formed-but-unsupported speed is
  refused by SAI per-port, gracefully.

## Fault B — nic_index is a POSITION contract, and sparse topologies broke it

QEMU attaches NICs in command-line order, and the SONiC VM dataplane
(Silicon One sim; VPP) binds front-panel ports to data NICs
**positionally** — the Nth data NIC backs the Nth port, regardless of the
netdev id string. The platform `ports[]` `nic_index` is therefore a
position contract: realizing `Ethernet4` (nic_index 5) requires NIC slots
1..5 to exist.

newtlab appended NICs in link order and labeled them `eth<nic_index>` —
a label QEMU ignores for ordering. A sparse topology (links on E0 + E4,
skipping E1–E3) got two data NICs; the sim bound the second to
**Ethernet1** while all config (IPs, BGP) sat on Ethernet4. LLDP was the
tell: every "Ethernet4" adjacency reported the far end as Ethernet1.
Every in-repo topology is accidentally dense and ordered, which is why
this survived until the first consumer-authored sparse fabric.

**Fix:** `normalizeNodeNICs` (post-`AllocateLinks`): per node, sort NICs by
index and pad interior gaps with disconnected filler NICs
(`-netdev user,restrict=on` — the VM-level equivalent of an unwired
front-panel port). The mgmt NIC (index 0) is preserved. Dense topologies
are untouched (pinned by test). Proven live: a sparse E0+E4 cisco fixture
now shows `Ethernet4 ↔ Ethernet4` in LLDP with fillers `eth2..eth4` in the
QEMU args.

## Fault B′ — an unwired host stole the management NIC

`provisionHostNamespaces` looked up `NICBase[hostName]` without an
existence check: a host defined in the topology but **wired to nothing**
got the zero value — `eth0`, the management NIC — and the provisioning
script moved it into the namespace, killing SSH to the host VM and marking
every coalesced host `error`. Fixed: an unwired host parks gracefully
(namespace + loopback only, SSH-reachable, wired later).

## RCA-020 relationship

RCA-020 documented "port count = NIC count, no gaps" for **VPP**. This RCA
generalizes it: positional binding is the rule on every VM platform in this
repository, and with the padding fix, sparse authoring is now *legal* —
the gap constraint moved from "operator must know" to "newtlab realizes it."

## Found by

Live debugging of the newtcon-authored `3node-vs-newtcon` fabric —
consumer-driven authoring exercises exactly the degrees of freedom the
in-repo suites never vary (sparse port choices, UI-form field values,
linkless placeholder hosts). The audit log (`PUT /topology/nodes` bodies)
made the poison traceable to the minute; LLDP made the mis-wiring provable
in one command.
