package newtron

import (
	"context"
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// ListPlatforms returns the canonical PlatformSpecFile (§46). Consumers
// (CLI, newtrun, newtlab) read whatever subset of fields they need.
func (net *Network) ListPlatforms() *spec.PlatformSpecFile {
	platforms := net.internal.Platforms()
	return &spec.PlatformSpecFile{Platforms: platforms}
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


// ============================================================================
// Platform CRUD (#173)
// ============================================================================
//
// Closes the residual gap from #152 — platforms now have the same
// Create/Update/Delete verbs the other 9 spec kinds gained in that PR.
// Auth gates uniform with #152: PermSpecAuthor with WithField("platforms")
// and WithResource(name).
//
// Request shape: CreatePlatformRequest embeds spec.PlatformSpec
// directly (DPN §46 — wire shape mirrors canonical types). The request
// body for create and update is the same shape; the verb dispatches.

// CreatePlatform adds a new platform definition. Returns a *ConflictError
// when a platform with this name already exists.
func (net *Network) CreatePlatform(ctx context.Context, req CreatePlatformRequest, opts ExecOpts) error {
	if req.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	if _, err := net.internal.GetPlatform(req.Name); err == nil {
		return fmt.Errorf("platform '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("platforms").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	p := req.PlatformSpec
	return translateInternalError(net.internal.CreatePlatform(req.Name, &p))
}

// UpdatePlatform replaces an existing platform definition. Returns
// *NotFoundError → HTTP 404 when no platform with that name exists.
// Full-replacement semantics: every field on req becomes the new
// content for that name.
func (net *Network) UpdatePlatform(ctx context.Context, req CreatePlatformRequest, opts ExecOpts) error {
	if req.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("platforms").WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	p := req.PlatformSpec
	return translateInternalError(net.internal.UpdatePlatform(req.Name, &p))
}

// DeletePlatform removes a platform definition. Returns
// *ConflictError when any device profile still references this
// platform (Profile.Platform == name); the operator must retarget or
// delete those profiles first. Returns *NotFoundError → 404 if the
// platform doesn't exist.
func (net *Network) DeletePlatform(ctx context.Context, name string, opts ExecOpts) error {
	if name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	if opts.Execute {
		if err := net.checkPermission(ctx, auth.PermSpecAuthor, auth.NewContext().WithField("platforms").WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return translateInternalError(net.internal.DeletePlatform(name))
}
