package redisx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrLockNotHeld is returned by ReleaseLock when the token does not match —
// i.e. the lock either expired or was taken by another holder.
var ErrLockNotHeld = errors.New("redisx: lock not held or token mismatch")

// releaseLockScript atomically checks the stored token and deletes the key
// only if the token matches. This prevents a slow holder from deleting a lock
// that was legitimately acquired by a new holder after the TTL.
//
// KEYS[1] = lock key
// ARGV[1] = expected token
// Returns 1 on success, 0 if token mismatch.
const releaseLockScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`

var releaseLockLua = redis.NewScript(releaseLockScript)

// AcquireLock tries to acquire a distributed lock using SET NX PX.
//   - key   — resource identifier (e.g. "lock:batch:abc123")
//   - token — unique caller identifier (use a UUID per attempt)
//   - ttl   — lock lifetime; choose slightly longer than the critical section
//
// Returns (true, nil) on success, (false, nil) if the lock is held by another
// caller. Non-nil err indicates a Redis failure.
func AcquireLock(ctx context.Context, c *redis.Client, key, token string, ttl time.Duration) (bool, error) {
	if GracefulDegrade.Load() == 1 {
		// Fail open — allow the operation; callers should handle concurrency at
		// the DB level (optimistic locking) when Redis is degraded.
		return true, nil
	}
	ok, err := c.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redisx: acquire lock %q: %w", key, err)
	}
	return ok, nil
}

// ReleaseLock releases a lock only when the stored token matches the caller's
// token. Returns ErrLockNotHeld if another holder owns the lock.
func ReleaseLock(ctx context.Context, c *redis.Client, key, token string) error {
	if GracefulDegrade.Load() == 1 {
		return nil
	}
	res, err := releaseLockLua.Run(ctx, c, []string{key}, token).Int64()
	if err != nil {
		return fmt.Errorf("redisx: release lock %q: %w", key, err)
	}
	if res == 0 {
		return ErrLockNotHeld
	}
	return nil
}
