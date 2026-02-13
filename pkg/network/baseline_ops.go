package network

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Baseline Configuration
// ============================================================================

// ApplyBaseline applies a baseline configlet to the device.
func (d *Device) ApplyBaseline(ctx context.Context, configletName string, vars []string) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
	}

	// Parse vars into a map
	varMap := make(map[string]string)
	for _, v := range vars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) == 2 {
			varMap[parts[0]] = parts[1]
		}
	}

	// Add default variables from resolved config
	if d.resolved != nil {
		if _, ok := varMap["loopback_ip"]; !ok {
			varMap["loopback_ip"] = d.resolved.LoopbackIP
		}
		if _, ok := varMap["device_name"]; !ok {
			varMap["device_name"] = d.name
		}
	}

	cs := NewChangeSet(d.name, "device.apply-baseline")

	// Load configlet based on name (simplified - in production would load from file)
	switch configletName {
	case "sonic-baseline":
		// Basic SONiC baseline
		cs.Add("DEVICE_METADATA", "localhost", ChangeModify, nil, map[string]string{
			"hostname": varMap["device_name"],
		})
		if loopbackIP, ok := varMap["loopback_ip"]; ok && loopbackIP != "" {
			cs.Add("LOOPBACK_INTERFACE", fmt.Sprintf("Loopback0|%s/32", loopbackIP), ChangeAdd, nil, map[string]string{})
		}

	case "sonic-evpn":
		// EVPN baseline - create VTEP
		if loopbackIP, ok := varMap["loopback_ip"]; ok && loopbackIP != "" {
			cs.Add("VXLAN_TUNNEL", "vtep1", ChangeAdd, nil, map[string]string{
				"src_ip": loopbackIP,
			})
			cs.Add("VXLAN_EVPN_NVO", "nvo1", ChangeAdd, nil, map[string]string{
				"source_vtep": "vtep1",
			})
		}

	default:
		return nil, fmt.Errorf("unknown configlet: %s", configletName)
	}

	util.WithDevice(d.name).Infof("Applied baseline configlet '%s'", configletName)
	return cs, nil
}

// ============================================================================
// Cleanup (Orphaned Resource Removal)
// ============================================================================

// CleanupSummary provides details about orphaned resources found.
type CleanupSummary struct {
	OrphanedACLs        []string
	OrphanedVRFs        []string
	OrphanedVNIMappings []string
}

// Cleanup identifies and removes orphaned configurations.
// Returns a changeset to remove them and a summary of what was found.
func (d *Device) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error) {
	if err := requireWritable(d); err != nil {
		return nil, nil, err
	}

	cs := NewChangeSet(d.name, "device.cleanup")
	summary := &CleanupSummary{}

	configDB := d.ConfigDB()
	if configDB == nil {
		return cs, summary, nil
	}

	// Find orphaned ACLs (no interfaces bound)
	if cleanupType == "" || cleanupType == "acl" {
		for aclName, acl := range configDB.ACLTable {
			if acl.Ports == "" {
				summary.OrphanedACLs = append(summary.OrphanedACLs, aclName)

				// Delete rules first
				prefix := aclName + "|"
				for ruleKey := range configDB.ACLRule {
					if strings.HasPrefix(ruleKey, prefix) {
						cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
					}
				}
				cs.Add("ACL_TABLE", aclName, ChangeDelete, nil, nil)
			}
		}
	}

	// Find orphaned VRFs (no interfaces bound)
	if cleanupType == "" || cleanupType == "vrf" {
		for vrfName := range configDB.VRF {
			if vrfName == "default" {
				continue
			}
			hasUsers := false
			for intfName, intf := range configDB.Interface {
				if strings.Contains(intfName, "|") {
					continue
				}
				if intf.VRFName == vrfName {
					hasUsers = true
					break
				}
			}
			if !hasUsers {
				summary.OrphanedVRFs = append(summary.OrphanedVRFs, vrfName)
				cs.Add("VRF", vrfName, ChangeDelete, nil, nil)
			}
		}
	}

	// Find orphaned VNI mappings (VRF or VLAN doesn't exist)
	if cleanupType == "" || cleanupType == "vni" {
		for mapKey, mapping := range configDB.VXLANTunnelMap {
			orphaned := false
			if mapping.VRF != "" {
				if _, ok := configDB.VRF[mapping.VRF]; !ok {
					orphaned = true
				}
			}
			if mapping.VLAN != "" {
				if _, ok := configDB.VLAN[mapping.VLAN]; !ok {
					orphaned = true
				}
			}
			if orphaned {
				summary.OrphanedVNIMappings = append(summary.OrphanedVNIMappings, mapKey)
				cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
			}
		}
	}

	return cs, summary, nil
}

