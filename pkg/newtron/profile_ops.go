package newtron

import (
	"context"
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ============================================================================
// Profiles
// ============================================================================

// ListProfiles returns the names of all device profiles.
func (net *Network) ListProfiles() []string {
	return net.internal.ListProfiles()
}

// ShowProfile returns the canonical DeviceProfile (§46) for a single
// device. Consumers (CLI, newtrun, newtlab) read whatever subset of
// fields they need.
func (net *Network) ShowProfile(name string) (*spec.DeviceProfile, error) {
	return net.internal.GetProfile(name)
}

// CreateProfile creates a new device profile.
func (net *Network) CreateProfile(ctx context.Context, req CreateDeviceProfileRequest, opts ExecOpts) error {
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
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("profiles").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	profile := &spec.DeviceProfile{
		MgmtIP:      req.MgmtIP,
		LoopbackIP:  req.LoopbackIP,
		Zone:        req.Zone,
		Platform:    req.Platform,
		MAC:         req.MAC,
		UnderlayASN: req.UnderlayASN,
		SSHUser:     req.SSHUser,
		SSHPass:     req.SSHPass,
	}
	if req.EVPN != nil {
		profile.EVPN = &spec.EVPNConfig{
			Peers:          req.EVPN.Peers,
			RouteReflector: req.EVPN.RouteReflector,
			ClusterID:      req.EVPN.ClusterID,
		}
	}
	return net.internal.CreateProfile(req.Name, profile)
}

// DeleteProfile removes a device profile. force=true cascade-deletes any
// topology device that references this profile (which itself cascade-deletes
// any links wired to that device). Without force, the call returns a
// *ConflictError listing the referring topology devices.
func (net *Network) DeleteProfile(ctx context.Context, name string, opts ExecOpts, force bool) error {
	if _, err := net.internal.GetProfile(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("profiles").WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteProfile(name, force)
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
func (net *Network) CreateZone(ctx context.Context, req CreateZoneRequest, opts ExecOpts) error {
	if req.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	if _, err := net.internal.GetZone(req.Name); err == nil {
		return fmt.Errorf("zone '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("zones").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	zone := &spec.ZoneSpec{}
	return net.internal.CreateZone(req.Name, zone)
}

// DeleteZone removes a zone.
func (net *Network) DeleteZone(ctx context.Context, name string, opts ExecOpts) error {
	if _, err := net.internal.GetZone(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("zones").WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteZone(name)
}

