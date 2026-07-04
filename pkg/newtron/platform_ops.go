package newtron

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ListPlatforms returns the platforms by name (§46). Matches the
// shape sibling list endpoints emit (e.g. ListIPVPNs returns the
// same name→spec map shape) — wire consumers read details directly
// without unwrapping a file-format envelope. The previous return
// shape leaked the on-disk PlatformSpecFile.Version field, which
// has no domain meaning on the wire and arrived empty besides.
func (net *Network) ListPlatforms() map[string]*spec.PlatformSpec {
	return net.internal.Platforms()
}

// ShowPlatform returns the canonical PlatformSpec (§46) for a single
// platform. Returns *NotFoundError → HTTP 404 when no platform with
// that name exists (consistent with the convention #173 introduces
// for create-platform / update-platform / delete-platform; the
// internal GetPlatform returns a generic error string which would
// otherwise surface as 500).
func (net *Network) ShowPlatform(name string) (*spec.PlatformSpec, error) {
	p, err := net.internal.GetPlatform(name)
	if err != nil {
		return nil, &NotFoundError{Resource: "platform", Name: name}
	}
	return p, nil
}

// PlatformPortDefaults returns the default TopologyNode.Ports authoring
// template for a platform — every front-panel port in the platform inventory,
// keyed by name, carrying newtron's default port-config convention (§27:
// newtron owns the convention so an authoring client fills a device's ports
// without embedding SONiC knowledge; #301). Directly assignable to a
// TopologyNode's Ports. Returns *NotFoundError → 404 for an unknown platform,
// and a non-nil empty map for a platform with no port inventory (host /
// HWSKU-less).
func (net *Network) PlatformPortDefaults(name string) (map[string]*spec.PortConfig, error) {
	p, err := net.ShowPlatform(name)
	if err != nil {
		return nil, err
	}
	return spec.DefaultPortConfig(p), nil
}

// GetAllFeatures returns all known feature names from the dependency map.
func (net *Network) GetAllFeatures() []string {
	return spec.GetAllFeatures()
}

// GetFeatureDependencies returns the list of features that the given feature depends on.
func (net *Network) GetFeatureDependencies(feature string) []string {
	return spec.GetFeatureDependencies(feature)
}

// GetUnsupportedDueTo returns all features that are unsupported due to the
// named base feature being missing (e.g., everything that requires evpn-vxlan).
func (net *Network) GetUnsupportedDueTo(feature string) []string {
	return spec.GetUnsupportedDueTo(feature)
}

// PlatformSupportsFeature returns true if the named platform supports the named feature.
// Returns false if the platform is not found.
func (net *Network) PlatformSupportsFeature(platform, feature string) bool {
	p, err := net.internal.GetPlatform(platform)
	if err != nil {
		return false
	}
	return p.SupportsFeature(feature)
}


// Platform CRUD is removed: platforms are a global registry loaded
// once at newt-server startup from --platforms-base. Operators
// author platforms by editing files under that directory directly
// and restarting (or via --spec-watch in a future iteration).
//
// This matches PlatformSpec's existing schema-metadata claim that
// platforms are read-only via the universal UI — adding a platform
// requires backend coordination, not a wire-API call.