// ============================================================================
// v3: Port Creation Operations
// ============================================================================

// CreatePort creates a PORT entry validated against the device's platform.json.
func (d *Device) CreatePort(ctx context.Context, cfg device.CreatePortConfig) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
	}

	underlying := d.Underlying()

	// Validate against platform.json if loaded
	if underlying.PlatformConfig != nil {
		if err := underlying.PlatformConfig.ValidatePort(cfg); err != nil {
			return nil, fmt.Errorf("port validation failed: %w", err)
		}

		// Check for conflicting ports (shared lanes)
		conflicts := underlying.PlatformConfig.HasConflictingPorts(cfg.Name, d.ConfigDB().Port)
		if len(conflicts) > 0 {
			return nil, fmt.Errorf("port %s conflicts with existing ports: %s (shared lanes)",
				cfg.Name, strings.Join(conflicts, ", "))
		}
	}

	// Check port doesn't already exist
	if _, ok := d.ConfigDB().Port[cfg.Name]; ok {
		return nil, fmt.Errorf("port %s already exists", cfg.Name)
	}

	cs := NewChangeSet(d.Name(), "device.create-port")

	fields := map[string]string{
		"admin_status": "up",
	}
	if cfg.AdminStatus != "" {
		fields["admin_status"] = cfg.AdminStatus
	}
	if cfg.Speed != "" {
		fields["speed"] = cfg.Speed
	}
	if cfg.Lanes != "" {
		fields["lanes"] = cfg.Lanes
	} else if underlying.PlatformConfig != nil {
		// Use lanes from platform.json
		if portDef, ok := underlying.PlatformConfig.Interfaces[cfg.Name]; ok {
			fields["lanes"] = portDef.Lanes
		}
	}
	if cfg.FEC != "" {
		fields["fec"] = cfg.FEC
	}
	if cfg.MTU > 0 {
		fields["mtu"] = fmt.Sprintf("%d", cfg.MTU)
	} else {
		fields["mtu"] = "9100" // SONiC default
	}
	if cfg.Alias != "" {
		fields["alias"] = cfg.Alias
	}
	if cfg.Index != "" {
		fields["index"] = cfg.Index
	}

	cs.Add("PORT", cfg.Name, ChangeAdd, nil, fields)

	util.WithDevice(d.Name()).Infof("Created port %s", cfg.Name)
	return cs, nil
}

// DeletePort removes a PORT entry.
func (d *Device) DeletePort(ctx context.Context, name string) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
	}

	if _, ok := d.ConfigDB().Port[name]; !ok {
		return nil, fmt.Errorf("port %s does not exist", name)
	}

	// Check no services bound
	if binding, ok := d.ConfigDB().NewtronServiceBinding[name]; ok {
		return nil, fmt.Errorf("port %s has service '%s' bound — remove it first", name, binding.ServiceName)
	}

	cs := NewChangeSet(d.Name(), "device.delete-port")
	cs.Add("PORT", name, ChangeDelete, nil, nil)

	util.WithDevice(d.Name()).Infof("Deleted port %s", name)
	return cs, nil
}

