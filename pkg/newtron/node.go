package newtron

import (
	"context"
	"fmt"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Node wraps a *node.Node with pending change management.
//
// Each ops method delegates to the internal node.Node, captures the returned
// *node.ChangeSet, appends it to n.pending, and returns only an error.
// Commit() applies all pending changesets and verifies them.
// Execute() is the one-shot pattern: lock → fn → commit → save → unlock.
type Node struct {
	net      *Network
	internal *node.Node

	// pending collects ChangeSets produced by Interface write operations.
	// Accumulated via appendPending; applied and cleared by Commit.
	pending []*node.ChangeSet
}

// ============================================================================
// Lifecycle methods
// ============================================================================

// Lock acquires a distributed lock for configuration changes.
func (n *Node) Lock(ctx context.Context) error { return n.internal.Lock(ctx) }

// Unlock releases the distributed lock.
func (n *Node) Unlock() error { return n.internal.Unlock() }

// Save persists the device's running CONFIG_DB to disk.
func (n *Node) Save(ctx context.Context) error {
	if err := n.gate(ctx, auth.PermDeviceWrite, ""); err != nil {
		return err
	}
	return n.internal.SaveConfig(ctx)
}

// Close disconnects from the device.
func (n *Node) Close() error {
	return n.internal.Disconnect()
}

// Ping checks Redis connectivity without touching the projection.
// No-op without transport (topology offline mode).
func (n *Node) Ping(ctx context.Context) error { return n.internal.Ping(ctx) }

// HasActuatedIntent returns true if this node was initialized from device intents.
func (n *Node) HasActuatedIntent() bool { return n.internal.HasActuatedIntent() }

// HasUnsavedIntents returns true if CRUD mutations have been made since the last Save.
func (n *Node) HasUnsavedIntents() bool { return n.internal.HasUnsavedIntents() }

// ClearUnsavedIntents resets the unsaved flag after a successful Save.
func (n *Node) ClearUnsavedIntents() { n.internal.ClearUnsavedIntents() }

// DisconnectTransport closes the SSH+Redis transport without affecting the projection.
func (n *Node) DisconnectTransport() { n.internal.DisconnectTransport() }

// RebuildProjection rebuilds the projection from the current intent DB.
// Called at the start of each operation to ensure the projection is the
// canonical derivation of the intents — not a cumulative approximation.
func (n *Node) RebuildProjection(ctx context.Context) error {
	return n.internal.RebuildProjection(ctx)
}

// Tree reads the intent DB and returns the ordered intent steps that
// reproduce the node's current expected state.
//
// Architecture §6: "Read the intent DB → build intent DAG."
func (n *Node) Tree() *TopologySnapshot {
	dev := n.internal.Tree()
	snap := &TopologySnapshot{}
	for _, s := range dev.Steps {
		snap.Steps = append(snap.Steps, TopologyStep{
			URL:    s.URL,
			Params: s.Params,
		})
	}
	return snap
}

// Projection returns the per-table per-key per-field expected state derived
// from intent replay — the canonical substrate (`sonic.RawConfigDB`) representing
// what newtron believes this device should look like. Compare against
// ConfigDBSnapshot to see drift. §46.
func (n *Node) Projection() sonic.RawConfigDB {
	return n.internal.Projection()
}

// ConfigDBSnapshot returns the device's actual CONFIG_DB state as a single
// internally-consistent snapshot. ownedOnly=true returns only newtron-owned
// tables (matching Drift's scope); ownedOnly=false returns every schema-known
// table on the device. §46.
func (n *Node) ConfigDBSnapshot(ctx context.Context, ownedOnly bool) (sonic.RawConfigDB, error) {
	return n.internal.ConfigDBSnapshot(ctx, ownedOnly)
}

// BindsService reports whether this Node has at least one actuated
// apply-service intent for the named service. Used by Network.ServiceProjection
// (and api handlers iterating over nodes) to skip non-binders cheaply before
// the full replay-diff computation.
func (n *Node) BindsService(serviceName string) bool {
	return n.internal.BindsService(serviceName)
}

// ProjectionDiff applies the given operations on top of the Node's current
// intent DB in-memory, captures the resulting projection, and restores both
// intent DB and projection. Returns the before/after RawConfigDB pair plus
// the entry-level diff. Workbench uses this for pre-commit previews — the
// substrate side of operator-philosophy invariant #4 (show before do). §46.
func (n *Node) ProjectionDiff(ctx context.Context, ops []spec.TopologyStep) (*node.ProjectionDiffResult, error) {
	return n.internal.ProjectionDiff(ctx, ops)
}

// ServiceProjection returns the projection entries this Node carries because
// the named service is bound on it — the canonical entry-level slice via the
// replay-diff technique. Returns []sonic.DriftEntry per §11.
//
// Exposed for callers that need per-Node service slices (typically the api
// layer's handleServiceProjection, which iterates over NodeActors and calls
// this method on each Node that BindsService). Newtcon and other HTTP
// consumers read this substrate via the network-scoped endpoint
// GET /networks/{n}/services/{name}/projection (returns *ServiceProjection),
// not directly per-Node.
func (n *Node) ServiceProjection(ctx context.Context, serviceName string) ([]sonic.DriftEntry, error) {
	return n.internal.ServiceProjection(ctx, serviceName)
}

// Drift compares the node's projection (expected state) against the device's
// actual CONFIG_DB. Returns drift entries for owned tables. Auto-connects
// transport if not already connected.
//
// Architecture §6: "Compare projection vs device → drift entries."
func (n *Node) Drift(ctx context.Context) ([]DriftEntry, error) {
	diffs, err := n.internal.Drift(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DriftEntry, 0, len(diffs))
	for _, d := range diffs {
		result = append(result, DriftEntry{
			Table:    d.Table,
			Key:      d.Key,
			Type:     d.Type,
			Expected: d.Expected,
			Actual:   d.Actual,
		})
	}
	return result, nil
}

// Reconcile delivers the projection to the device, eliminating drift.
// Auto-connects transport if not already connected.
//
// Two modes: "full" (config reload + ReplaceAll) and "delta" (patch only drifted entries).
func (n *Node) Reconcile(ctx context.Context, opts ReconcileOpts) (*ReconcileResult, error) {
	if err := n.gate(ctx, auth.PermDeviceWrite, ""); err != nil {
		return nil, err
	}
	result, err := n.internal.Reconcile(ctx, node.ReconcileOpts{Mode: opts.Mode})
	if err != nil {
		return nil, err
	}
	return &ReconcileResult{
		Mode:     result.Mode,
		Applied:  result.Applied,
		Missing:  result.Missing,
		Extra:    result.Extra,
		Modified: result.Modified,
	}, nil
}

// Interface returns a wrapped Interface for the given interface name.
func (n *Node) Interface(name string) (*Interface, error) {
	intf, err := n.internal.GetInterface(name)
	if err != nil {
		return nil, err
	}
	return &Interface{node: n, internal: intf}, nil
}

// QueryConfigDB reads a CONFIG_DB entry by table and key.
// Returns an empty map (not error) if the entry does not exist.
// Falls through to the projection when no device connection exists (loopback mode).
func (n *Node) QueryConfigDB(table, key string) (map[string]string, error) {
	client := n.internal.ConfigDBClient()
	if client == nil {
		// Loopback: read from projection
		return n.internal.ConfigDB().Get(table, key), nil
	}
	return client.Get(table, key)
}

// ConfigDBTableKeys returns all keys in a CONFIG_DB table.
// Falls through to the projection when no device connection exists (loopback mode).
func (n *Node) ConfigDBTableKeys(table string) ([]string, error) {
	client := n.internal.ConfigDBClient()
	if client == nil {
		return n.internal.ConfigDB().TableKeys(table), nil
	}
	return client.TableKeys(table)
}

// ConfigDBEntryExists returns true if a CONFIG_DB entry exists.
// Falls through to the projection when no device connection exists (loopback mode).
func (n *Node) ConfigDBEntryExists(table, key string) (bool, error) {
	client := n.internal.ConfigDBClient()
	if client == nil {
		return n.internal.ConfigDB().Exists(table, key), nil
	}
	return client.Exists(table, key)
}

// QueryStateDB reads a STATE_DB entry by table and key.
// Returns nil (not error) if the entry does not exist.
func (n *Node) QueryStateDB(table, key string) (map[string]string, error) {
	client := n.internal.StateDBClient()
	if client == nil {
		return nil, fmt.Errorf("no STATE_DB client for device %s", n.internal.Name())
	}
	return client.GetEntry(table, key)
}

// ============================================================================
// Pending change management
// ============================================================================

// appendPending adds a non-nil ChangeSet to the Node's pending list.
// Called by all write methods after each successful operation.
func (n *Node) appendPending(cs *node.ChangeSet) {
	if cs != nil {
		n.pending = append(n.pending, cs)
	}
}

// gate consults the parent Network's permission checker (auth-design.md
// L4) before a Node mutation acts. It populates auth.Context.Device
// from the node's name and Resource from the per-method resource
// string (vlan id, vrf name, etc.) so the audit log decision event
// records what was being acted on. When authorization is disabled
// at the Network level (net.auth == nil), checkPermission returns
// nil and the mutation proceeds.
func (n *Node) gate(ctx context.Context, perm auth.Permission, resource string) error {
	return n.net.checkPermission(ctx, perm, auth.NewContext().
		WithDevice(n.internal.Name()).
		WithResource(resource))
}

// PendingPreview returns a formatted preview of all pending changes.
func (n *Node) PendingPreview() string {
	var sb strings.Builder
	for _, cs := range n.pending {
		sb.WriteString(cs.Preview())
	}
	return sb.String()
}

// PendingCount returns the total number of pending changes.
func (n *Node) PendingCount() int {
	count := 0
	for _, cs := range n.pending {
		count += len(cs.Changes)
	}
	return count
}

// Commit applies all pending changesets, verifies them, and clears the pending list.
func (n *Node) Commit(ctx context.Context) (*WriteResult, error) {
	if len(n.pending) == 0 {
		return &WriteResult{}, nil
	}

	result := &WriteResult{}
	for _, cs := range n.pending {
		result.Preview += cs.Preview()
		result.ChangeCount += len(cs.Changes)
		result.Changes = append(result.Changes, cs.Changes...)
	}

	// Apply all pending changesets. DeviceOps entries accumulated by each
	// cs.Apply / cs.Verify are aggregated onto the public WriteResult so
	// callers see the full per-substrate-op timeline for the whole bundle.
	for _, cs := range n.pending {
		if err := cs.Apply(n.internal); err != nil {
			result.DeviceOps = append(result.DeviceOps, cs.DeviceOps...)
			return result, fmt.Errorf("apply failed: %w", err)
		}
	}
	result.Applied = true

	// Verify all pending changesets
	allPassed := true
	var vr VerificationResult
	for _, cs := range n.pending {
		if err := cs.Verify(n.internal); err != nil {
			result.DeviceOps = append(result.DeviceOps, cs.DeviceOps...)
			return result, fmt.Errorf("verify failed: %w", err)
		}
		if cs.Verification != nil {
			vr.Passed += cs.Verification.Passed
			vr.Failed += cs.Verification.Failed
			for _, e := range cs.Verification.Errors {
				vr.Errors = append(vr.Errors, VerificationError{
					Table:          e.Table,
					Key:            e.Key,
					Field:          e.Field,
					Expected:       e.Expected,
					Actual:         e.Actual,
					DeviceResponse: e.DeviceResponse,
				})
			}
			if cs.Verification.Failed > 0 {
				allPassed = false
			}
		}
		result.DeviceOps = append(result.DeviceOps, cs.DeviceOps...)
	}
	result.Verification = &vr
	if !allPassed {
		n.pending = nil
		return result, &VerificationFailedError{
			Device: n.internal.Name(),
			Passed: vr.Passed,
			Failed: vr.Failed,
			Total:  vr.Passed + vr.Failed,
			Result: result,
		}
	}
	result.Verified = true

	n.pending = nil
	return result, nil
}


// ============================================================================
// Execute (one-shot pattern)
// ============================================================================

// Execute acquires the distributed lock, runs fn, then commits or previews.
// The projection is already fresh when Execute is called — execute() in the
// actor layer rebuilt it from device intents.
//
// If opts.Execute is false (dry-run), snapshots the intent DB before running
// fn, captures the preview, then restores the intent DB. The projection is
// left dirty — execute() rebuilds it at the start of the next operation.
//
// If opts.NoSave is true, skips config save after commit.
func (n *Node) Execute(ctx context.Context, opts ExecOpts, fn func(ctx context.Context) error) (*WriteResult, error) {
	if err := n.Lock(ctx); err != nil {
		return nil, err
	}
	defer n.Unlock()

	// Snapshot intent DB before running fn so we can restore on dry-run or error.
	snapshot := n.internal.SnapshotIntentDB()

	if err := fn(ctx); err != nil {
		// Error: restore intent DB to pre-operation state.
		n.internal.RestoreIntentDB(snapshot)
		n.pending = nil
		return nil, err
	}

	if !opts.Execute {
		// Dry-run: capture preview, typed changes, then restore intent DB.
		result := &WriteResult{
			Preview:     n.PendingPreview(),
			ChangeCount: n.PendingCount(),
		}
		for _, cs := range n.pending {
			result.Changes = append(result.Changes, cs.Changes...)
		}
		n.internal.RestoreIntentDB(snapshot)
		n.pending = nil
		return result, nil
	}

	result, err := n.Commit(ctx)
	if err != nil {
		return result, err
	}

	if !opts.NoSave {
		if err := n.Save(ctx); err != nil {
			return result, fmt.Errorf("config save failed: %w", err)
		}
		result.Saved = true
	}

	return result, nil
}


// ============================================================================
// Device-level write ops — VLAN
// ============================================================================

// CreateVLAN creates a VLAN on the device.
func (n *Node) CreateVLAN(ctx context.Context, id int, config VLANConfig) error {
	if err := n.gate(ctx, auth.PermVLANCreate, fmt.Sprintf("VLAN%d", id)); err != nil {
		return err
	}
	cs, err := n.internal.CreateVLAN(ctx, id, node.VLANConfig{Description: config.Description, L2VNI: config.L2VNI})
	n.appendPending(cs)
	return err
}

// DeleteVLAN deletes a VLAN from the device.
func (n *Node) DeleteVLAN(ctx context.Context, id int) error {
	if err := n.gate(ctx, auth.PermVLANDelete, fmt.Sprintf("VLAN%d", id)); err != nil {
		return err
	}
	cs, err := n.internal.DeleteVLAN(ctx, id)
	n.appendPending(cs)
	return err
}

// ConfigureIRB configures the IRB (Integrated Routing and Bridging) interface for a VLAN.
func (n *Node) ConfigureIRB(ctx context.Context, id int, config IRBConfig) error {
	if err := n.gate(ctx, auth.PermVLANModify, fmt.Sprintf("VLAN%d", id)); err != nil {
		return err
	}
	cs, err := n.internal.ConfigureIRB(ctx, id, node.IRBConfig{
		VRF:        config.VRF,
		IPAddress:  config.IPAddress,
		AnycastMAC: config.AnycastMAC,
	})
	n.appendPending(cs)
	return err
}

// UnconfigureIRB removes the IRB configuration from a VLAN.
func (n *Node) UnconfigureIRB(ctx context.Context, id int) error {
	if err := n.gate(ctx, auth.PermVLANModify, fmt.Sprintf("VLAN%d", id)); err != nil {
		return err
	}
	cs, err := n.internal.UnconfigureIRB(ctx, id)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — VRF
// ============================================================================

// CreateVRF creates a VRF on the device.
func (n *Node) CreateVRF(ctx context.Context, name string, config VRFConfig) error {
	if err := n.gate(ctx, auth.PermVRFCreate, name); err != nil {
		return err
	}
	cs, err := n.internal.CreateVRF(ctx, name, node.VRFConfig{})
	n.appendPending(cs)
	return err
}

// DeleteVRF deletes a VRF from the device.
func (n *Node) DeleteVRF(ctx context.Context, name string) error {
	if err := n.gate(ctx, auth.PermVRFDelete, name); err != nil {
		return err
	}
	cs, err := n.internal.DeleteVRF(ctx, name)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — IPVPN
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition.
// Resolves the IPVPN spec by name from the node's SpecProvider.
func (n *Node) BindIPVPN(ctx context.Context, vrf, ipvpnName string) error {
	if err := n.gate(ctx, auth.PermVRFBind, vrf); err != nil {
		return err
	}
	cs, err := n.internal.BindIPVPN(ctx, vrf, util.NormalizeName(ipvpnName))
	n.appendPending(cs)
	return err
}

// UnbindIPVPN unbinds the IP-VPN from a VRF.
func (n *Node) UnbindIPVPN(ctx context.Context, vrf string) error {
	if err := n.gate(ctx, auth.PermVRFBind, vrf); err != nil {
		return err
	}
	cs, err := n.internal.UnbindIPVPN(ctx, vrf)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — BGP
// ============================================================================

// AddBGPEVPNPeer adds a loopback BGP neighbor (indirect, multi-hop eBGP).
func (n *Node) AddBGPEVPNPeer(ctx context.Context, config BGPNeighborConfig) error {
	if err := n.gate(ctx, auth.PermEVPNPeer, config.NeighborIP); err != nil {
		return err
	}
	cs, err := n.internal.AddBGPEVPNPeer(ctx, config.NeighborIP, config.RemoteAS, config.Description, false)
	n.appendPending(cs)
	return err
}

// UpdateBGPEVPNPeer atomically mutates fields on an existing EVPN
// overlay peer. Per §47 the key (default, neighbor_ip) is immutable;
// to change the neighbor IP, remove and re-add. §15 mirror of
// AddBGPEVPNPeer that closes the EVPN session blip the remove + add
// sequence exposes today (#227).
func (n *Node) UpdateBGPEVPNPeer(ctx context.Context, neighborIP string, config BGPNeighborConfig) error {
	if err := n.gate(ctx, auth.PermEVPNPeer, neighborIP); err != nil {
		return err
	}
	cs, err := n.internal.UpdateBGPEVPNPeer(ctx, neighborIP, config.RemoteAS, config.Description, false)
	n.appendPending(cs)
	return err
}

// RemoveBGPEVPNPeer removes an EVPN BGP peer by IP.
func (n *Node) RemoveBGPEVPNPeer(ctx context.Context, ip string) error {
	if err := n.gate(ctx, auth.PermEVPNPeer, ip); err != nil {
		return err
	}
	cs, err := n.internal.RemoveBGPEVPNPeer(ctx, ip)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (n *Node) AddStaticRoute(ctx context.Context, vrf, prefix, nexthop string, metric int) error {
	if err := n.gate(ctx, auth.PermVRFRoute, vrf); err != nil {
		return err
	}
	cs, err := n.internal.AddStaticRoute(ctx, vrf, prefix, nexthop, metric)
	n.appendPending(cs)
	return err
}

// UpdateStaticRoute atomically mutates fields on an existing static
// route. Per §47 the key (vrf, prefix) is immutable; to change the
// prefix, remove and re-add. §15 mirror of AddStaticRoute that closes
// the forwarding black hole the remove + add sequence exposes today
// (#227).
func (n *Node) UpdateStaticRoute(ctx context.Context, vrf, prefix, nexthop string, metric int) error {
	if err := n.gate(ctx, auth.PermVRFRoute, vrf); err != nil {
		return err
	}
	cs, err := n.internal.UpdateStaticRoute(ctx, vrf, prefix, nexthop, metric)
	n.appendPending(cs)
	return err
}

// RemoveStaticRoute removes a static route from a VRF.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrf, prefix string) error {
	if err := n.gate(ctx, auth.PermVRFRoute, vrf); err != nil {
		return err
	}
	cs, err := n.internal.RemoveStaticRoute(ctx, vrf, prefix)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — EVPN
// ============================================================================

// BindMACVPN maps a VLAN to an L2VNI for EVPN.
func (n *Node) BindMACVPN(ctx context.Context, vlanID int, macvpnName string) error {
	if err := n.gate(ctx, auth.PermEVPNMACVPN, fmt.Sprintf("VLAN%d", vlanID)); err != nil {
		return err
	}
	macvpnName = util.NormalizeName(macvpnName)
	cs, err := n.internal.BindMACVPN(ctx, vlanID, macvpnName)
	n.appendPending(cs)
	return err
}

// UnbindMACVPN removes the MAC-VPN binding for a VLAN.
func (n *Node) UnbindMACVPN(ctx context.Context, vlanID int) error {
	if err := n.gate(ctx, auth.PermEVPNMACVPN, fmt.Sprintf("VLAN%d", vlanID)); err != nil {
		return err
	}
	cs, err := n.internal.UnbindMACVPN(ctx, vlanID)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — ACL
// ============================================================================

// CreateACL creates a new ACL table on the device.
func (n *Node) CreateACL(ctx context.Context, name string, config ACLConfig) error {
	if err := n.gate(ctx, auth.PermACLCreate, name); err != nil {
		return err
	}
	cs, err := n.internal.CreateACL(ctx, name, node.ACLConfig{
		Type:        config.Type,
		Stage:       config.Stage,
		Ports:       config.Ports,
		Description: config.Description,
	})
	n.appendPending(cs)
	return err
}

// DeleteACL deletes an ACL table and its rules from the device.
func (n *Node) DeleteACL(ctx context.Context, name string) error {
	if err := n.gate(ctx, auth.PermACLDelete, name); err != nil {
		return err
	}
	cs, err := n.internal.DeleteACL(ctx, name)
	n.appendPending(cs)
	return err
}

// AddACLRule adds a rule to an ACL table.
func (n *Node) AddACLRule(ctx context.Context, acl, ruleName string, config ACLRuleConfig) error {
	if err := n.gate(ctx, auth.PermACLModify, acl); err != nil {
		return err
	}
	cs, err := n.internal.AddACLRule(ctx, acl, ruleName, node.ACLRuleConfig{
		Priority: config.Priority,
		Action:   config.Action,
		SrcIP:    config.SrcIP,
		DstIP:    config.DstIP,
		Protocol: config.Protocol,
		SrcPort:  config.SrcPort,
		DstPort:  config.DstPort,
	})
	n.appendPending(cs)
	return err
}

// UpdateACLRule atomically mutates fields on an existing ACL rule.
// Per §47 the key (acl_table, rule_name) is immutable; to rename a
// rule, remove and re-add. §15 mirror of AddACLRule that closes the
// packet-leak window the remove + add sequence exposes today (#227).
func (n *Node) UpdateACLRule(ctx context.Context, acl, ruleName string, config ACLRuleConfig) error {
	if err := n.gate(ctx, auth.PermACLModify, acl); err != nil {
		return err
	}
	cs, err := n.internal.UpdateACLRule(ctx, acl, ruleName, node.ACLRuleConfig{
		Priority: config.Priority,
		Action:   config.Action,
		SrcIP:    config.SrcIP,
		DstIP:    config.DstIP,
		Protocol: config.Protocol,
		SrcPort:  config.SrcPort,
		DstPort:  config.DstPort,
	})
	n.appendPending(cs)
	return err
}

// RemoveACLRule removes a rule from an ACL table.
func (n *Node) RemoveACLRule(ctx context.Context, acl, ruleName string) error {
	if err := n.gate(ctx, auth.PermACLModify, acl); err != nil {
		return err
	}
	cs, err := n.internal.DeleteACLRule(ctx, acl, ruleName)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — PortChannel
// ============================================================================

// CreatePortChannel creates a new PortChannel on the device.
func (n *Node) CreatePortChannel(ctx context.Context, name string, config PortChannelConfig) error {
	if err := n.gate(ctx, auth.PermLAGCreate, name); err != nil {
		return err
	}
	cs, err := n.internal.CreatePortChannel(ctx, name, node.PortChannelConfig{
		Members:  config.Members,
		MinLinks: config.MinLinks,
		FastRate: config.FastRate,
		Fallback: config.Fallback,
		MTU:      config.MTU,
	})
	n.appendPending(cs)
	return err
}

// DeletePortChannel deletes a PortChannel from the device.
func (n *Node) DeletePortChannel(ctx context.Context, name string) error {
	if err := n.gate(ctx, auth.PermLAGDelete, name); err != nil {
		return err
	}
	cs, err := n.internal.DeletePortChannel(ctx, name)
	n.appendPending(cs)
	return err
}

// AddPortChannelMember adds a member interface to a PortChannel.
func (n *Node) AddPortChannelMember(ctx context.Context, pc, member string) error {
	if err := n.gate(ctx, auth.PermLAGModify, pc); err != nil {
		return err
	}
	cs, err := n.internal.AddPortChannelMember(ctx, pc, member)
	n.appendPending(cs)
	return err
}

// RemovePortChannelMember removes a member interface from a PortChannel.
func (n *Node) RemovePortChannelMember(ctx context.Context, pc, member string) error {
	if err := n.gate(ctx, auth.PermLAGModify, pc); err != nil {
		return err
	}
	cs, err := n.internal.RemovePortChannelMember(ctx, pc, member)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Baseline
// ============================================================================

// convertRROpts converts a public RouteReflectorOpts to the internal type.
func convertRROpts(opts RouteReflectorOpts) node.RouteReflectorOpts {
	result := node.RouteReflectorOpts{
		ClusterID: opts.ClusterID,
		LocalASN:  opts.LocalASN,
		RouterID:  opts.RouterID,
		LocalAddr: opts.LocalAddr,
	}
	for _, c := range opts.Clients {
		result.Clients = append(result.Clients, node.RouteReflectorPeer{IP: c.IP, ASN: c.ASN})
	}
	for _, p := range opts.Peers {
		result.Peers = append(result.Peers, node.RouteReflectorPeer{IP: p.IP, ASN: p.ASN})
	}
	return result
}

// SetupDevice performs consolidated device initialization: metadata, loopback,
// BGP, VTEP (optional), and route reflector (optional). Writes a single
// NEWTRON_INTENT record for the entire setup.
func (n *Node) SetupDevice(ctx context.Context, opts SetupDeviceOpts) error {
	if err := n.gate(ctx, auth.PermDeviceWrite, ""); err != nil {
		return err
	}
	internalOpts := node.SetupDeviceOpts{
		Fields:   opts.Fields,
		SourceIP: opts.SourceIP,
	}
	if opts.RR != nil {
		rr := convertRROpts(*opts.RR)
		internalOpts.RR = &rr
	}
	cs, err := n.internal.SetupDevice(ctx, internalOpts)
	n.appendPending(cs)
	return err
}

// SetDeviceMetadata writes fields to DEVICE_METADATA|localhost.
func (n *Node) SetDeviceMetadata(ctx context.Context, fields map[string]string) error {
	cs, err := n.internal.SetDeviceMetadata(ctx, fields)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level read ops (no changeset, delegation only)
// ============================================================================

// DeviceInfo returns structured device info from the internal node's profile.
func (n *Node) DeviceInfo() (*DeviceInfo, error) {
	p := n.internal.Profile()
	return &DeviceInfo{
		Name:             n.internal.Name(),
		MgmtIP:           p.MgmtIP,
		LoopbackIP:       p.LoopbackIP,
		Platform:         p.Platform,
		Zone:             p.Zone,
		BGPAS:            n.internal.ASNumber(),
		RouterID:         n.internal.RouterID(),
		VTEPSourceIP:     n.internal.VTEPSourceIP(),
		BGPNeighbors:     n.internal.BGPNeighbors(),
		InterfaceCount:   len(n.internal.ListInterfaces()),
		PortChannelCount: len(n.internal.ListPortChannels()),
		VLANCount:        len(n.internal.ListVLANs()),
		VRFCount:         len(n.internal.ListVRFs()),
	}, nil
}

// CheckBGPSessions checks that all configured BGP neighbors are Established.
func (n *Node) CheckBGPSessions(ctx context.Context) ([]HealthCheckResult, error) {
	results, err := n.internal.CheckBGPSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]HealthCheckResult, len(results))
	for i, r := range results {
		out[i] = HealthCheckResult{Check: r.Check, Status: r.Status, Message: r.Message}
	}
	return out, nil
}

// GetRoute reads a route from APP_DB for the given VRF and prefix.
func (n *Node) GetRoute(ctx context.Context, vrf, prefix string) (*RouteEntry, error) {
	re, err := n.internal.GetRoute(ctx, vrf, prefix)
	if err != nil {
		return nil, err
	}
	return convertRouteEntry(re), nil
}

// GetRouteASIC reads a route from ASIC_DB for the given VRF and prefix.
func (n *Node) GetRouteASIC(ctx context.Context, vrf, prefix string) (*RouteEntry, error) {
	re, err := n.internal.GetRouteASIC(ctx, vrf, prefix)
	if err != nil {
		return nil, err
	}
	return convertRouteEntry(re), nil
}

// convertRouteEntry converts a *sonic.RouteEntry to a *RouteEntry.
func convertRouteEntry(re *sonic.RouteEntry) *RouteEntry {
	if re == nil {
		return nil
	}
	entry := &RouteEntry{
		Prefix:   re.Prefix,
		VRF:      re.VRF,
		Protocol: re.Protocol,
		Source:   string(re.Source),
	}
	for _, nh := range re.NextHops {
		entry.NextHops = append(entry.NextHops, RouteNextHop{
			Address:   nh.IP,
			Interface: nh.Interface,
		})
	}
	return entry
}

// ============================================================================
// SSH / device management
// ============================================================================

// ExecCommand executes a command on the device via SSH.
// Returns an error if no SSH tunnel is configured.
func (n *Node) ExecCommand(ctx context.Context, cmd string) (string, error) {
	if err := n.gate(ctx, auth.PermDeviceWrite, ""); err != nil {
		return "", err
	}
	tunnel := n.internal.Tunnel()
	if tunnel == nil {
		return "", fmt.Errorf("no SSH tunnel configured for device %s", n.internal.Name())
	}
	return tunnel.ExecCommand(cmd)
}

// ConfigReload runs 'config reload -y' on the device via SSH.
func (n *Node) ConfigReload(ctx context.Context) error {
	if err := n.gate(ctx, auth.PermDeviceWrite, ""); err != nil {
		return err
	}
	return n.internal.ConfigReload(ctx)
}

// RestartService restarts a SONiC Docker container by name via SSH.
func (n *Node) RestartService(ctx context.Context, name string) error {
	if err := n.gate(ctx, auth.PermDeviceWrite, name); err != nil {
		return err
	}
	return n.internal.RestartService(ctx, name)
}

// ============================================================================
// HealthCheck
// ============================================================================

// HealthCheck runs health checks on this device using the unified pipeline.
// Config check: compares the node's projection against actual CONFIG_DB (Drift).
// Oper checks: BGP session state and wired interface oper-up.
// Auto-connects transport if not already connected.
func (n *Node) HealthCheck(ctx context.Context) (*HealthReport, error) {
	// Config check: projection vs actual CONFIG_DB
	driftEntries, err := n.internal.Drift(ctx)
	if err != nil {
		return nil, fmt.Errorf("config drift check: %w", err)
	}

	// BGP operational check
	bgpResults, err := n.internal.CheckBGPSessions(ctx)
	if err != nil {
		bgpResults = []node.HealthCheckResult{{
			Check: "bgp", Status: "fail",
			Message: fmt.Sprintf("BGP check error: %s", err),
		}}
	}

	// Interface oper-up check for wired interfaces from the projection
	wiredInterfaces := n.internal.WiredInterfaces()
	var intfResults []node.HealthCheckResult
	if len(wiredInterfaces) > 0 {
		intfResults = n.internal.CheckInterfaceOper(wiredInterfaces)
	}

	// Build report
	report := &HealthReport{
		Device: n.internal.Name(),
		Status: "pass",
	}

	// Config check: any drift = fail
	configCheck := &ConfigDriftResult{
		DriftCount: len(driftEntries),
	}
	for _, d := range driftEntries {
		configCheck.Entries = append(configCheck.Entries, DriftEntry{
			Table:    d.Table,
			Key:      d.Key,
			Type:     string(d.Type),
			Expected: d.Expected,
			Actual:   d.Actual,
		})
	}
	report.ConfigCheck = configCheck
	if len(driftEntries) > 0 {
		report.Status = "fail"
	}

	// Oper checks
	var operChecks []HealthCheckResult
	for _, r := range bgpResults {
		operChecks = append(operChecks, HealthCheckResult{Check: r.Check, Status: r.Status, Message: r.Message})
	}
	for _, r := range intfResults {
		operChecks = append(operChecks, HealthCheckResult{Check: r.Check, Status: r.Status, Message: r.Message})
	}
	report.OperChecks = operChecks

	for _, oc := range operChecks {
		if oc.Status == "fail" {
			report.Status = "fail"
			break
		}
		if oc.Status == "warn" && report.Status == "pass" {
			report.Status = "warn"
		}
	}

	return report, nil
}

// ============================================================================
// Status views (read methods)
// ============================================================================

// BGPStatus returns comprehensive BGP status: config + operational state.
func (n *Node) BGPStatus() (*BGPStatusResult, error) {
	resolved := n.internal.Resolved()
	configDB := n.internal.ConfigDB()

	result := &BGPStatusResult{
		LocalAS:    resolved.UnderlayASN,
		RouterID:   resolved.RouterID,
		LoopbackIP: resolved.LoopbackIP,
		EVPNPeers:  resolved.BGPNeighbors,
	}

	if configDB == nil {
		return result, nil
	}

	stateClient := n.internal.StateDBClient()
	for key, neighbor := range configDB.BGPNeighbor {
		parts := strings.SplitN(key, "|", 2)
		var vrf, addr string
		if len(parts) == 2 {
			vrf = parts[0]
			addr = parts[1]
		} else {
			addr = key
		}

		nType := "indirect"
		if neighbor.LocalAddr != "" && neighbor.LocalAddr != resolved.LoopbackIP {
			nType = "direct"
		}

		adminStatus := neighbor.AdminStatus
		if adminStatus == "" {
			adminStatus = "up"
		}

		ns := BGPNeighborStatus{
			Address:   addr,
			VRF:       vrf,
			Type:      nType,
			RemoteAS:  neighbor.ASN,
			LocalAddr: neighbor.LocalAddr,
			Admin:     adminStatus,
			Name:      neighbor.Name,
		}

		// Get operational state from STATE_DB
		if stateClient != nil && vrf != "" {
			entry, err := stateClient.GetBGPNeighborState(vrf, addr)
			if err == nil {
				ns.State = entry.State
				ns.PfxRcvd = entry.PfxRcvd
				ns.PfxSent = entry.PfxSent
				ns.Uptime = entry.Uptime
				if ns.RemoteAS == "" {
					ns.RemoteAS = entry.RemoteAS
				}
			}
		}

		result.Neighbors = append(result.Neighbors, ns)
	}
	return result, nil
}

// EVPNStatus returns comprehensive EVPN status: config + operational state.
func (n *Node) EVPNStatus() (*EVPNStatusResult, error) {
	configDB := n.internal.ConfigDB()

	result := &EVPNStatusResult{
		VTEPs: make(map[string]string),
		NVOs:  make(map[string]string),
	}

	if configDB != nil {
		for name, vtep := range configDB.VXLANTunnel {
			result.VTEPs[name] = vtep.SrcIP
		}
		for name, nvo := range configDB.VXLANEVPNNVO {
			result.NVOs[name] = nvo.SourceVTEP
		}
		result.VNICount = len(configDB.VXLANTunnelMap)

		// VNI mappings
		for _, mapping := range configDB.VXLANTunnelMap {
			resType := "L2"
			res := mapping.VLAN
			if mapping.VRF != "" {
				resType = "L3"
				res = mapping.VRF
			}
			result.VNIMappings = append(result.VNIMappings, VNIMapping{
				VNI:      mapping.VNI,
				Type:     resType,
				Resource: res,
			})
		}

		// VRFs with L3VNI
		for _, vrfName := range n.internal.ListVRFs() {
			vrf, err := n.internal.GetVRF(vrfName)
			if err != nil || vrf.L3VNI <= 0 {
				continue
			}
			result.L3VNIVRFs = append(result.L3VNIVRFs, L3VNIEntry{
				VRF:   vrfName,
				L3VNI: vrf.L3VNI,
			})
		}
	}

	// Operational state from STATE_DB
	stateDB := n.internal.StateDB()
	if stateDB != nil {
		for name, tunnelState := range stateDB.VXLANTunnelTable {
			if configDB != nil {
				if _, isLocal := configDB.VXLANTunnel[name]; isLocal {
					result.VTEPStatus = tunnelState.OperStatus
					continue
				}
			}
			result.RemoteVTEPs = append(result.RemoteVTEPs, name)
		}
	}

	return result, nil
}

// VLANStatus returns all VLANs with summary details.
func (n *Node) VLANStatus() ([]VLANStatusEntry, error) {
	var result []VLANStatusEntry
	for _, id := range n.internal.ListVLANs() {
		vlan, err := n.internal.GetVLAN(id)
		if err != nil {
			continue
		}
		entry := VLANStatusEntry{
			ID:          vlan.ID,
			Name:        vlan.Name,
			L2VNI:       vlan.L2VNI(),
			SVI:         vlan.IRBStatus,
			MemberCount: len(vlan.Members),
			MemberNames: vlan.Members,
		}
		if vlan.MACVPNInfo != nil {
			entry.MACVPN = vlan.MACVPNInfo.Name
			entry.MACVPNInfo = &VLANMACVPNDetail{
				Name:           vlan.MACVPNInfo.Name,
				L2VNI:          vlan.MACVPNInfo.L2VNI,
				ARPSuppression: vlan.MACVPNInfo.ARPSuppression,
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

// ShowVLAN returns VLAN info for a given VLAN ID.
func (n *Node) ShowVLAN(id int) (*VLANStatusEntry, error) {
	vlan, err := n.internal.GetVLAN(id)
	if err != nil {
		return nil, err
	}
	entry := &VLANStatusEntry{
		ID:          vlan.ID,
		Name:        vlan.Name,
		L2VNI:       vlan.L2VNI(),
		SVI:         vlan.IRBStatus,
		MemberCount: len(vlan.Members),
		MemberNames: vlan.Members,
	}
	if vlan.MACVPNInfo != nil {
		entry.MACVPN = vlan.MACVPNInfo.Name
		entry.MACVPNInfo = &VLANMACVPNDetail{
			Name:           vlan.MACVPNInfo.Name,
			L2VNI:          vlan.MACVPNInfo.L2VNI,
			ARPSuppression: vlan.MACVPNInfo.ARPSuppression,
		}
	}
	return entry, nil
}

// VRFStatus returns all VRFs with operational state from STATE_DB.
func (n *Node) VRFStatus() ([]VRFStatusEntry, error) {
	var result []VRFStatusEntry
	for _, name := range n.internal.ListVRFs() {
		vrf, err := n.internal.GetVRF(name)
		if err != nil {
			continue
		}
		entry := VRFStatusEntry{
			Name:       name,
			L3VNI:      vrf.L3VNI,
			Interfaces: len(vrf.Interfaces),
		}
		stateClient := n.internal.StateDBClient()
		if stateClient != nil {
			stateEntry, err := stateClient.GetEntry("VRF_TABLE", name)
			if err == nil && stateEntry != nil {
				entry.State = stateEntry["state"]
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

// ShowVRF returns VRF info including BGP neighbors from CONFIG_DB.
func (n *Node) ShowVRF(name string) (*VRFDetail, error) {
	vrf, err := n.internal.GetVRF(name)
	if err != nil {
		return nil, err
	}
	detail := &VRFDetail{
		Name:       vrf.Name,
		L3VNI:      vrf.L3VNI,
		Interfaces: vrf.Interfaces,
	}

	// Extract BGP neighbors for this VRF from CONFIG_DB
	configDB := n.internal.ConfigDB()
	if configDB != nil {
		vrfPrefix := name + "|"
		for key, neighbor := range configDB.BGPNeighbor {
			if !strings.HasPrefix(key, vrfPrefix) {
				continue
			}
			parts := strings.SplitN(key, "|", 2)
			if len(parts) != 2 {
				continue
			}
			detail.BGPNeighbors = append(detail.BGPNeighbors, BGPNeighborEntry{
				Address:     parts[1],
				ASN:         neighbor.ASN,
				Description: neighbor.Name,
			})
		}
	}
	return detail, nil
}

// LAGStatus returns all PortChannels with operational state.
func (n *Node) LAGStatus() ([]LAGStatusEntry, error) {
	var result []LAGStatusEntry
	for _, pcName := range n.internal.ListPortChannels() {
		pc, err := n.internal.GetPortChannel(pcName)
		if err != nil {
			continue
		}
		entry := LAGStatusEntry{
			Name:          pc.Name,
			AdminStatus:   pc.AdminStatus,
			Members:       pc.Members,
			ActiveMembers: pc.ActiveMembers,
		}
		if intf, err := n.internal.GetInterface(pc.Name); err == nil {
			entry.OperStatus = intf.OperStatus()
			entry.MTU = intf.MTU()
		}
		result = append(result, entry)
	}
	return result, nil
}

// ShowLAGDetail returns LAG info including interface MTU.
func (n *Node) ShowLAGDetail(name string) (*LAGStatusEntry, error) {
	pc, err := n.internal.GetPortChannel(name)
	if err != nil {
		return nil, err
	}
	entry := &LAGStatusEntry{
		Name:          pc.Name,
		AdminStatus:   pc.AdminStatus,
		Members:       pc.Members,
		ActiveMembers: pc.ActiveMembers,
	}
	if intf, err := n.internal.GetInterface(pc.Name); err == nil {
		entry.OperStatus = intf.OperStatus()
		entry.MTU = intf.MTU()
	}
	return entry, nil
}

// ListACLs returns all ACL tables with summary info.
func (n *Node) ListACLs() ([]ACLTableSummary, error) {
	configDB := n.internal.ConfigDB()
	if configDB == nil {
		return nil, nil
	}
	// Count rules per ACL table
	ruleCounts := make(map[string]int, len(configDB.ACLTable))
	for ruleKey := range configDB.ACLRule {
		if i := strings.IndexByte(ruleKey, '|'); i >= 0 {
			ruleCounts[ruleKey[:i]]++
		}
	}
	var result []ACLTableSummary
	for name, table := range configDB.ACLTable {
		result = append(result, ACLTableSummary{
			Name:       name,
			Type:       table.Type,
			Stage:      table.Stage,
			Interfaces: table.Ports,
			RuleCount:  ruleCounts[name],
		})
	}
	return result, nil
}

// ShowACL returns an ACL table with all its rules.
func (n *Node) ShowACL(name string) (*ACLTableDetail, error) {
	configDB := n.internal.ConfigDB()
	if configDB == nil {
		return nil, fmt.Errorf("not connected to device config_db")
	}
	table, ok := configDB.ACLTable[name]
	if !ok {
		return nil, &NotFoundError{Resource: "ACL table", Name: name}
	}
	detail := &ACLTableDetail{
		Name:        name,
		Type:        table.Type,
		Stage:       table.Stage,
		Interfaces:  table.Ports,
		Description: table.PolicyDesc,
	}
	prefix := name + "|"
	for ruleKey, rule := range configDB.ACLRule {
		if !strings.HasPrefix(ruleKey, prefix) {
			continue
		}
		detail.Rules = append(detail.Rules, ACLRuleInfo{
			Name:     strings.TrimPrefix(ruleKey, prefix),
			Priority: rule.Priority,
			Action:   rule.PacketAction,
			SrcIP:    rule.SrcIP,
			DstIP:    rule.DstIP,
			Protocol: rule.IPProtocol,
			SrcPort:  rule.L4SrcPort,
			DstPort:  rule.L4DstPort,
		})
	}
	return detail, nil
}

// GetServiceBindingDetail returns the full service binding: name, IPs, VRF.
func (n *Node) GetServiceBindingDetail(iface string) (*ServiceBindingDetail, error) {
	intf, err := n.internal.GetInterface(iface)
	if err != nil {
		return nil, err
	}
	return &ServiceBindingDetail{
		Service:     intf.ServiceName(),
		IPAddresses: intf.IPAddresses(),
		VRF:         intf.VRF(),
	}, nil
}

// ListInterfaceDetails returns summary info for all interfaces on the device.
func (n *Node) ListInterfaceDetails() ([]InterfaceSummary, error) {
	var result []InterfaceSummary
	for _, name := range n.internal.ListInterfaces() {
		intf, err := n.internal.GetInterface(name)
		if err != nil {
			continue
		}
		result = append(result, InterfaceSummary{
			Name:        name,
			AdminStatus: intf.AdminStatus(),
			OperStatus:  intf.OperStatus(),
			IPAddresses: intf.IPAddresses(),
			VRF:         intf.VRF(),
			Service:     intf.ServiceName(),
		})
	}
	return result, nil
}

// ShowInterfaceDetail returns all properties of a single interface.
func (n *Node) ShowInterfaceDetail(name string) (*InterfaceDetail, error) {
	intf, err := n.internal.GetInterface(name)
	if err != nil {
		return nil, err
	}
	return &InterfaceDetail{
		Name:        name,
		AdminStatus: intf.AdminStatus(),
		OperStatus:  intf.OperStatus(),
		Speed:       intf.Speed(),
		MTU:         intf.MTU(),
		IPAddresses: intf.IPAddresses(),
		VRF:         intf.VRF(),
		Service:     intf.ServiceName(),
		PCMember:    intf.IsPortChannelMember(),
		PCParent:    intf.PortChannelParent(),
		IngressACL:  intf.IngressACL(),
		EgressACL:   intf.EgressACL(),
		PCMembers:   intf.PortChannelMembers(),
		VLANMembers: intf.VLANMembers(),
	}, nil
}

