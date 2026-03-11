package newtron

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// ============================================================================
// Profiles
// ============================================================================

// ListProfiles returns the names of all device profiles.
func (net *Network) ListProfiles() []string {
	return net.internal.ListProfiles()
}

// ShowProfile returns the profile for a given device name, converted to DeviceProfileDetail.
func (net *Network) ShowProfile(name string) (*DeviceProfileDetail, error) {
	p, err := net.internal.GetProfile(name)
	if err != nil {
		return nil, err
	}
	return convertProfileDetail(name, p), nil
}

// CreateProfile creates a new device profile.
func (net *Network) CreateProfile(req CreateDeviceProfileRequest, opts ExecOpts) error {
	if req.MgmtIP == "" {
		return &ValidationError{Field: "mgmt_ip", Message: "required"}
	}
	if req.Zone == "" {
		return &ValidationError{Field: "zone", Message: "required"}
	}
	if _, err := net.internal.GetProfile(req.Name); err == nil {
		return fmt.Errorf("profile '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	profile := &spec.DeviceProfile{
		MgmtIP:      req.MgmtIP,
		LoopbackIP:  req.LoopbackIP,
		Zone:         req.Zone,
		Platform:     req.Platform,
		MAC:          req.MAC,
		UnderlayASN:  req.UnderlayASN,
		SSHUser:      req.SSHUser,
		SSHPass:      req.SSHPass,
		SSHPort:      req.SSHPort,
	}
	if req.EVPN != nil {
		profile.EVPN = &spec.EVPNConfig{
			Peers:          req.EVPN.Peers,
			RouteReflector: req.EVPN.RouteReflector,
			ClusterID:      req.EVPN.ClusterID,
		}
	}
	return net.internal.SaveProfile(req.Name, profile)
}

// DeleteProfile removes a device profile.
func (net *Network) DeleteProfile(name string, opts ExecOpts) error {
	if _, err := net.internal.GetProfile(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteProfile(name)
}

// ============================================================================
// Zones
// ============================================================================

// ListZones returns all zone names.
func (net *Network) ListZones() []string {
	return net.internal.ListZones()
}

// ShowZone returns the zone spec for a given name, converted to ZoneDetail.
func (net *Network) ShowZone(name string) (*ZoneDetail, error) {
	_, err := net.internal.GetZone(name)
	if err != nil {
		return nil, err
	}
	return &ZoneDetail{Name: name}, nil
}

// CreateZone creates a new zone.
func (net *Network) CreateZone(req CreateZoneRequest, opts ExecOpts) error {
	if req.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	if _, err := net.internal.GetZone(req.Name); err == nil {
		return fmt.Errorf("zone '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	zone := &spec.ZoneSpec{}
	return net.internal.SaveZone(req.Name, zone)
}

// DeleteZone removes a zone.
func (net *Network) DeleteZone(name string, opts ExecOpts) error {
	if _, err := net.internal.GetZone(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteZone(name)
}

// ============================================================================
// Conversion helpers
// ============================================================================

func convertProfileDetail(name string, p *spec.DeviceProfile) *DeviceProfileDetail {
	detail := &DeviceProfileDetail{
		Name:        name,
		MgmtIP:      p.MgmtIP,
		LoopbackIP:  p.LoopbackIP,
		Zone:         p.Zone,
		Platform:     p.Platform,
		MAC:          p.MAC,
		UnderlayASN:  p.UnderlayASN,
		SSHUser:      p.SSHUser,
		SSHPort:      p.SSHPort,
	}
	if p.EVPN != nil {
		detail.EVPN = &EVPNDetail{
			Peers:          p.EVPN.Peers,
			RouteReflector: p.EVPN.RouteReflector,
			ClusterID:      p.EVPN.ClusterID,
		}
	}
	return detail
}
