package newtron

// ephemeral_ops.go provides Network-level convenience methods that handle the
// full operation lifecycle: connect → permission check → lock → op → commit →
// save → close. These are the methods that CLI commands call directly.

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
)

// ============================================================================
// Service (interface-scoped)
// ============================================================================

// ApplyService applies a service to an interface on a device.
func (net *Network) ApplyService(ctx context.Context, req ApplyServiceRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermServiceApply,
		auth.NewContext().WithDevice(req.Device).WithService(req.Service).WithInterface(req.Interface)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return err
		}
		return iface.ApplyService(ctx, req.Service, ApplyServiceOpts{IPAddress: req.IPAddress, PeerAS: req.PeerAS})
	})
}

// RemoveService removes the service from an interface on a device.
func (net *Network) RemoveService(ctx context.Context, req RemoveServiceRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermServiceRemove,
		auth.NewContext().WithDevice(req.Device).WithInterface(req.Interface)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return err
		}
		return iface.RemoveService(ctx)
	})
}

// RefreshService reapplies the service configuration on an interface to sync
// with the current service definition.
func (net *Network) RefreshService(ctx context.Context, req RefreshServiceRequest, opts ExecOpts) (*WriteResult, error) {
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return err
		}
		if !iface.HasService() {
			return fmt.Errorf("no service bound to interface %s", req.Interface)
		}
		if err := net.checkPermission(auth.PermServiceApply,
			auth.NewContext().WithDevice(req.Device).WithResource(req.Interface).WithService(iface.ServiceName())); err != nil {
			return err
		}
		return iface.RefreshService(ctx)
	})
}

// ============================================================================
// VLAN
// ============================================================================

// CreateVLAN creates a VLAN on a device.
func (net *Network) CreateVLAN(ctx context.Context, req CreateVLANRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVLANCreate,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.CreateVLAN(ctx, req.VlanID, VLANConfig{Description: req.Description})
	})
}

// DeleteVLAN deletes a VLAN from a device.
func (net *Network) DeleteVLAN(ctx context.Context, req DeleteVLANRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVLANDelete,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.DeleteVLAN(ctx, req.VlanID)
	})
}

// AddVLANMember adds an interface to a VLAN on a device.
func (net *Network) AddVLANMember(ctx context.Context, req AddVLANMemberRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVLANModify,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.AddVLANMember(ctx, req.VlanID, req.Interface, req.Tagged)
	})
}

// RemoveVLANMember removes an interface from a VLAN on a device.
func (net *Network) RemoveVLANMember(ctx context.Context, req RemoveVLANMemberRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVLANModify,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveVLANMember(ctx, req.VlanID, req.Interface)
	})
}

// ConfigureSVI configures the SVI (Layer 3 VLAN interface) for a VLAN on a device.
func (net *Network) ConfigureSVI(ctx context.Context, req ConfigureSVIRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermInterfaceModify,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.ConfigureSVI(ctx, req.VlanID, SVIConfig{VRF: req.VRF, IPAddress: req.IPAddress, AnycastMAC: req.AnycastMAC})
	})
}

// RemoveSVI removes the SVI configuration from a VLAN on a device.
func (net *Network) RemoveSVI(ctx context.Context, req RemoveSVIRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermInterfaceModify,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveSVI(ctx, req.VlanID)
	})
}

// BindMACVPN binds a VLAN to a MAC-VPN definition on a device.
func (net *Network) BindMACVPN(ctx context.Context, req BindMACVPNRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermEVPNModify,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(node.VLANName(req.VlanID))
		if err != nil {
			return fmt.Errorf("VLAN %d not found: %w", req.VlanID, err)
		}
		return iface.BindMACVPN(ctx, req.MACVPName)
	})
}

// UnbindMACVPN unbinds the MAC-VPN from a VLAN on a device.
func (net *Network) UnbindMACVPN(ctx context.Context, req UnbindMACVPNRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermEVPNModify,
		auth.NewContext().WithDevice(req.Device).WithResource(node.VLANName(req.VlanID))); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(node.VLANName(req.VlanID))
		if err != nil {
			return fmt.Errorf("VLAN %d not found: %w", req.VlanID, err)
		}
		return iface.UnbindMACVPN(ctx)
	})
}

// ============================================================================
// VRF
// ============================================================================

// CreateVRF creates a VRF on a device.
func (net *Network) CreateVRF(ctx context.Context, req CreateVRFRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFCreate,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Name)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.CreateVRF(ctx, req.Name, VRFConfig{})
	})
}

