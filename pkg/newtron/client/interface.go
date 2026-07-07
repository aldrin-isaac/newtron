package client

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
)

// ============================================================================
// Interface write operations
// ============================================================================

// interfaceWrite performs a write operation on an interface endpoint.
func (c *Client) interfaceWrite(device, iface, endpoint string, body any, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	var result newtron.WriteResult
	if err := c.doPost(c.interfacePath(device, iface)+"/"+endpoint+execQuery(opts), body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ApplyService applies a service to an interface.
func (c *Client) ApplyService(device, iface, service string, serviceOpts newtron.ApplyServiceOpts, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.ApplyServiceRequest{
		Service:   service,
		IPAddress: serviceOpts.IPAddress,
		VLAN:      serviceOpts.VLAN,
		PeerAS:    serviceOpts.PeerAS,
		Params:    serviceOpts.Params,
	}
	return c.interfaceWrite(device, iface, "apply-service", body, opts)
}

// RemoveService removes a service from an interface.
func (c *Client) RemoveService(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "remove-service", nil, opts)
}

// RefreshService refreshes a service on an interface.
func (c *Client) RefreshService(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "refresh-service", nil, opts)
}

// BindACL binds an ACL to an interface.
func (c *Client) BindACL(device, iface, acl, direction string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindACLRequest{ACL: acl, Direction: direction}
	return c.interfaceWrite(device, iface, "bind-acl", body, opts)
}

// UnbindACL unbinds an ACL from an interface.
func (c *Client) UnbindACL(device, iface, acl string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.UnbindACLRequest{ACL: acl}
	return c.interfaceWrite(device, iface, "unbind-acl", body, opts)
}

// InterfaceAddBGPPeer adds a direct (interface-level) BGP peer.
func (c *Client) InterfaceAddBGPPeer(device, iface string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "add-bgp-peer", config, opts)
}

// InterfaceUpdateBGPPeer atomically mutates the BGP peer's fields on
// an interface. The composite key (vrf + neighbor_ip) is the row's
// identity (§47); a re-IP is remove + add via separate verbs. #227.
func (c *Client) InterfaceUpdateBGPPeer(device, iface string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "update-bgp-peer", config, opts)
}

// InterfaceRemoveBGPPeer removes a BGP peer from an interface.
// The neighbor IP is read from the intent record on the device.
func (c *Client) InterfaceRemoveBGPPeer(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "remove-bgp-peer", nil, opts)
}

// SetProperty sets a property on an interface.
func (c *Client) SetProperty(device, iface, property, value string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.InterfaceSetRequest{Property: property, Value: value}
	return c.interfaceWrite(device, iface, "set-property", body, opts)
}

// ClearProperty clears a property override on an interface, reverting to default.
func (c *Client) ClearProperty(device, iface, property string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.InterfaceClearRequest{Property: property}
	return c.interfaceWrite(device, iface, "clear-property", body, opts)
}

// InterfaceBindQoS binds a QoS policy to an interface (interface-level).
func (c *Client) InterfaceBindQoS(device, iface, policy string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindQoSRequest{Policy: policy}
	return c.interfaceWrite(device, iface, "bind-qos", body, opts)
}

// InterfaceUnbindQoS unbinds QoS from an interface (interface-level).
func (c *Client) InterfaceUnbindQoS(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "unbind-qos", nil, opts)
}

// ConfigureInterface sets forwarding mode on an interface.
func (c *Client) ConfigureInterface(device, iface string, cfg api.ConfigureInterfaceRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "configure-interface", cfg, opts)
}

// UnconfigureInterface reverses ConfigureInterface. Reads the intent record to
// determine what was configured, then undoes it.
func (c *Client) UnconfigureInterface(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "unconfigure-interface", nil, opts)
}

// RemoveTrunkVLAN strips one VLAN from an interface's trunk membership
// without affecting other VLANs or the rest of the port configuration.
// Reverse mirror of ConfigureInterface(tagged=true) per §15 (#224).
func (c *Client) RemoveTrunkVLAN(device, iface string, vlanID int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "remove-trunk-vlan", api.RemoveTrunkVLANRequest{VLAN: vlanID}, opts)
}

