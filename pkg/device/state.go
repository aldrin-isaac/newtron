package device

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// LoadState refreshes the device state from config_db and operational data
func (d *Device) LoadState(ctx context.Context) error {
	if err := d.RequireConnected(); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Reload config_db
	var err error
	d.ConfigDB, err = d.client.GetAll()
	if err != nil {
		return fmt.Errorf("loading config_db: %w", err)
	}

	// Parse interfaces
	d.State.Interfaces = d.parseInterfaces()

	// Parse port channels
	d.State.PortChannels = d.parsePortChannels()

	// Parse VLANs
	d.State.VLANs = d.parseVLANs()

	// Parse VRFs
	d.State.VRFs = d.parseVRFs()

	return nil
}

func (d *Device) parseInterfaces() map[string]*InterfaceState {
	interfaces := make(map[string]*InterfaceState)

	// Parse physical ports
	for name, port := range d.ConfigDB.Port {
		intf := &InterfaceState{
			Name:        name,
			AdminStatus: port.AdminStatus,
			Speed:       port.Speed,
		}
		if mtu, err := strconv.Atoi(port.MTU); err == nil {
			intf.MTU = mtu
		}
		interfaces[name] = intf
	}

	// Add interface config (VRF bindings, IPs)
	for key, entry := range d.ConfigDB.Interface {
		parts := strings.SplitN(key, "|", 2)
		name := parts[0]
		if intf, ok := interfaces[name]; ok {
			intf.VRF = entry.VRFName
			if len(parts) == 2 {
				// This is an IP address entry
				intf.IPAddresses = append(intf.IPAddresses, parts[1])
			}
		}
	}

	// Mark LAG members
	for key := range d.ConfigDB.PortChannelMember {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 {
			lagName := parts[0]
			memberName := parts[1]
			if intf, ok := interfaces[memberName]; ok {
				intf.LAGMember = lagName
			}
		}
	}

	// Add service bindings from NEWTRON_SERVICE_BINDING
	for intfName, binding := range d.ConfigDB.NewtronServiceBinding {
		if intf, ok := interfaces[intfName]; ok {
			intf.Service = binding.ServiceName
		}
	}

	// Add ACL bindings
	for name, acl := range d.ConfigDB.ACLTable {
		ports := strings.Split(acl.Ports, ",")
		for _, port := range ports {
			port = strings.TrimSpace(port)
			if intf, ok := interfaces[port]; ok {
				if acl.Stage == "ingress" {
					intf.IngressACL = name
				} else if acl.Stage == "egress" {
					intf.EgressACL = name
				}
			}
		}
	}

	return interfaces
}

func (d *Device) parsePortChannels() map[string]*PortChannelState {
	portChannels := make(map[string]*PortChannelState)

	for name, pc := range d.ConfigDB.PortChannel {
		state := &PortChannelState{
			Name:        name,
			AdminStatus: pc.AdminStatus,
			Members:     []string{},
		}
		portChannels[name] = state
	}

	// Find members
	for key := range d.ConfigDB.PortChannelMember {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 {
			lagName := parts[0]
			memberName := parts[1]
			if pc, ok := portChannels[lagName]; ok {
				pc.Members = append(pc.Members, memberName)
			}
		}
	}

	return portChannels
}

func (d *Device) parseVLANs() map[int]*VLANState {
	vlans := make(map[int]*VLANState)

	for name, vlan := range d.ConfigDB.VLAN {
		vlanID, err := strconv.Atoi(vlan.VLANID)
		if err != nil {
			// Try parsing from name (Vlan100)
			if strings.HasPrefix(name, "Vlan") {
				vlanID, _ = strconv.Atoi(name[4:])
			}
		}
		if vlanID == 0 {
			continue
		}

		state := &VLANState{
			ID:         vlanID,
			Name:       name,
			OperStatus: vlan.AdminStatus,
			Ports:      []string{},
		}
		vlans[vlanID] = state
	}

	// Find members
	for key, member := range d.ConfigDB.VLANMember {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 {
			vlanName := parts[0]
			portName := parts[1]
			// Parse VLAN ID from name (Vlan100)
			if strings.HasPrefix(vlanName, "Vlan") {
				vlanID, _ := strconv.Atoi(vlanName[4:])
				if vlan, ok := vlans[vlanID]; ok {
					if member.TaggingMode == "tagged" {
						vlan.Ports = append(vlan.Ports, portName+"(t)")
					} else {
						vlan.Ports = append(vlan.Ports, portName)
					}
				}
			}
		}
	}

	// Find L2VNI mappings
	for _, mapping := range d.ConfigDB.VXLANTunnelMap {
		if mapping.VLAN != "" {
			// Parse VLAN ID from name (Vlan100)
			if strings.HasPrefix(mapping.VLAN, "Vlan") {
				vlanID, _ := strconv.Atoi(mapping.VLAN[4:])
				if vlan, ok := vlans[vlanID]; ok {
					vni, _ := strconv.Atoi(mapping.VNI)
					vlan.L2VNI = vni
				}
			}
		}
	}

	// Find SVI status
	for key := range d.ConfigDB.VLANInterface {
		parts := strings.SplitN(key, "|", 2)
		vlanName := parts[0]
		if strings.HasPrefix(vlanName, "Vlan") {
			vlanID, _ := strconv.Atoi(vlanName[4:])
			if vlan, ok := vlans[vlanID]; ok {
				vlan.SVIStatus = "configured"
			}
		}
	}

	return vlans
}

