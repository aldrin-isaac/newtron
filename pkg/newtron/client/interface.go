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

// SetIP sets an IP address on an interface.
func (c *Client) SetIP(device, iface, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.SetIPRequest{IP: ip}
	return c.interfaceWrite(device, iface, "set-ip", body, opts)
}

// RemoveIP removes an IP address from an interface.
func (c *Client) RemoveIP(device, iface, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.RemoveIPRequest{IP: ip}
	return c.interfaceWrite(device, iface, "remove-ip", body, opts)
}

// SetVRF assigns an interface to a VRF.
func (c *Client) SetVRF(device, iface, vrf string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.SetVRFRequest{VRF: vrf}
	return c.interfaceWrite(device, iface, "set-vrf", body, opts)
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

// BindMACVPN binds a MAC-VPN to an interface.
func (c *Client) BindMACVPN(device, iface, macvpn string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindMACVPNRequest{MACVPN: macvpn}
	return c.interfaceWrite(device, iface, "bind-macvpn", body, opts)
}

// UnbindMACVPN unbinds a MAC-VPN from an interface.
func (c *Client) UnbindMACVPN(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "unbind-macvpn", nil, opts)
}

// InterfaceAddBGPNeighbor adds a direct (interface-level) BGP neighbor.
func (c *Client) InterfaceAddBGPNeighbor(device, iface string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "add-bgp-neighbor", config, opts)
}

// InterfaceRemoveBGPNeighbor removes a BGP neighbor from an interface.
func (c *Client) InterfaceRemoveBGPNeighbor(device, iface, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		IP string `json:"ip"`
	}{IP: ip}
	return c.interfaceWrite(device, iface, "remove-bgp-neighbor", body, opts)
}

// InterfaceSet sets a property on an interface.
func (c *Client) InterfaceSet(device, iface, property, value string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.InterfaceSetRequest{Property: property, Value: value}
	return c.interfaceWrite(device, iface, "set", body, opts)
}

// InterfaceApplyQoS applies QoS to an interface.
func (c *Client) InterfaceApplyQoS(device, iface, policy string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.ApplyQoSRequest{Policy: policy}
	return c.interfaceWrite(device, iface, "apply-qos", body, opts)
}

// InterfaceRemoveQoS removes QoS from an interface.
func (c *Client) InterfaceRemoveQoS(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "remove-qos", nil, opts)
}