// DeleteVRF deletes a VRF from a device.
func (net *Network) DeleteVRF(ctx context.Context, req DeleteVRFRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFDelete,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Name)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.DeleteVRF(ctx, req.Name)
	})
}

// AddVRFInterface adds an interface to a VRF on a device.
func (net *Network) AddVRFInterface(ctx context.Context, req AddVRFInterfaceRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.AddVRFInterface(ctx, req.VRF, req.Interface)
	})
}

// RemoveVRFInterface removes an interface from a VRF on a device.
func (net *Network) RemoveVRFInterface(ctx context.Context, req RemoveVRFInterfaceRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveVRFInterface(ctx, req.VRF, req.Interface)
	})
}

// BindIPVPN binds a VRF to an IP-VPN definition on a device.
func (net *Network) BindIPVPN(ctx context.Context, req BindIPVPNRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.BindIPVPN(ctx, req.VRF, req.IPVPN)
	})
}

// UnbindIPVPN unbinds the IP-VPN from a VRF on a device.
func (net *Network) UnbindIPVPN(ctx context.Context, req UnbindIPVPNRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.UnbindIPVPN(ctx, req.VRF)
	})
}

// ============================================================================
// BGP Neighbor
// ============================================================================

// AddBGPNeighbor adds a BGP neighbor on a device.
// If req.Interface is set, adds an interface-scoped (direct) neighbor.
// Otherwise adds a loopback BGP neighbor (indirect, EVPN overlay).
func (net *Network) AddBGPNeighbor(ctx context.Context, req AddBGPNeighborRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		if req.Interface != "" {
			iface, err := n.Interface(req.Interface)
			if err != nil {
				return err
			}
			if req.VRF != "" && iface.VRF() != req.VRF {
				return fmt.Errorf("interface %s is not in VRF %s (current VRF: %q)", req.Interface, req.VRF, iface.VRF())
			}
			return iface.AddBGPNeighbor(ctx, BGPNeighborConfig{
				NeighborIP:  req.NeighborIP,
				RemoteAS:    req.RemoteAS,
				Description: req.Description,
			})
		}
		return n.AddBGPNeighbor(ctx, BGPNeighborConfig{
			NeighborIP:  req.NeighborIP,
			RemoteAS:    req.RemoteAS,
			Description: req.Description,
		})
	})
}

// RemoveBGPNeighbor removes a BGP neighbor from a device.
// If req.Interface is set, removes via interface. If req.NeighborIP is set,
// removes by IP. If req.Target is set (CLI compat), auto-detects.
func (net *Network) RemoveBGPNeighbor(ctx context.Context, req RemoveBGPNeighborRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		// Explicit interface path
		if req.Interface != "" {
			iface, err := n.Interface(req.Interface)
			if err != nil {
				return err
			}
			return iface.RemoveBGPNeighbor(ctx, req.NeighborIP)
		}
		// Explicit neighbor IP path
		if req.NeighborIP != "" {
			return n.RemoveBGPNeighbor(ctx, req.NeighborIP)
		}
		// CLI compat: Target is ambiguous (interface name or neighbor IP)
		if req.Target != "" {
			iface, intfErr := n.Interface(req.Target)
			if intfErr == nil {
				return iface.RemoveBGPNeighbor(ctx, "")
			}
			return n.RemoveBGPNeighbor(ctx, req.Target)
		}
		return fmt.Errorf("remove-bgp-neighbor: no target specified")
	})
}

// ============================================================================
// Static Routes
// ============================================================================

// AddStaticRoute adds a static route to a VRF on a device.
func (net *Network) AddStaticRoute(ctx context.Context, req AddStaticRouteRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.AddStaticRoute(ctx, req.VRF, req.Prefix, req.NextHop, req.Metric)
	})
}

// RemoveStaticRoute removes a static route from a VRF on a device.
func (net *Network) RemoveStaticRoute(ctx context.Context, req RemoveStaticRouteRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermVRFModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.VRF)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveStaticRoute(ctx, req.VRF, req.Prefix)
	})
}

// ============================================================================
// EVPN
// ============================================================================

// SetupEVPN configures the full EVPN stack (VTEP + NVO + BGP EVPN) on a device.
func (net *Network) SetupEVPN(ctx context.Context, req SetupEVPNRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermEVPNModify,
		auth.NewContext().WithDevice(req.Device).WithResource("evpn")); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.SetupEVPN(ctx, req.SourceIP)
	})
}

