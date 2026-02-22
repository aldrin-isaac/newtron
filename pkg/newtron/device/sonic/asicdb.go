// Package device — ASIC_DB client for ASIC-level route verification (Redis DB 1).
// ASIC_DB contains SAI objects that represent what is programmed in hardware.
// Reading routes from ASIC_DB confirms data-plane programming, not just control-plane.
package sonic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"

)

// AsicDBClient wraps Redis client for ASIC_DB access (DB 1).
// More complex than AppDBClient due to SAI OID chain resolution.
type AsicDBClient struct {
	client    *redis.Client
	ctx       context.Context
	switchOID string            // cached switch OID (discovered on Connect)
	defaultVR string            // cached default Virtual Router OID
	vrfOIDs   map[string]string // VRF name → VR OID (populated on demand, cached)
}

// NewAsicDBClient creates a new ASIC_DB client.
func NewAsicDBClient(addr string) *AsicDBClient {
	return &AsicDBClient{
		client: redis.NewClient(&redis.Options{
			Addr: addr,
			DB:   1, // ASIC_DB
		}),
		ctx: context.Background(),
	}
}

// Connect establishes the Redis connection and discovers the switch and
// default VR OIDs required for all subsequent route lookups.
func (c *AsicDBClient) Connect() error {
	if err := c.client.Ping(c.ctx).Err(); err != nil {
		return fmt.Errorf("asic_db ping: %w", err)
	}

	// 1. Discover switch OID: scan for the single SAI_OBJECT_TYPE_SWITCH key
	keys, err := c.scanKeys("ASIC_STATE:SAI_OBJECT_TYPE_SWITCH:*")
	if err != nil || len(keys) == 0 {
		return fmt.Errorf("asic_db: cannot discover switch OID: %w", err)
	}
	// Key format: "ASIC_STATE:SAI_OBJECT_TYPE_SWITCH:oid:0x..."
	c.switchOID = strings.TrimPrefix(keys[0], "ASIC_STATE:SAI_OBJECT_TYPE_SWITCH:")

	// 2. Discover default VR OID from the switch object's attribute
	defaultVR, err := c.client.HGet(c.ctx, keys[0], "SAI_SWITCH_ATTR_DEFAULT_VIRTUAL_ROUTER_ID").Result()
	if err != nil {
		return fmt.Errorf("asic_db: cannot read default VR from switch: %w", err)
	}
	c.defaultVR = defaultVR

	c.vrfOIDs = map[string]string{"default": c.defaultVR}
	return nil
}

// Close closes the connection.
func (c *AsicDBClient) Close() error {
	return c.client.Close()
}

// ResolveVROID returns the VR OID for a given VRF name. Returns the cached
// value if available; otherwise performs CONFIG_DB-based discovery by scanning
// ASIC_DB route entries for a known connected prefix in the VRF.
func (c *AsicDBClient) ResolveVROID(vrfName string, configDB *ConfigDB) (string, error) {
	if oid, ok := c.vrfOIDs[vrfName]; ok {
		return oid, nil
	}

	// Find a connected prefix in this VRF from CONFIG_DB INTERFACE table
	var knownPrefix string
	for key := range configDB.Interface {
		// Look for IP binding entries (e.g., "Ethernet0|10.1.1.1/30")
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue // base entry, skip
		}
		baseName := parts[0]
		// Check if this interface belongs to the target VRF
		if base, ok := configDB.Interface[baseName]; ok && base.VRFName == vrfName {
			knownPrefix = parts[1]
			break
		}
	}
	if knownPrefix == "" {
		return "", fmt.Errorf("no connected prefix found for VRF %s in CONFIG_DB", vrfName)
	}

	// Scan ASIC_DB route entries for the known prefix and extract the VR OID
	keys, err := c.scanKeys("ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:*")
	if err != nil {
		return "", fmt.Errorf("scanning route entries: %w", err)
	}
	for _, key := range keys {
		jsonPart := strings.TrimPrefix(key, "ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:")
		var entry struct {
			Dest     string `json:"dest"`
			SwitchID string `json:"switch_id"`
			VR       string `json:"vr"`
		}
		if json.Unmarshal([]byte(jsonPart), &entry) == nil && entry.Dest == knownPrefix {
			c.vrfOIDs[vrfName] = entry.VR
			return entry.VR, nil
		}
	}
	return "", fmt.Errorf("VR OID not found for VRF %s (prefix %s not in ASIC_DB)", vrfName, knownPrefix)
}

