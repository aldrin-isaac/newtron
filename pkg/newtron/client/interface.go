package client

import (
	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/api"
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

// InterfaceApplyQoS applies a QoS policy to an interface (interface-level).
func (c *Client) InterfaceApplyQoS(device, iface, policy string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.ApplyQoSRequest{Policy: policy}
	return c.interfaceWrite(device, iface, "apply-qos", body, opts)
}

// InterfaceRemoveQoS removes QoS from an interface (interface-level).
func (c *Client) InterfaceRemoveQoS(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "remove-qos", nil, opts)
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