// TeardownEVPN removes the EVPN configuration from a device.
func (net *Network) TeardownEVPN(ctx context.Context, req TeardownEVPNRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermEVPNModify,
		auth.NewContext().WithDevice(req.Device).WithResource("evpn")); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.TeardownEVPN(ctx)
	})
}

// ============================================================================
// ACL
// ============================================================================

// CreateACL creates a new ACL table on a device.
func (net *Network) CreateACL(ctx context.Context, req CreateACLRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermACLModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Name)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.CreateACLTable(ctx, req.Name, ACLTableConfig{
			Type:        req.Type,
			Stage:       req.Stage,
			Ports:       req.Ports,
			Description: req.Description,
		})
	})
}

// DeleteACL deletes an ACL table and its rules from a device.
func (net *Network) DeleteACL(ctx context.Context, req DeleteACLRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermACLModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Name)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		if !n.ACLTableExists(req.Name) {
			return fmt.Errorf("ACL table '%s' not found", req.Name)
		}
		return n.DeleteACLTable(ctx, req.Name)
	})
}

// AddACLRule adds a rule to an ACL table on a device.
func (net *Network) AddACLRule(ctx context.Context, req AddACLRuleRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermACLModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.ACLName)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		if !n.ACLTableExists(req.ACLName) {
			return fmt.Errorf("ACL table '%s' not found", req.ACLName)
		}
		return n.AddACLRule(ctx, req.ACLName, req.RuleName, ACLRuleConfig{
			Priority: req.Priority,
			Action:   req.Action,
			SrcIP:    req.SrcIP,
			DstIP:    req.DstIP,
			Protocol: req.Protocol,
			SrcPort:  req.SrcPort,
			DstPort:  req.DstPort,
		})
	})
}

// RemoveACLRule removes a rule from an ACL table on a device.
func (net *Network) RemoveACLRule(ctx context.Context, req RemoveACLRuleRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermACLModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.ACLName)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		if !n.ACLTableExists(req.ACLName) {
			return fmt.Errorf("ACL table '%s' not found", req.ACLName)
		}
		return n.RemoveACLRule(ctx, req.ACLName, req.RuleName)
	})
}

// BindACL binds an ACL to an interface on a device.
func (net *Network) BindACL(ctx context.Context, req BindACLRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermACLModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.ACLName)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		if !n.ACLTableExists(req.ACLName) {
			return fmt.Errorf("ACL table '%s' not found", req.ACLName)
		}
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return fmt.Errorf("interface not found: %w", err)
		}
		return iface.BindACL(ctx, req.ACLName, req.Direction)
	})
}

// UnbindACL unbinds an ACL from an interface on a device.
func (net *Network) UnbindACL(ctx context.Context, req UnbindACLRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermACLModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.ACLName)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		if !n.ACLTableExists(req.ACLName) {
			return fmt.Errorf("ACL table '%s' not found", req.ACLName)
		}
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return fmt.Errorf("interface not found: %w", err)
		}
		return iface.UnbindACL(ctx, req.ACLName)
	})
}

// ============================================================================
// QoS
// ============================================================================

// ApplyQoS applies a QoS policy to an interface on a device.
func (net *Network) ApplyQoS(ctx context.Context, req ApplyQoSRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermQoSModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Interface)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.ApplyQoS(ctx, req.Interface, req.Policy)
	})
}

// RemoveQoS removes QoS configuration from an interface on a device.
func (net *Network) RemoveQoS(ctx context.Context, req RemoveQoSRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermQoSModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Interface)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveQoS(ctx, req.Interface)
	})
}

// ============================================================================
// Interface
// ============================================================================

// SetInterfaceProperty sets a property on an interface on a device.
// Property "ip" calls SetIP, property "vrf" calls SetVRF, all others call Set.
func (net *Network) SetInterfaceProperty(ctx context.Context, req SetInterfacePropertyRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermInterfaceModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Interface)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return err
		}
		switch req.Property {
		case "ip":
			return iface.SetIP(ctx, req.Value)
		case "vrf":
			return iface.SetVRF(ctx, req.Value)
		default:
			return iface.Set(ctx, req.Property, req.Value)
		}
	})
}

