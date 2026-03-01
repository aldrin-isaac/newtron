package newtron

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/network"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
)

// Node wraps a *node.Node with pending change management.
//
// Each ops method delegates to the internal node.Node, captures the returned
// *node.ChangeSet, appends it to n.pending, and returns only an error.
// Commit() applies all pending changesets, verifies, and moves them to history.
// Execute() is the one-shot pattern: lock → fn → commit → save → unlock.
type Node struct {
	net      *Network
	internal *node.Node
	abstract bool // true when this is an abstract (offline) node

	// pending collects ChangeSets produced by Interface write operations.
	// Accumulated via appendPending; applied and cleared by Commit.
	pending []*node.ChangeSet

	// history holds applied (committed) ChangeSets for VerifyCommitted.
	history []*node.ChangeSet
}

// ============================================================================
// Lifecycle methods
// ============================================================================

// Name returns the device name.
func (n *Node) Name() string { return n.internal.Name() }

// IsAbstract returns true if this is an abstract (offline) node.
func (n *Node) IsAbstract() bool { return n.abstract }

// Lock acquires a distributed lock for configuration changes.
func (n *Node) Lock() error { return n.internal.Lock() }

// Unlock releases the distributed lock.
func (n *Node) Unlock() error { return n.internal.Unlock() }

// Save persists the device's running CONFIG_DB to disk.
func (n *Node) Save(ctx context.Context) error { return n.internal.SaveConfig(ctx) }

// Close disconnects from the device. No-op for abstract nodes.
func (n *Node) Close() error {
	if n.abstract {
		return nil
	}
	return n.internal.Disconnect()
}

// Refresh reloads CONFIG_DB from Redis and rebuilds the interface list.
// The ctx parameter is accepted for API consistency but not forwarded
// (node.Node.Refresh does not take a context).
func (n *Node) Refresh(ctx context.Context) error { return n.internal.Refresh() }

// RefreshWithRetry polls Refresh until CONFIG_DB is available or timeout expires.
func (n *Node) RefreshWithRetry(ctx context.Context, timeout time.Duration) error {
	return n.internal.RefreshWithRetry(ctx, timeout)
}

// Interface returns a wrapped Interface for the given interface name.
func (n *Node) Interface(name string) (*Interface, error) {
	intf, err := n.internal.GetInterface(name)
	if err != nil {
		return nil, err
	}
	return &Interface{node: n, internal: intf}, nil
}

// ListInterfaces returns all interface names on the device.
func (n *Node) ListInterfaces() []string { return n.internal.ListInterfaces() }

// InterfaceExists checks if an interface exists on the device.
func (n *Node) InterfaceExists(name string) bool { return n.internal.InterfaceExists(name) }

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

// Commit applies all pending changesets, verifies them, and moves to history.
//
// In abstract mode the shadow ConfigDB was already updated during ops, so
// Commit just records the pending list to history without re-applying.
func (n *Node) Commit(ctx context.Context) (*WriteResult, error) {
	if n.abstract {
		// Abstract mode: shadow already updated during ops
		result := &WriteResult{}
		for _, cs := range n.pending {
			result.Preview += cs.Preview()
			result.ChangeCount += len(cs.Changes)
		}
		result.Applied = true
		result.Verified = true
		n.history = append(n.history, n.pending...)
		n.pending = nil
		return result, nil
	}

	if len(n.pending) == 0 {
		return &WriteResult{}, nil
	}

	result := &WriteResult{}
	for _, cs := range n.pending {
		result.Preview += cs.Preview()
		result.ChangeCount += len(cs.Changes)
	}

	// Apply all pending changesets
	for _, cs := range n.pending {
		if err := cs.Apply(n.internal); err != nil {
			return result, fmt.Errorf("apply failed: %w", err)
		}
	}
	result.Applied = true

	// Verify all pending changesets
	allPassed := true
	var vr VerificationResult
	for _, cs := range n.pending {
		if err := cs.Verify(n.internal); err != nil {
			return result, fmt.Errorf("verify failed: %w", err)
		}
		if cs.Verification != nil {
			vr.Passed += cs.Verification.Passed
			vr.Failed += cs.Verification.Failed
			for _, e := range cs.Verification.Errors {
				vr.Errors = append(vr.Errors, VerificationError{
					Table:    e.Table,
					Key:      e.Key,
					Field:    e.Field,
					Expected: e.Expected,
					Actual:   e.Actual,
				})
			}
			if cs.Verification.Failed > 0 {
				allPassed = false
			}
		}
	}
	result.Verification = &vr
	if !allPassed {
		// Move to history even on partial failure so VerifyCommitted can recheck
		n.history = append(n.history, n.pending...)
		n.pending = nil
		return result, &VerificationFailedError{
			Device: n.internal.Name(),
			Passed: vr.Passed,
			Failed: vr.Failed,
		}
	}
	result.Verified = true

	n.history = append(n.history, n.pending...)
	n.pending = nil
	return result, nil
}

