package spec

import (
	"regexp"
	"strings"
)

var resolveStringRegexp = regexp.MustCompile(`\$\{([^}]+)\}|\$([a-zA-Z_][a-zA-Z0-9_-]*)`)

// Resolver handles alias and variable resolution in configuration
type Resolver struct {
	aliases     map[string]string
	prefixLists map[string][]string
}

// NewResolver creates a new resolver with the given alias and prefix list maps
func NewResolver(aliases map[string]string, prefixLists map[string][]string) *Resolver {
	return &Resolver{
		aliases:     aliases,
		prefixLists: prefixLists,
	}
}

// ResolveString resolves aliases in a string
// Aliases are referenced as ${alias_name} or $alias_name
func (r *Resolver) ResolveString(s string) string {
	return resolveStringRegexp.ReplaceAllStringFunc(s, func(match string) string {
		var name string
		if strings.HasPrefix(match, "${") {
			name = match[2 : len(match)-1]
		} else {
			name = match[1:]
		}

		if value, ok := r.aliases[name]; ok {
			return value
		}
		return match // Return original if not found
	})
}

// ResolvePrefixList resolves a prefix list name to its contents
func (r *Resolver) ResolvePrefixList(name string) ([]string, bool) {
	list, ok := r.prefixLists[name]
	return list, ok
}

// ExpandPrefixLists expands all prefix list references in a string slice
// If an entry starts with "@", it's treated as a prefix list reference
func (r *Resolver) ExpandPrefixLists(entries []string) []string {
	var result []string
	for _, entry := range entries {
		if strings.HasPrefix(entry, "@") {
			listName := entry[1:]
			if list, ok := r.prefixLists[listName]; ok {
				result = append(result, list...)
			} else {
				result = append(result, entry) // Keep as-is if not found
			}
		} else {
			result = append(result, entry)
		}
	}
	return result
}

// ResolveAllStrings resolves aliases in a map of strings
func (r *Resolver) ResolveAllStrings(m map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		result[k] = r.ResolveString(v)
	}
	return result
}

// MergeAliases merges additional aliases into the resolver
// Later values override earlier ones
func (r *Resolver) MergeAliases(aliases map[string]string) {
	for k, v := range aliases {
		r.aliases[k] = v
	}
}

// MergePrefixLists merges additional prefix lists
func (r *Resolver) MergePrefixLists(lists map[string][]string) {
	for k, v := range lists {
		r.prefixLists[k] = v
	}
}

// GetAlias returns an alias value
func (r *Resolver) GetAlias(name string) (string, bool) {
	v, ok := r.aliases[name]
	return v, ok
}

// SetAlias sets an alias value
func (r *Resolver) SetAlias(name, value string) {
	r.aliases[name] = value
}

// AliasContext creates a resolver with device-specific context
type AliasContext struct {
	resolver *Resolver
	device   string
	intf     string
	service  string
}

// NewAliasContext creates a new alias context for a device
func NewAliasContext(r *Resolver, device string) *AliasContext {
	ctx := &AliasContext{
		resolver: r,
		device:   device,
	}
	// Add device-specific aliases
	ctx.resolver.SetAlias("device", device)
	return ctx
}

// WithInterface adds interface context
func (c *AliasContext) WithInterface(intf string) *AliasContext {
	c.intf = intf
	c.resolver.SetAlias("interface", intf)
	return c
}

// WithService adds service context
func (c *AliasContext) WithService(service string) *AliasContext {
	c.service = service
	c.resolver.SetAlias("service", service)
	return c
}

// Resolve resolves a string with all context
func (c *AliasContext) Resolve(s string) string {
	return c.resolver.ResolveString(s)
}