// RemoveIP removes an IP address from an interface on a device.
func (net *Network) RemoveIP(ctx context.Context, req RemoveIPRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermInterfaceModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Interface)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		iface, err := n.Interface(req.Interface)
		if err != nil {
			return err
		}
		return iface.RemoveIP(ctx, req.IP)
	})
}

// ============================================================================
// LAG (PortChannel)
// ============================================================================

// CreateLAG creates a new PortChannel on a device.
func (net *Network) CreateLAG(ctx context.Context, req CreateLAGRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermLAGCreate,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Name)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.CreatePortChannel(ctx, req.Name, PortChannelConfig{
			Members:  req.Members,
			MinLinks: req.MinLinks,
			FastRate: req.FastRate,
			Fallback: req.Fallback,
			MTU:      req.MTU,
		})
	})
}

// DeleteLAG deletes a PortChannel from a device.
func (net *Network) DeleteLAG(ctx context.Context, req DeleteLAGRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermLAGDelete,
		auth.NewContext().WithDevice(req.Device).WithResource(req.Name)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.DeletePortChannel(ctx, req.Name)
	})
}

// AddLAGMember adds a member interface to a LAG on a device.
func (net *Network) AddLAGMember(ctx context.Context, req AddLAGMemberRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermLAGModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.LAG)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.AddPortChannelMember(ctx, req.LAG, req.Member)
	})
}

// RemoveLAGMember removes a member interface from a LAG on a device.
func (net *Network) RemoveLAGMember(ctx context.Context, req RemoveLAGMemberRequest, opts ExecOpts) (*WriteResult, error) {
	if err := net.checkPermission(auth.PermLAGModify,
		auth.NewContext().WithDevice(req.Device).WithResource(req.LAG)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemovePortChannelMember(ctx, req.LAG, req.Member)
	})
}

// ============================================================================
// Device-level: Cleanup
// ============================================================================

// Cleanup identifies and removes orphaned configurations on a device.
// Returns a CleanupSummary describing what was found and removed.
func (net *Network) Cleanup(ctx context.Context, req CleanupRequest, opts ExecOpts) (*CleanupSummary, error) {
	if err := net.checkPermission(auth.PermDeviceCleanup,
		auth.NewContext().WithDevice(req.Device)); err != nil {
		return nil, err
	}
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()

	if err := n.Lock(); err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer n.Unlock()

	summary, err := n.Cleanup(ctx, req.Type)
	if err != nil {
		return nil, err
	}

	if !opts.Execute {
		n.Rollback()
		return summary, nil
	}

	if _, commitErr := n.Commit(ctx); commitErr != nil {
		return summary, commitErr
	}
	if !opts.NoSave {
		n.Save(ctx) //nolint:errcheck // best-effort save; summary already returned
	}
	return summary, nil
}

// ============================================================================
// BGP device-level
// ============================================================================

// ConfigureBGP configures BGP globals on a device using its profile.
func (net *Network) ConfigureBGP(ctx context.Context, req ConfigureBGPRequest, opts ExecOpts) (*WriteResult, error) {
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.ConfigureBGP(ctx)
	})
}

// RemoveBGPGlobals removes BGP globals from a device.
func (net *Network) RemoveBGPGlobals(ctx context.Context, req RemoveBGPGlobalsRequest, opts ExecOpts) (*WriteResult, error) {
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveBGPGlobals(ctx)
	})
}

// ============================================================================
// Loopback
// ============================================================================

// ConfigureLoopback configures the loopback interface using the device's profile.
func (net *Network) ConfigureLoopback(ctx context.Context, req ConfigureLoopbackRequest, opts ExecOpts) (*WriteResult, error) {
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.ConfigureLoopback(ctx)
	})
}

// RemoveLoopback removes the loopback interface configuration from a device.
func (net *Network) RemoveLoopback(ctx context.Context, req RemoveLoopbackRequest, opts ExecOpts) (*WriteResult, error) {
	n, err := net.Connect(ctx, req.Device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.Execute(ctx, opts, func(ctx context.Context) error {
		return n.RemoveLoopback(ctx)
	})
}

// ============================================================================
// Read methods
// ============================================================================

// ShowDevice returns structured device info for the named device.
func (net *Network) ShowDevice(ctx context.Context, device string) (*DeviceInfo, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.DeviceInfo()
}

// ListInterfaceDetails returns summary info for all interfaces on a device.
func (net *Network) ListInterfaceDetails(ctx context.Context, device string) ([]InterfaceSummary, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ListInterfaceDetails()
}

// ListInterfaces returns all interface names on a device.
func (net *Network) ListInterfaces(ctx context.Context, device string) ([]string, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ListInterfaces(), nil
}

// ShowInterfaceDetail returns all properties of a single interface on a device.
func (net *Network) ShowInterfaceDetail(ctx context.Context, device, name string) (*InterfaceDetail, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ShowInterfaceDetail(name)
}

// GetInterfaceProperty returns a single property of an interface on a device.
func (net *Network) GetInterfaceProperty(ctx context.Context, device, name, property string) (string, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return "", err
	}
	defer n.Close()
	return n.GetInterfaceProperty(name, property)
}

