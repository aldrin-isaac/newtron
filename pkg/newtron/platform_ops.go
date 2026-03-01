package newtron

import "github.com/newtron-network/newtron/pkg/newtron/spec"

// ListPlatforms returns all platform definitions, converted to PlatformDetail.
func (net *Network) ListPlatforms() map[string]*PlatformDetail {
	platforms := net.internal.Platforms()
	result := make(map[string]*PlatformDetail, len(platforms))
	for name, p := range platforms {
		result[name] = convertPlatformDetail(name, p)
	}
	return result
}

// ShowPlatform returns the platform spec for a given name, converted to PlatformDetail.
func (net *Network) ShowPlatform(name string) (*PlatformDetail, error) {
	p, err := net.internal.GetPlatform(name)
	if err != nil {
		return nil, err
	}
	return convertPlatformDetail(name, p), nil
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

func convertPlatformDetail(name string, p *spec.PlatformSpec) *PlatformDetail {
	return &PlatformDetail{
		Name:                name,
		HWSKU:               p.HWSKU,
		Description:         p.Description,
		DeviceType:          p.DeviceType,
		Dataplane:           p.Dataplane,
		DefaultSpeed:        p.DefaultSpeed,
		PortCount:           p.PortCount,
		Breakouts:           p.Breakouts,
		UnsupportedFeatures: p.UnsupportedFeatures,
	}
}
