package redisx

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Publish sends a binary payload to the given Redis pub/sub channel.
// Used by the API to fan-out SSE events to all connected instances.
func Publish(ctx context.Context, c *redis.Client, channel string, payload []byte) error {
	if GracefulDegrade.Load() == 1 {
		// Drop the message silently; SSE clients will miss this event but the
		// platform degrades gracefully rather than blocking the request.
		return nil
	}
	if err := c.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("redisx: publish to %q: %w", channel, err)
	}
	return nil
}

// Subscribe returns a channel that delivers messages matching the given
// pattern (e.g. "chan:kitchen:*"). The channel is closed when ctx is done.
//
// Callers are responsible for consuming the channel promptly; slow consumers
// will be dropped by the go-redis internal buffer.
func Subscribe(ctx context.Context, c *redis.Client, pattern string) <-chan *redis.Message {
	out := make(chan *redis.Message, 64)
	ps := c.PSubscribe(ctx, pattern)

	go func() {
		defer close(out)
		defer ps.Close() //nolint:errcheck
		ch := ps.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- msg:
				default:
					// Slow consumer — drop oldest and continue to avoid blocking.
				}
			}
		}
	}()
	return out
}