// Rollback discards all pending changes without applying them.
func (n *Node) Rollback() {
	n.pending = nil
}

// ============================================================================
// Execute (one-shot pattern)
// ============================================================================

// Execute is the one-shot pattern: lock → fn → commit → save → unlock.
//
// If opts.Execute is false (dry-run), returns a preview without applying.
// If opts.NoSave is true, skips config save after commit.
func (n *Node) Execute(ctx context.Context, opts ExecOpts, fn func(ctx context.Context) error) (*WriteResult, error) {
	if err := n.Lock(); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer n.Unlock()

	if err := fn(ctx); err != nil {
		return nil, err
	}

	if !opts.Execute {
		// Dry-run: return preview only
		result := &WriteResult{
			Preview:     n.PendingPreview(),
			ChangeCount: n.PendingCount(),
		}
		n.Rollback()
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

// VerifyCommitted re-verifies all committed changesets against live CONFIG_DB.
func (n *Node) VerifyCommitted(ctx context.Context) (*VerificationResult, error) {
	var vr VerificationResult
	for _, cs := range n.history {
		if err := cs.Verify(n.internal); err != nil {
			return nil, fmt.Errorf("verify failed: %w", err)
		}
		if cs.Verification != nil {
			vr.Passed += cs.Verification.Passed
			vr.Failed += cs.Verification.Failed
			for _, e := range cs.Verification.Errors {
				vr.Errors = append(vr.Errors, VerificationError{
					Table:    e.Table,
					Key:      e.Key,
					Field:    e.Field,
					Expected: e.Expected,
					Actual:   e.Actual,
				})
			}
		}
	}
	return &vr, nil
}

// ============================================================================
// Device-level write ops — VLAN
// ============================================================================

// CreateVLAN creates a VLAN on the device.
func (n *Node) CreateVLAN(ctx context.Context, id int, config VLANConfig) error {
	cs, err := n.internal.CreateVLAN(ctx, id, node.VLANConfig{Description: config.Description})
	n.appendPending(cs)
	return err
}

// DeleteVLAN deletes a VLAN from the device.
func (n *Node) DeleteVLAN(ctx context.Context, id int) error {
	cs, err := n.internal.DeleteVLAN(ctx, id)
	n.appendPending(cs)
	return err
}

// AddVLANMember adds an interface to a VLAN.
func (n *Node) AddVLANMember(ctx context.Context, id int, iface string, tagged bool) error {
	cs, err := n.internal.AddVLANMember(ctx, id, iface, tagged)
	n.appendPending(cs)
	return err
}

// RemoveVLANMember removes an interface from a VLAN.
func (n *Node) RemoveVLANMember(ctx context.Context, id int, iface string) error {
	cs, err := n.internal.RemoveVLANMember(ctx, id, iface)
	n.appendPending(cs)
	return err
}

// ConfigureSVI configures the SVI (Layer 3 VLAN interface) for a VLAN.
func (n *Node) ConfigureSVI(ctx context.Context, id int, config SVIConfig) error {
	cs, err := n.internal.ConfigureSVI(ctx, id, node.SVIConfig{
		VRF:        config.VRF,
		IPAddress:  config.IPAddress,
		AnycastMAC: config.AnycastMAC,
	})
	n.appendPending(cs)
	return err
}

// RemoveSVI removes the SVI configuration from a VLAN.
func (n *Node) RemoveSVI(ctx context.Context, id int) error {
	cs, err := n.internal.RemoveSVI(ctx, id)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — VRF
// ============================================================================

// CreateVRF creates a VRF on the device.
func (n *Node) CreateVRF(ctx context.Context, name string, config VRFConfig) error {
	cs, err := n.internal.CreateVRF(ctx, name, node.VRFConfig{})
	n.appendPending(cs)
	return err
}

// DeleteVRF deletes a VRF from the device.
func (n *Node) DeleteVRF(ctx context.Context, name string) error {
	cs, err := n.internal.DeleteVRF(ctx, name)
	n.appendPending(cs)
	return err
}

// AddVRFInterface adds an interface to a VRF.
func (n *Node) AddVRFInterface(ctx context.Context, vrf, iface string) error {
	cs, err := n.internal.AddVRFInterface(ctx, vrf, iface)
	n.appendPending(cs)
	return err
}

// RemoveVRFInterface removes an interface from a VRF.
func (n *Node) RemoveVRFInterface(ctx context.Context, vrf, iface string) error {
	cs, err := n.internal.RemoveVRFInterface(ctx, vrf, iface)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — IPVPN
// ============================================================================

// BindIPVPN binds a VRF to an IP-VPN definition.
// Resolves the IPVPN spec by name from the node's SpecProvider.
func (n *Node) BindIPVPN(ctx context.Context, vrf, ipvpnName string) error {
	ipvpnDef, err := n.internal.GetIPVPN(ipvpnName)
	if err != nil {
		return fmt.Errorf("ipvpn '%s' not found: %w", ipvpnName, err)
	}
	cs, err := n.internal.BindIPVPN(ctx, vrf, ipvpnDef)
	n.appendPending(cs)
	return err
}

// UnbindIPVPN unbinds the IP-VPN from a VRF.
func (n *Node) UnbindIPVPN(ctx context.Context, vrf string) error {
	cs, err := n.internal.UnbindIPVPN(ctx, vrf)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — BGP
// ============================================================================

// ConfigureBGP configures BGP globals on the device using its profile.
func (n *Node) ConfigureBGP(ctx context.Context) error {
	cs, err := n.internal.ConfigureBGP(ctx)
	n.appendPending(cs)
	return err
}

// RemoveBGPGlobals removes BGP globals from the device.
func (n *Node) RemoveBGPGlobals(ctx context.Context) error {
	cs, err := n.internal.RemoveBGPGlobals(ctx)
	n.appendPending(cs)
	return err
}

// AddBGPNeighbor adds a loopback BGP neighbor (indirect, EVPN overlay).
func (n *Node) AddBGPNeighbor(ctx context.Context, config BGPNeighborConfig) error {
	cs, err := n.internal.AddLoopbackBGPNeighbor(ctx, config.NeighborIP, config.RemoteAS, config.Description, false)
	n.appendPending(cs)
	return err
}

// RemoveBGPNeighbor removes a BGP neighbor by IP.
func (n *Node) RemoveBGPNeighbor(ctx context.Context, ip string) error {
	cs, err := n.internal.RemoveBGPNeighbor(ctx, ip)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF.
func (n *Node) AddStaticRoute(ctx context.Context, vrf, prefix, nexthop string, metric int) error {
	cs, err := n.internal.AddStaticRoute(ctx, vrf, prefix, nexthop, metric)
	n.appendPending(cs)
	return err
}

// RemoveStaticRoute removes a static route from a VRF.
func (n *Node) RemoveStaticRoute(ctx context.Context, vrf, prefix string) error {
	cs, err := n.internal.RemoveStaticRoute(ctx, vrf, prefix)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — EVPN
// ============================================================================

// SetupEVPN configures the full EVPN stack (VTEP + NVO + BGP EVPN).
func (n *Node) SetupEVPN(ctx context.Context, sourceIP string) error {
	cs, err := n.internal.SetupEVPN(ctx, sourceIP)
	n.appendPending(cs)
	return err
}

// TeardownEVPN removes the EVPN configuration from the device.
func (n *Node) TeardownEVPN(ctx context.Context) error {
	cs, err := n.internal.TeardownEVPN(ctx)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — ACL
// ============================================================================

// CreateACLTable creates a new ACL table on the device.
func (n *Node) CreateACLTable(ctx context.Context, name string, config ACLTableConfig) error {
	cs, err := n.internal.CreateACLTable(ctx, name, node.ACLTableConfig{
		Type:        config.Type,
		Stage:       config.Stage,
		Ports:       config.Ports,
		Description: config.Description,
	})
	n.appendPending(cs)
	return err
}

// DeleteACLTable deletes an ACL table and its rules from the device.
func (n *Node) DeleteACLTable(ctx context.Context, name string) error {
	cs, err := n.internal.DeleteACLTable(ctx, name)
	n.appendPending(cs)
	return err
}

// AddACLRule adds a rule to an ACL table.
func (n *Node) AddACLRule(ctx context.Context, acl, ruleName string, config ACLRuleConfig) error {
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

// RemoveACLRule removes a rule from an ACL table.
func (n *Node) RemoveACLRule(ctx context.Context, acl, ruleName string) error {
	cs, err := n.internal.DeleteACLRule(ctx, acl, ruleName)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — QoS
// ============================================================================

// ApplyQoS applies a QoS policy to an interface.
// Resolves the QoS policy spec by name from the node's SpecProvider.
func (n *Node) ApplyQoS(ctx context.Context, iface, policy string) error {
	policyDef, err := n.internal.GetQoSPolicy(policy)
	if err != nil {
		return fmt.Errorf("qos policy '%s' not found: %w", policy, err)
	}
	cs, err := n.internal.ApplyQoS(ctx, iface, policy, policyDef)
	n.appendPending(cs)
	return err
}

// RemoveQoS removes QoS configuration from an interface.
func (n *Node) RemoveQoS(ctx context.Context, iface string) error {
	cs, err := n.internal.RemoveQoS(ctx, iface)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — PortChannel
// ============================================================================

// CreatePortChannel creates a new PortChannel on the device.
func (n *Node) CreatePortChannel(ctx context.Context, name string, config PortChannelConfig) error {
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
	cs, err := n.internal.DeletePortChannel(ctx, name)
	n.appendPending(cs)
	return err
}

// AddPortChannelMember adds a member interface to a PortChannel.
func (n *Node) AddPortChannelMember(ctx context.Context, pc, member string) error {
	cs, err := n.internal.AddPortChannelMember(ctx, pc, member)
	n.appendPending(cs)
	return err
}

// RemovePortChannelMember removes a member interface from a PortChannel.
func (n *Node) RemovePortChannelMember(ctx context.Context, pc, member string) error {
	cs, err := n.internal.RemovePortChannelMember(ctx, pc, member)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Baseline
// ============================================================================

// ConfigureLoopback configures the loopback interface using the device's profile.
func (n *Node) ConfigureLoopback(ctx context.Context) error {
	cs, err := n.internal.ConfigureLoopback(ctx)
	n.appendPending(cs)
	return err
}

// RemoveLoopback removes the loopback interface configuration.
func (n *Node) RemoveLoopback(ctx context.Context) error {
	cs, err := n.internal.RemoveLoopback(ctx)
	n.appendPending(cs)
	return err
}

// ============================================================================
// Device-level write ops — Device metadata
// ============================================================================

// SetDeviceMetadata writes fields to DEVICE_METADATA|localhost.
func (n *Node) SetDeviceMetadata(ctx context.Context, fields map[string]string) error {
	cs, err := n.internal.SetDeviceMetadata(ctx, fields)
	n.appendPending(cs)
	return err
}

// Cleanup identifies and removes orphaned configurations on the device.
// cleanupType may be "acls", "vrfs", "vnis", or "" for all.
func (n *Node) Cleanup(ctx context.Context, cleanupType string) (*CleanupSummary, error) {
	cs, summary, err := n.internal.Cleanup(ctx, cleanupType)
	n.appendPending(cs)
	if err != nil || summary == nil {
		return nil, err
	}
	return &CleanupSummary{
		OrphanedACLs:        summary.OrphanedACLs,
		OrphanedVRFs:        summary.OrphanedVRFs,
		OrphanedVNIMappings: summary.OrphanedVNIMappings,
	}, nil
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

// ListVLANs returns all VLAN IDs on the device.
func (n *Node) ListVLANs() []int { return n.internal.ListVLANs() }

// ListVRFs returns all VRF names on the device.
func (n *Node) ListVRFs() []string { return n.internal.ListVRFs() }

// ListPortChannels returns all PortChannel names on the device.
func (n *Node) ListPortChannels() []string { return n.internal.ListPortChannels() }

// ACLTableExists checks if an ACL table exists on the device.
func (n *Node) ACLTableExists(name string) bool { return n.internal.ACLTableExists(name) }

// GetOrphanedACLs returns ACL table names that are not bound to any interface.
func (n *Node) GetOrphanedACLs() []string { return n.internal.GetOrphanedACLs() }

// VTEPExists checks if a VTEP is configured on the device.
func (n *Node) VTEPExists() bool { return n.internal.VTEPExists() }

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
	tunnel := n.internal.Tunnel()
	if tunnel == nil {
		return "", fmt.Errorf("no SSH tunnel configured for device %s", n.internal.Name())
	}
	return tunnel.ExecCommand(cmd)
}

// ConfigReload runs 'config reload -y' on the device via SSH.
func (n *Node) ConfigReload(ctx context.Context) error {
	return n.internal.ConfigReload(ctx)
}

// ApplyFRRDefaults sets FRR runtime defaults not supported by frrcfgd templates.
func (n *Node) ApplyFRRDefaults(ctx context.Context) error {
	return n.internal.ApplyFRRDefaults(ctx)
}

// RestartService restarts a SONiC Docker container by name via SSH.
func (n *Node) RestartService(ctx context.Context, name string) error {
	return n.internal.RestartService(ctx, name)
}

// ============================================================================
// Abstract mode
// ============================================================================

// RegisterPort creates a PORT entry in the shadow ConfigDB.
// Only valid in abstract mode — enables Interface() for the port.
func (n *Node) RegisterPort(name string, fields map[string]string) {
	n.internal.RegisterPort(name, fields)
}

// BuildComposite exports accumulated entries as a CompositeInfo.
// Only valid in abstract mode.
func (n *Node) BuildComposite() *CompositeInfo {
	cc := n.internal.BuildComposite()
	return wrapComposite(cc)
}

// wrapComposite wraps a *node.CompositeConfig into a *CompositeInfo.
func wrapComposite(cc *node.CompositeConfig) *CompositeInfo {
	if cc == nil {
		return nil
	}
	ci := &CompositeInfo{
		DeviceName: cc.Metadata.DeviceName,
		Tables:     make(map[string]int),
		internal:   cc,
	}
	for table, keys := range cc.Tables {
		ci.Tables[table] = len(keys)
		ci.EntryCount += len(keys)
	}
	return ci
}

// ============================================================================
// Composite delivery
// ============================================================================

// DeliverComposite delivers a composite config to the device.
func (n *Node) DeliverComposite(ctx context.Context, ci *CompositeInfo, mode CompositeMode) (*DeliveryResult, error) {
	if ci == nil || ci.internal == nil {
		return nil, fmt.Errorf("nil CompositeInfo or missing internal state")
	}
	cc, ok := ci.internal.(*node.CompositeConfig)
	if !ok {
		return nil, fmt.Errorf("invalid CompositeInfo: unexpected internal type")
	}
	result, err := n.internal.DeliverComposite(cc, node.CompositeMode(mode))
	if err != nil {
		return nil, err
	}
	return &DeliveryResult{
		Applied: result.Applied,
		Skipped: result.Skipped,
		Failed:  result.Failed,
	}, nil
}

// VerifyComposite verifies a composite config against live CONFIG_DB.
func (n *Node) VerifyComposite(ctx context.Context, ci *CompositeInfo) (*VerificationResult, error) {
	if ci == nil || ci.internal == nil {
		return nil, fmt.Errorf("nil CompositeInfo or missing internal state")
	}
	cc, ok := ci.internal.(*node.CompositeConfig)
	if !ok {
		return nil, fmt.Errorf("invalid CompositeInfo: unexpected internal type")
	}
	result, err := n.internal.VerifyComposite(ctx, cc)
	if err != nil {
		return nil, err
	}
	vr := &VerificationResult{
		Passed: result.Passed,
		Failed: result.Failed,
	}
	for _, e := range result.Errors {
		vr.Errors = append(vr.Errors, VerificationError{
			Table:    e.Table,
			Key:      e.Key,
			Field:    e.Field,
			Expected: e.Expected,
			Actual:   e.Actual,
		})
	}
	return vr, nil
}

// ============================================================================
// Escape hatch
// ============================================================================

// InternalNode returns the underlying *node.Node for advanced callers.
func (n *Node) InternalNode() *node.Node { return n.internal }

// ============================================================================
// HealthCheck
// ============================================================================

// HealthCheck runs topology-driven health checks on this device.
// Requires the Network to have a loaded topology.
func (n *Node) HealthCheck(ctx context.Context) (*HealthReport, error) {
	if n.net == nil || !n.net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — health checks require a topology"}
	}
	provisioner, err := network.NewTopologyProvisioner(n.net.internal)
	if err != nil {
		return nil, err
	}
	report, err := provisioner.VerifyDeviceHealth(ctx, n.internal.Name())
	if err != nil {
		return nil, err
	}
	return convertHealthReport(report), nil
}

// convertHealthReport converts a *network.HealthReport to a *HealthReport.
func convertHealthReport(r *network.HealthReport) *HealthReport {
	hr := &HealthReport{
		Device: r.Device,
		Status: r.Status,
	}
	if r.ConfigCheck != nil {
		hr.ConfigCheck = &VerificationResult{
			Passed: r.ConfigCheck.Passed,
			Failed: r.ConfigCheck.Failed,
		}
		for _, e := range r.ConfigCheck.Errors {
			hr.ConfigCheck.Errors = append(hr.ConfigCheck.Errors, VerificationError{
				Table: e.Table, Key: e.Key, Field: e.Field,
				Expected: e.Expected, Actual: e.Actual,
			})
		}
	}
	for _, oc := range r.OperChecks {
		hr.OperChecks = append(hr.OperChecks, HealthCheckResult{
			Check: oc.Check, Status: oc.Status, Message: oc.Message,
		})
	}
	return hr
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
			SVI:         vlan.SVIStatus,
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
		SVI:         vlan.SVIStatus,
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

// GetServiceBinding returns the service name bound to an interface (empty if none).
func (n *Node) GetServiceBinding(iface string) (string, error) {
	intf, err := n.internal.GetInterface(iface)
	if err != nil {
		return "", err
	}
	return intf.ServiceName(), nil
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

// GetInterfaceProperty returns a single property of an interface.
func (n *Node) GetInterfaceProperty(name, property string) (string, error) {
	iface, err := n.internal.GetInterface(name)
	if err != nil {
		return "", err
	}
	switch property {
	case "admin_status", "admin-status":
		return iface.AdminStatus(), nil
	case "oper_status", "oper-status":
		return iface.OperStatus(), nil
	case "speed":
		return iface.Speed(), nil
	case "mtu":
		mtu := iface.MTU()
		if mtu == 0 {
			return "", nil
		}
		return fmt.Sprintf("%d", mtu), nil
	case "description":
		return iface.Description(), nil
	case "vrf":
		return iface.VRF(), nil
	case "service":
		return iface.ServiceName(), nil
	case "ip":
		return strings.Join(iface.IPAddresses(), ", "), nil
	default:
		return "", &ValidationError{Field: "property", Message: "unknown property: " + property}
	}
}
