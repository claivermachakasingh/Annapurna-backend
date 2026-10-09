package redisx

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// dangerZoneKey returns the Redis hash key for a batch's danger-zone state.
// Format: dz:{batchID}
func dangerZoneKey(batchID string) string {
	return "dz:" + batchID
}

// incrDangerZoneScript atomically increments the "minutes" field in the batch
// hash and caps the total at 15 minutes (the hard safety limit).
//
// KEYS[1] = hash key  (e.g. dz:abc123)
// ARGV[1] = delta     (float, may be negative for corrections)
// ARGV[2] = cap value (15.0)
//
// Returns the new total as a bulk string.
const incrDangerZoneScript = `
local current = tonumber(redis.call("HGET", KEYS[1], "minutes") or "0")
local delta   = tonumber(ARGV[1])
local cap     = tonumber(ARGV[2])
local next    = current + delta
if next > cap then next = cap end
if next < 0   then next = 0   end
redis.call("HSET",   KEYS[1], "minutes", tostring(next))
return tostring(next)
`

var incrDangerZoneLua = redis.NewScript(incrDangerZoneScript)

const dangerZoneCap = 15.0

// IncrDangerZone atomically adds deltaMinutes to the danger-zone accumulator
// for batchID, capping the result at 15 minutes. Negative deltas are allowed
// (e.g. when a batch is cooled down). The hash has no expiry by design — the
// batch lifecycle owner must call Delete when a batch is closed.
func IncrDangerZone(ctx context.Context, c *redis.Client, batchID string, deltaMinutes float64) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	key := dangerZoneKey(batchID)
	_, err := incrDangerZoneLua.Run(ctx, c, []string{key},
		strconv.FormatFloat(deltaMinutes, 'f', 4, 64),
		strconv.FormatFloat(dangerZoneCap, 'f', 4, 64),
	).Text()
	if err != nil {
		return fmt.Errorf("redisx: incr danger zone %q: %w", batchID, err)
	}
	return nil
}

// GetDangerZoneMinutes returns the accumulated danger-zone minutes for a
// batch. Returns 0.0 if the key does not exist.
func GetDangerZoneMinutes(ctx context.Context, c *redis.Client, batchID string) (float64, error) {
	if GracefulDegrade.Load() == 1 {
		return 0, nil
	}
	key := dangerZoneKey(batchID)
	val, err := c.HGet(ctx, key, "minutes").Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("redisx: get danger zone %q: %w", batchID, err)
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, fmt.Errorf("redisx: parse danger zone value %q: %w", val, err)
	}
	return f, nil
}

// SetDangerZoneLast records the timestamp of the last danger-zone event for a
// batch in the same hash. Used by the safety assessor to compute recency.
func SetDangerZoneLast(ctx context.Context, c *redis.Client, batchID string, ts time.Time) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	key := dangerZoneKey(batchID)
	return c.HSet(ctx, key, "last_ts", ts.UTC().Format(time.RFC3339)).Err()
}