func (d *Device) parseVRFs() map[string]*VRFState {
	vrfs := make(map[string]*VRFState)

	for name, vrf := range d.ConfigDB.VRF {
		state := &VRFState{
			Name:       name,
			State:      "up",
			Interfaces: []string{},
		}
		if vni, err := strconv.Atoi(vrf.VNI); err == nil {
			state.L3VNI = vni
		}
		vrfs[name] = state
	}

	// Find interfaces in VRFs
	for key, intf := range d.ConfigDB.Interface {
		if intf.VRFName != "" {
			parts := strings.SplitN(key, "|", 2)
			intfName := parts[0]
			if vrf, ok := vrfs[intf.VRFName]; ok {
				// Avoid duplicates
				found := false
				for _, i := range vrf.Interfaces {
					if i == intfName {
						found = true
						break
					}
				}
				if !found {
					vrf.Interfaces = append(vrf.Interfaces, intfName)
				}
			}
		}
	}

	// Find VLAN interfaces in VRFs
	for key := range d.ConfigDB.VLANInterface {
		parts := strings.SplitN(key, "|", 2)
		vlanName := parts[0]
		// Check if VLAN has VRF binding
		if vlanIntf, ok := d.ConfigDB.VLANInterface[vlanName]; ok {
			if vrfName, exists := vlanIntf["vrf_name"]; exists && vrfName != "" {
				if vrf, found := vrfs[vrfName]; found {
					// Avoid duplicates
					hasIt := false
					for _, i := range vrf.Interfaces {
						if i == vlanName {
							hasIt = true
							break
						}
					}
					if !hasIt {
						vrf.Interfaces = append(vrf.Interfaces, vlanName)
					}
				}
			}
		}
	}

	return vrfs
}

// ListInterfaces returns all interface names
func (d *Device) ListInterfaces() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var names []string
	for name := range d.State.Interfaces {
		names = append(names, name)
	}
	return names
}

// ListPortChannels returns all port channel names
func (d *Device) ListPortChannels() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var names []string
	for name := range d.State.PortChannels {
		names = append(names, name)
	}
	return names
}

// ListVLANs returns all VLAN IDs
func (d *Device) ListVLANs() []int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var ids []int
	for id := range d.State.VLANs {
		ids = append(ids, id)
	}
	return ids
}

// ListVRFs returns all VRF names
func (d *Device) ListVRFs() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var names []string
	for name := range d.State.VRFs {
		names = append(names, name)
	}
	return names
}

// GetInterfaceSummary returns a summary of all interfaces
func (d *Device) GetInterfaceSummary() []InterfaceSummary {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var summary []InterfaceSummary
	for name, intf := range d.State.Interfaces {
		s := InterfaceSummary{
			Name:        name,
			AdminStatus: intf.AdminStatus,
			Speed:       intf.Speed,
			VRF:         intf.VRF,
			Service:     intf.Service,
			LAGMember:   intf.LAGMember,
		}
		if len(intf.IPAddresses) > 0 {
			s.IPAddress = intf.IPAddresses[0]
		}
		summary = append(summary, s)
	}
	return summary
}

// InterfaceSummary is a compact interface summary
type InterfaceSummary struct {
	Name        string
	AdminStatus string
	Speed       string
	IPAddress   string
	VRF         string
	Service     string
	LAGMember   string
}
