# RCA-018: APP_DB route key format differs from documented convention

## Symptom

`verify-route` test step with `source: app_db` failed to find BGP-learned
loopback routes (e.g., 10.0.0.11/32) on SONiC VPP, despite the routes being
present in FRR and in the APP_DB itself.

## Root Cause

Two key format issues in `AppDBClient.GetRoute()`:

1. **Default VRF prefix**: The code constructed keys as
   `ROUTE_TABLE:default:<prefix>`, but SONiC APP_DB stores default VRF routes
   without a VRF segment: `ROUTE_TABLE:<prefix>`. Non-default VRFs use
   `ROUTE_TABLE:<vrf>:<prefix>`.

2. **Host route /32 suffix**: fpmsyncd sometimes stores host routes without the
   `/32` mask (e.g., `ROUTE_TABLE:10.0.0.11` instead of
   `ROUTE_TABLE:10.0.0.11/32`), while subnet routes always include the mask
   (e.g., `ROUTE_TABLE:10.1.0.0/31`).

## Impact

- `verify-route` with `source: app_db` failed for all host routes in default VRF
- `route-propagation` test scenario always failed
- `ping-loopback` was skipped (depends on route-propagation)

## Fix

Rewrote `AppDBClient.GetRoute()` in `pkg/newtron/device/sonic/appldb.go`:

1. Default VRF key: `ROUTE_TABLE:<prefix>` (no VRF segment)
2. Non-default VRF key: `ROUTE_TABLE:<vrf>:<prefix>`
3. Fallback: if prefix ends with `/32` and lookup fails, retry without the mask

```go
func (c *AppDBClient) getRouteHash(vrf, prefix string) (map[string]string, error) {
    var key string
    if vrf == "" || vrf == "default" {
        key = "ROUTE_TABLE:" + prefix
    } else {
        key = fmt.Sprintf("ROUTE_TABLE:%s:%s", vrf, prefix)
    }
    return c.client.HGetAll(c.ctx, key).Result()
}
```

## Lesson

SONiC's Redis key conventions vary by database and VRF type:
- CONFIG_DB uses pipe separator: `TABLE|key`
- APP_DB uses colon separator: `TABLE:key`
- Default VRF is implicit (no segment) in APP_DB, explicit in CONFIG_DB
- Host routes may omit /32 in APP_DB depending on the fpmsyncd version