// BreakoutPort applies a breakout mode to a port, creating child ports and removing the parent.
func (d *Device) BreakoutPort(ctx context.Context, cfg device.BreakoutConfig) (*ChangeSet, error) {
	if err := requireWritable(d); err != nil {
		return nil, err
	}

	underlying := d.Underlying()
	if underlying.PlatformConfig == nil {
		return nil, fmt.Errorf("platform config not loaded — call LoadPlatformConfig first")
	}

	// Validate breakout mode
	if err := underlying.PlatformConfig.ValidateBreakout(cfg); err != nil {
		return nil, fmt.Errorf("breakout validation failed: %w", err)
	}

	// Get child ports
	childPorts, err := underlying.PlatformConfig.GetChildPorts(cfg.ParentPort, cfg.Mode)
	if err != nil {
		return nil, fmt.Errorf("cannot determine child ports: %w", err)
	}

	// Parent port must not have services
	if binding, ok := d.ConfigDB().NewtronServiceBinding[cfg.ParentPort]; ok {
		return nil, fmt.Errorf("parent port %s has service '%s' bound — remove it first",
			cfg.ParentPort, binding.ServiceName)
	}

	cs := NewChangeSet(d.Name(), "device.breakout-port")

	// Delete parent port
	cs.Add("PORT", cfg.ParentPort, ChangeDelete, nil, nil)

	// Parse breakout mode for child speed (e.g., "4x25G" -> "25000")
	childSpeed := parseBreakoutSpeed(cfg.Mode)

	// Get parent port definition for lane distribution
	parentDef := underlying.PlatformConfig.Interfaces[cfg.ParentPort]
	parentLanes := strings.Split(parentDef.Lanes, ",")
	lanesPerChild := len(parentLanes) / len(childPorts)

	// Create child ports
	for i, childName := range childPorts {
		startLane := i * lanesPerChild
		endLane := startLane + lanesPerChild
		if endLane > len(parentLanes) {
			endLane = len(parentLanes)
		}
		childLanes := strings.Join(parentLanes[startLane:endLane], ",")

		cs.Add("PORT", childName, ChangeAdd, nil, map[string]string{
			"admin_status": "up",
			"speed":        childSpeed,
			"lanes":        childLanes,
			"mtu":          "9100",
			"index":        fmt.Sprintf("%d", i),
		})
	}

	util.WithDevice(d.Name()).Infof("Breakout port %s into %s (%d child ports)",
		cfg.ParentPort, cfg.Mode, len(childPorts))
	return cs, nil
}

// LoadPlatformConfig fetches and caches platform.json from the device via SSH.
func (d *Device) LoadPlatformConfig(ctx context.Context) error {
	if !d.IsConnected() {
		return fmt.Errorf("device not connected")
	}

	underlying := d.Underlying()

	// Get platform identifier from DEVICE_METADATA
	configDB := d.ConfigDB()
	if configDB == nil {
		return fmt.Errorf("config_db not loaded")
	}

	meta, ok := configDB.DeviceMetadata["localhost"]
	if !ok {
		return fmt.Errorf("DEVICE_METADATA|localhost not found")
	}
	platform := meta["platform"]
	if platform == "" {
		return fmt.Errorf("platform field not set in DEVICE_METADATA")
	}

	// Read platform.json via SSH
	path := fmt.Sprintf("/usr/share/sonic/device/%s/platform.json", platform)
	data, err := d.readFileViaSSH(ctx, path)
	if err != nil {
		return fmt.Errorf("reading platform.json: %w", err)
	}

	config, err := device.ParsePlatformJSON(data)
	if err != nil {
		return err
	}

	underlying.PlatformConfig = config
	util.WithDevice(d.Name()).Infof("Loaded platform config: %d interfaces", len(config.Interfaces))
	return nil
}

// GeneratePlatformSpec creates a spec.PlatformSpec from the device's platform.json.
// Used to prime the spec system on first connect to a new hardware platform.
func (d *Device) GeneratePlatformSpec(ctx context.Context) (*spec.PlatformSpec, error) {
	underlying := d.Underlying()
	if underlying.PlatformConfig == nil {
		return nil, fmt.Errorf("platform config not loaded — call LoadPlatformConfig first")
	}

	configDB := d.ConfigDB()
	hwsku := ""
	if meta, ok := configDB.DeviceMetadata["localhost"]; ok {
		hwsku = meta["hwsku"]
	}

	return underlying.PlatformConfig.GeneratePlatformSpec(hwsku), nil
}

// readFileViaSSH reads a file from the device via SSH tunnel.
// This is a placeholder — the actual implementation uses the device's SSH tunnel.
func (d *Device) readFileViaSSH(ctx context.Context, path string) ([]byte, error) {
	// In production, this would execute "cat <path>" over the SSH tunnel.
	// For now, return an error indicating SSH file read is not yet implemented.
	return nil, fmt.Errorf("SSH file read not yet implemented for path: %s", path)
}