// GetRouteASIC reads a route from ASIC_DB by resolving the SAI object chain:
// SAI_ROUTE_ENTRY -> SAI_NEXT_HOP_GROUP -> SAI_NEXT_HOP.
// Returns nil (not error) if the route is not programmed in ASIC.
// Returns RouteEntry with Source: RouteSourceAsicDB.
func (c *AsicDBClient) GetRouteASIC(vrf, prefix string, configDB *ConfigDB) (*RouteEntry, error) {
	vrOID, err := c.ResolveVROID(vrf, configDB)
	if err != nil {
		return nil, err
	}

	// Build the JSON key with canonical formatting (sorted keys, no whitespace)
	routeKey := fmt.Sprintf(`{"dest":"%s","switch_id":"%s","vr":"%s"}`, prefix, c.switchOID, vrOID)
	redisKey := "ASIC_STATE:SAI_OBJECT_TYPE_ROUTE_ENTRY:" + routeKey

	vals, err := c.client.HGetAll(c.ctx, redisKey).Result()
	if err != nil {
		return nil, fmt.Errorf("reading ASIC_DB route: %w", err)
	}
	if len(vals) == 0 {
		return nil, nil // route not programmed
	}

	// Step 1: Get the next hop ID from the route entry
	nextHopID, ok := vals["SAI_ROUTE_ENTRY_ATTR_NEXT_HOP_ID"]
	if !ok {
		return nil, nil // no next-hop (e.g., blackhole or trap)
	}

	entry := &RouteEntry{
		Prefix: prefix,
		VRF:    vrf,
		Source: RouteSourceAsicDB,
	}

	// Determine if this points to a single next-hop or a next-hop group
	nextHops, err := c.resolveNextHops(nextHopID)
	if err != nil {
		return nil, err
	}
	entry.NextHops = nextHops

	return entry, nil
}

// resolveNextHops resolves a next-hop OID to a list of NextHop entries.
// If the OID is a SAI_NEXT_HOP_GROUP, resolves all group members.
// If the OID is a SAI_NEXT_HOP directly, returns a single entry.
func (c *AsicDBClient) resolveNextHops(oid string) ([]NextHop, error) {
	// Try as single next-hop first
	nhKey := fmt.Sprintf("ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP:%s", oid)
	nhVals, err := c.client.HGetAll(c.ctx, nhKey).Result()
	if err != nil {
		return nil, fmt.Errorf("reading next hop %s: %w", oid, err)
	}
	if len(nhVals) > 0 {
		// Single next-hop
		return []NextHop{{
			IP: nhVals["SAI_NEXT_HOP_ATTR_IP"],
		}}, nil
	}

	// Try as next-hop group
	groupKey := fmt.Sprintf("ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP_GROUP:%s", oid)
	groupVals, err := c.client.HGetAll(c.ctx, groupKey).Result()
	if err != nil {
		return nil, fmt.Errorf("reading next hop group %s: %w", oid, err)
	}
	if len(groupVals) == 0 {
		return nil, nil // neither a next-hop nor a group
	}

	// Scan for group members
	memberKeys, err := c.scanKeys("ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP_GROUP_MEMBER:*")
	if err != nil {
		return nil, fmt.Errorf("scanning next hop group members: %w", err)
	}

	var nextHops []NextHop
	for _, mk := range memberKeys {
		memberVals, err := c.client.HGetAll(c.ctx, mk).Result()
		if err != nil {
			continue
		}
		// Check if this member belongs to our group
		if memberVals["SAI_NEXT_HOP_GROUP_MEMBER_ATTR_NEXT_HOP_GROUP_ID"] != oid {
			continue
		}

		// Resolve the member's next-hop OID
		memberNHOID := memberVals["SAI_NEXT_HOP_GROUP_MEMBER_ATTR_NEXT_HOP_ID"]
		if memberNHOID == "" {
			continue
		}

		memberNHKey := fmt.Sprintf("ASIC_STATE:SAI_OBJECT_TYPE_NEXT_HOP:%s", memberNHOID)
		memberNHVals, err := c.client.HGetAll(c.ctx, memberNHKey).Result()
		if err != nil {
			continue
		}

		nextHops = append(nextHops, NextHop{
			IP: memberNHVals["SAI_NEXT_HOP_ATTR_IP"],
		})
	}

	return nextHops, nil
}

// scanKeys uses SCAN to find keys matching a pattern (avoids KEYS on large databases).
func (c *AsicDBClient) scanKeys(pattern string) ([]string, error) {
	var allKeys []string
	var cursor uint64
	for {
		keys, nextCursor, err := c.client.Scan(c.ctx, cursor, pattern, 1000).Result()
		if err != nil {
			return nil, err
		}
		allKeys = append(allKeys, keys...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return allKeys, nil
}
