// Package device — APP_DB client for route observation (Redis DB 0).
// APP_DB contains application-level state written by SONiC daemons. For route
// verification, newtron reads ROUTE_TABLE entries written by fpmsyncd.
package device

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
)

// AppDBRouteEntry represents a route in APP_DB's ROUTE_TABLE.
// Multi-path (ECMP) routes use comma-separated values in nexthop and ifname.
type AppDBRouteEntry struct {
	NextHop   string `json:"nexthop"`  // "10.0.0.1" or "10.0.0.1,10.0.0.3" (ECMP)
	Interface string `json:"ifname"`   // "Ethernet0" or "Ethernet0,Ethernet4" (ECMP)
	Protocol  string `json:"protocol"` // "bgp", "connected", "static"
}

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

// GetRoute reads a single route from ROUTE_TABLE by VRF and prefix.
// Returns nil (not error) if the prefix does not exist.
// Parses comma-separated nexthop/ifname into []NextHop.
//
// APP_DB key format: ROUTE_TABLE:<vrf>:<prefix>
// Note: APP_DB uses colon separator, unlike CONFIG_DB/STATE_DB which use pipe.
func (c *AppDBClient) GetRoute(vrf, prefix string) (*RouteEntry, error) {
	key := fmt.Sprintf("ROUTE_TABLE:%s:%s", vrf, prefix)
	vals, err := c.client.HGetAll(c.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("reading APP_DB route %s: %w", key, err)
	}
	if len(vals) == 0 {
		return nil, nil
	}

	entry := &RouteEntry{
		Prefix:   prefix,
		VRF:      vrf,
		Protocol: vals["protocol"],
		Source:   RouteSourceAppDB,
	}

	// Parse comma-separated ECMP next-hops
	nexthops := strings.Split(vals["nexthop"], ",")
	interfaces := strings.Split(vals["ifname"], ",")
	for i, nh := range nexthops {
		hop := NextHop{IP: strings.TrimSpace(nh)}
		if i < len(interfaces) {
			hop.Interface = strings.TrimSpace(interfaces[i])
		}
		entry.NextHops = append(entry.NextHops, hop)
	}

	return entry, nil
}
