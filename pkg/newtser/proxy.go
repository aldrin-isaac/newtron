package newtser

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	httputilpkg "github.com/aldrin-isaac/newtron/pkg/httputil"
)

// Proxy is the catch-all handler that newtser registers for every
// path not handled by its own meta-routes (/services, /health).
//
// On request:
//  1. Extract the first non-empty path segment as the service name.
//  2. Look up the registered upstream in the Registry.
//  3. If found, reverse-proxy the request unchanged (the upstream
//     already serves /<service>/<rest> — no path rewriting).
//  4. If not found, return 503 with a JSON envelope explaining what
//     the operator should check.
//
// SSE is supported transparently: net/http/httputil.ReverseProxy
// preserves chunked transfer encoding and flushes responses
// incrementally.
type Proxy struct {
	registry *Registry
	logger   *log.Logger

	// proxies caches one *httputil.ReverseProxy per upstream URL.
	// Building a fresh proxy on every request is cheap, but caching
	// keeps connection pooling intact between requests to the same
	// backend. The cache is replaced (not mutated) on registry change.
	cache *proxyCache
}

// NewProxy constructs a Proxy bound to the given registry and logger.
func NewProxy(registry *Registry, logger *log.Logger) *Proxy {
	return &Proxy{
		registry: registry,
		logger:   logger,
		cache:    newProxyCache(),
	}
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serviceName := firstPathSegment(r.URL.Path)
	if serviceName == "" {
		httputilpkg.WriteError(w, http.StatusNotFound,
			fmt.Errorf("newtser: no service in path %q", r.URL.Path))
		return
	}

	svc := p.registry.Get(serviceName)
	if svc == nil {
		httputilpkg.WriteError(w, http.StatusServiceUnavailable,
			fmt.Errorf("newtser: service %q not registered (check GET /services)", serviceName))
		return
	}

	rp, err := p.cache.get(svc.Upstream, p.logger)
	if err != nil {
		httputilpkg.WriteError(w, http.StatusInternalServerError,
			fmt.Errorf("newtser: bad upstream %q for service %q: %w", svc.Upstream, serviceName, err))
		return
	}

	rp.ServeHTTP(w, r)
}

// firstPathSegment returns the first non-empty segment of a URL path,
// or "" if the path is empty or only slashes.
func firstPathSegment(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

// proxyCache caches *httputil.ReverseProxy by upstream URL string.
// Each proxy holds its own *http.Transport; the cache ensures we
// reuse connection pools across requests rather than rebuilding the
// transport every time.
type proxyCache struct {
	cache map[string]*httputil.ReverseProxy
}

func newProxyCache() *proxyCache {
	return &proxyCache{cache: make(map[string]*httputil.ReverseProxy)}
}

func (c *proxyCache) get(upstream string, logger *log.Logger) (*httputil.ReverseProxy, error) {
	if rp, ok := c.cache[upstream]; ok {
		return rp, nil
	}
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorLog = logger
	// Preserve the full incoming path — Director defaults to host-only
	// rewriting which is what we want (the upstream serves the same
	// /<service>/<version>/... path the client sent).
	c.cache[upstream] = rp
	return rp, nil
}
