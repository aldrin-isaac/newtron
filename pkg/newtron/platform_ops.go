package newtron

import "github.com/aldrin-isaac/newtron/pkg/newtron/spec"

// ListPlatforms returns the canonical PlatformSpecFile (§46). Consumers
// (CLI, newtrun, newtlab) read whatever subset of fields they need.
func (net *Network) ListPlatforms() *spec.PlatformSpecFile {
	platforms := net.internal.Platforms()
	return &spec.PlatformSpecFile{Platforms: platforms}
}

// ShowPlatform returns the canonical PlatformSpec (§46) for a single
// platform.
func (net *Network) ShowPlatform(name string) (*spec.PlatformSpec, error) {
	return net.internal.GetPlatform(name)
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

