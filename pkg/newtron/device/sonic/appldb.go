// Package device — APP_DB client for route observation (Redis DB 0).
// APP_DB contains application-level state written by SONiC daemons. For route
// verification, newtron reads ROUTE_TABLE entries written by fpmsyncd.
package sonic

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"

	"github.com/newtron-network/newtron/pkg/newtron/device"
)

// AppDBClient wraps Redis client for APP_DB access (DB 0).
type AppDBClient struct {
	client *redis.Client
	ctx    context.Context
}

// NewAppDBClient creates a new APP_DB client.
func NewAppDBClient(addr string) *AppDBClient {
	return &AppDBClient{
		client: redis.NewClient(&redis.Options{
			Addr: addr,
			DB:   0, // APP_DB
		}),
		ctx: context.Background(),
	}
}

// Connect tests the connection.
func (c *AppDBClient) Connect() error {
	return c.client.Ping(c.ctx).Err()
}

// Close closes the connection.
func (c *AppDBClient) Close() error {
	return c.client.Close()
}

// getRouteHash returns the raw hash for a ROUTE_TABLE key. Default VRF
// routes are stored without a VRF segment (ROUTE_TABLE:<prefix>); non-default
// VRF routes include the VRF name (ROUTE_TABLE:<vrf>:<prefix>).
func (c *AppDBClient) getRouteHash(vrf, prefix string) (map[string]string, error) {
	var key string
	if vrf == "" || vrf == "default" {
		key = "ROUTE_TABLE:" + prefix
	} else {
		key = fmt.Sprintf("ROUTE_TABLE:%s:%s", vrf, prefix)
	}
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("reading APP_DB route %s: %w", key, err)
	}
	return vals, nil
}

// GetRoute reads a single route from ROUTE_TABLE by VRF and prefix.
// Returns nil (not error) if the prefix does not exist.
// Parses comma-separated nexthop/ifname into []device.NextHop.
//
// APP_DB key format:
//   - Default VRF: ROUTE_TABLE:<prefix>
//   - Non-default: ROUTE_TABLE:<vrf>:<prefix>
//
// fpmsyncd may omit the /32 suffix for host routes, so if the initial
// lookup fails and the prefix is a /32, a second lookup is attempted
// without the mask.
func (c *AppDBClient) GetRoute(vrf, prefix string) (*device.RouteEntry, error) {
	vals, err := c.getRouteHash(vrf, prefix)
	if err != nil {
		return nil, err
	}
	// Retry without /32 — fpmsyncd sometimes omits the mask for host routes.
	if len(vals) == 0 && strings.HasSuffix(prefix, "/32") {
		vals, err = c.getRouteHash(vrf, strings.TrimSuffix(prefix, "/32"))
		if err != nil {
			return nil, err
		}
	}
	if len(vals) == 0 {
		return nil, nil
	}

	entry := &device.RouteEntry{
		Prefix:   prefix,
		VRF:      vrf,
		Protocol: vals["protocol"],
		Source:   device.RouteSourceAppDB,
	}

	// Parse comma-separated ECMP next-hops
	nexthops := strings.Split(vals["nexthop"], ",")
	interfaces := strings.Split(vals["ifname"], ",")
	for i, nh := range nexthops {
		hop := device.NextHop{IP: strings.TrimSpace(nh)}
		if i < len(interfaces) {
			hop.Interface = strings.TrimSpace(interfaces[i])
		}
		entry.NextHops = append(entry.NextHops, hop)
	}

	return entry, nil
}
