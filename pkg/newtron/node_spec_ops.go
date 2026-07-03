package newtron

import (
	"context"
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ============================================================================
// NodeSpecs
// ============================================================================

// ListNodeSpecs returns the names of all nodes.
func (net *Network) ListNodeSpecs() []string {
	return net.internal.ListNodeSpecs()
}

// ShowNodeSpec returns the canonical NodeSpec (§46) for a single
// device. Consumers (CLI, newtrun, newtlab) read whatever subset of
// fields they need.
func (net *Network) ShowNodeSpec(name string) (*spec.NodeSpec, error) {
	// Effective (inherited) login, not the authored own-value: this read feeds
	// newtlab's profile fetch and the CLI/API node view, both of which need the
	// login the device actually dials (§24). resolveSSHLogin owns the rule.
	return net.internal.EffectiveNodeSpec(name)
}

// CreateNodeSpec creates a new node spec.
func (net *Network) CreateNodeSpec(ctx context.Context, req CreateNodeSpecRequest, opts ExecOpts) error {
	if req.MgmtIP == "" {
		return &ValidationError{Field: "mgmt_ip", Message: "required"}
	}
	if req.Zone == "" {
		return &ValidationError{Field: "zone", Message: "required"}
	}
	if _, err := net.internal.GetNodeSpec(req.Name); err == nil {
		return fmt.Errorf("node spec '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("nodes").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	nodeSpec := &spec.NodeSpec{
		MgmtIP:      req.MgmtIP,
		LoopbackIP:  req.LoopbackIP,
		Zone:        req.Zone,
		Platform:    req.Platform,
		MAC:         req.MAC,
		UnderlayASN: req.UnderlayASN,
	}
	if req.EVPN != nil {
		nodeSpec.EVPN = &spec.EVPNConfig{
			Peers:          req.EVPN.Peers,
			RouteReflector: req.EVPN.RouteReflector,
			ClusterID:      req.EVPN.ClusterID,
		}
	}
	return net.internal.CreateNodeSpec(req.Name, nodeSpec)
}

// DeleteNodeSpec removes a node spec. force=true cascade-deletes any
// topology device that references this nodeSpec (which itself cascade-deletes
// any links wired to that device). Without force, the call returns a
// *ConflictError listing the referring topology devices.
func (net *Network) DeleteNodeSpec(ctx context.Context, name string, opts ExecOpts, force bool) error {
	if _, err := net.internal.GetNodeSpec(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("nodes").WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteNodeSpec(name, force)
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

// ============================================================================
// Update — full-replacement nodeSpec/zone mutation (#152)
// ============================================================================

// UpdateNodeSpec replaces an existing node spec. Returns
// *NotFoundError → HTTP 404 when no nodeSpec with that name exists.
// Same Field + Resource (and same `spec.author` gate) as
// CreateNodeSpec / DeleteNodeSpec.
func (net *Network) UpdateNodeSpec(ctx context.Context, req CreateNodeSpecRequest, opts ExecOpts) error {
	if req.MgmtIP == "" {
		return &ValidationError{Field: "mgmt_ip", Message: "required"}
	}
	if req.Zone == "" {
		return &ValidationError{Field: "zone", Message: "required"}
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("nodes").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	nodeSpec := &spec.NodeSpec{
		MgmtIP:      req.MgmtIP,
		LoopbackIP:  req.LoopbackIP,
		Zone:        req.Zone,
		Platform:    req.Platform,
		MAC:         req.MAC,
		UnderlayASN: req.UnderlayASN,
	}
	if req.EVPN != nil {
		nodeSpec.EVPN = &spec.EVPNConfig{
			Peers:          req.EVPN.Peers,
			RouteReflector: req.EVPN.RouteReflector,
			ClusterID:      req.EVPN.ClusterID,
		}
	}
	return translateInternalError(net.internal.UpdateNodeSpec(req.Name, nodeSpec))
}

// UpdateZone replaces an existing zone. ZoneSpec carries only
// OverridableSpecs (the network→zone→node hierarchical spec maps);
// CreateZoneRequest transports nothing beyond the name today, so this
// Update preserves the existing OverridableSpecs (matching the
// preservation pattern for Filter/RoutePolicy/QoSPolicy). It exists
// for symmetry with the other Update<Kind> verbs — when a future
// request shape carries zone overrides, the body-build logic below
// is where the new values flow in.
func (net *Network) UpdateZone(ctx context.Context, req CreateZoneRequest, opts ExecOpts) error {
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("zones").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	existing, err := net.internal.GetZone(req.Name)
	if err != nil {
		return err
	}
	return translateInternalError(net.internal.UpdateZone(req.Name, existing))
}