// ListVLANs returns all VLAN IDs on a device.
func (net *Network) ListVLANs(ctx context.Context, device string) ([]int, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ListVLANs(), nil
}

// ShowVLAN returns VLAN info for a given VLAN ID on a device.
func (net *Network) ShowVLAN(ctx context.Context, device string, id int) (*VLANStatusEntry, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ShowVLAN(id)
}

// VLANStatus returns all VLANs with summary details on a device.
func (net *Network) VLANStatus(ctx context.Context, device string) ([]VLANStatusEntry, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.VLANStatus()
}

// ListVRFs returns all VRF names on a device.
func (net *Network) ListVRFs(ctx context.Context, device string) ([]string, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ListVRFs(), nil
}

// ShowVRF returns VRF info including BGP neighbors from CONFIG_DB on a device.
func (net *Network) ShowVRF(ctx context.Context, device, name string) (*VRFDetail, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ShowVRF(name)
}

// VRFStatus returns all VRFs with operational state on a device.
func (net *Network) VRFStatus(ctx context.Context, device string) ([]VRFStatusEntry, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.VRFStatus()
}

// ListACLs returns all ACL tables with summary info on a device.
func (net *Network) ListACLs(ctx context.Context, device string) ([]ACLTableSummary, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ListACLs()
}

// ShowACL returns an ACL table with all its rules on a device.
func (net *Network) ShowACL(ctx context.Context, device, name string) (*ACLTableDetail, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ShowACL(name)
}

// GetOrphanedACLs returns ACL table names that are not bound to any interface on a device.
func (net *Network) GetOrphanedACLs(ctx context.Context, device string) ([]string, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.GetOrphanedACLs(), nil
}

// BGPStatus returns comprehensive BGP status (config + operational state) for a device.
func (net *Network) BGPStatus(ctx context.Context, device string) (*BGPStatusResult, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.BGPStatus()
}

// EVPNStatus returns comprehensive EVPN status (config + operational state) for a device.
func (net *Network) EVPNStatus(ctx context.Context, device string) (*EVPNStatusResult, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.EVPNStatus()
}

// VTEPExists checks if a VTEP is configured on a device.
func (net *Network) VTEPExists(ctx context.Context, device string) (bool, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return false, err
	}
	defer n.Close()
	return n.VTEPExists(), nil
}

// ListLAGs returns all PortChannel names on a device.
func (net *Network) ListLAGs(ctx context.Context, device string) ([]string, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ListPortChannels(), nil
}

// ShowLAGDetail returns LAG info including interface MTU on a device.
func (net *Network) ShowLAGDetail(ctx context.Context, device, name string) (*LAGStatusEntry, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.ShowLAGDetail(name)
}

// LAGStatus returns all PortChannels with operational state on a device.
func (net *Network) LAGStatus(ctx context.Context, device string) ([]LAGStatusEntry, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.LAGStatus()
}

// GetServiceBinding returns the service name bound to an interface (empty if none).
func (net *Network) GetServiceBinding(ctx context.Context, device, iface string) (string, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return "", err
	}
	defer n.Close()
	return n.GetServiceBinding(iface)
}

// GetServiceBindingDetail returns the full service binding (name, IPs, VRF) for an interface.
func (net *Network) GetServiceBindingDetail(ctx context.Context, device, iface string) (*ServiceBindingDetail, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.GetServiceBindingDetail(iface)
}

// HealthCheck runs topology-driven health checks on a device.
// Requires the Network to have a loaded topology.
func (net *Network) HealthCheck(ctx context.Context, device string) (*HealthReport, error) {
	n, err := net.Connect(ctx, device)
	if err != nil {
		return nil, err
	}
	defer n.Close()
	return n.HealthCheck(ctx)
}
