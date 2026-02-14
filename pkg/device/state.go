package device

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
