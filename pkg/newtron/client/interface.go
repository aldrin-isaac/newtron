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

// BindMACVPN binds a MAC-VPN to an interface.
func (c *Client) BindMACVPN(device, iface, macvpn string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindMACVPNRequest{MACVPN: macvpn}
	return c.interfaceWrite(device, iface, "bind-macvpn", body, opts)
}

// UnbindMACVPN unbinds a MAC-VPN from an interface.
func (c *Client) UnbindMACVPN(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "unbind-macvpn", nil, opts)
}

// InterfaceAddBGPPeer adds a direct (interface-level) BGP peer.
func (c *Client) InterfaceAddBGPPeer(device, iface string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.interfaceWrite(device, iface, "add-bgp-peer", config, opts)
}

// InterfaceRemoveBGPPeer removes a BGP peer from an interface.
func (c *Client) InterfaceRemoveBGPPeer(device, iface, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		IP string `json:"ip"`
	}{IP: ip}
	return c.interfaceWrite(device, iface, "remove-bgp-peer", body, opts)
}

// SetPortProperty sets a property on an interface.
func (c *Client) SetPortProperty(device, iface, property, value string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.InterfaceSetRequest{Property: property, Value: value}
	return c.interfaceWrite(device, iface, "set-port-property", body, opts)
}

// ConfigureInterface sets VRF and IP on an interface in one operation.
func (c *Client) ConfigureInterface(device, iface, vrf, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.ConfigureInterfaceRequest{VRF: vrf, IP: ip}
	return c.interfaceWrite(device, iface, "configure-interface", body, opts)
}

// UnconfigureInterface removes VRF binding and/or IP address from an interface.
func (c *Client) UnconfigureInterface(device, iface, vrf, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.ConfigureInterfaceRequest{VRF: vrf, IP: ip}
	return c.interfaceWrite(device, iface, "unconfigure-interface", body, opts)
}

