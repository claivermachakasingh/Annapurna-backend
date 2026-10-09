// Package sse provides a Server-Sent Events hub that fans out kitchen events
// over Redis pub/sub (chan:kitchen:*) so all API replicas receive every event.
// Recent events are stored in a Redis list (events:recent:{kitchen}) capped at
// 200 entries to support Last-Event-ID replay on reconnect.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sih26234/food-waste/internal/redisx"
)

const (
	recentListCap   = 200
	recentKeyPrefix = "events:recent:"
	chanPrefix      = "chan:kitchen:"
	keepAliveEvery  = 20 * time.Second
)

// Event is a single SSE event delivered to a subscriber.
type Event struct {
	ID   string    `json:"id"`
	Type string    `json:"type"`
	Data string    `json:"data"`
	TS   time.Time `json:"ts"`
}

// Hub manages per-kitchen subscriber sets and fan-out from Redis pub/sub.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string][]chan Event // kitchenID → active subscriber channels
	redis       *redis.Client
}

// NewHub creates a Hub and starts the Redis pattern-subscribe loop.
// ctx should be the application root context; cancellation shuts the hub down.
func NewHub(ctx context.Context, c *redis.Client) *Hub {
	h := &Hub{
		subscribers: make(map[string][]chan Event),
		redis:       c,
	}
	go h.fanOut(ctx)
	return h
}

// Subscribe returns a channel that will receive events for kitchenID.
// The channel is buffered (64) to avoid blocking the fan-out loop on a slow
// HTTP client. The caller is responsible for draining and closing via
// Unsubscribe or by cancelling the request context.
func (h *Hub) Subscribe(kitchenID string) chan Event {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subscribers[kitchenID] = append(h.subscribers[kitchenID], ch)
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from the hub and closes it.
func (h *Hub) Unsubscribe(kitchenID string, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subscribers[kitchenID]
	for i, s := range subs {
		if s == ch {
			h.subscribers[kitchenID] = append(subs[:i], subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// Publish emits an event to all local subscribers and pushes to Redis so
// other replicas receive it. It also appends to the recent ring-buffer list.
func (h *Hub) Publish(ctx context.Context, kitchenID string, event Event) error {
	if event.ID == "" {
		event.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if event.TS.IsZero() {
		event.TS = time.Now().UTC()
	}

	// Fan out locally.
	h.broadcast(kitchenID, event)

	// Persist to recent ring buffer.
	if err := h.appendRecent(ctx, kitchenID, event); err != nil {
		// Non-fatal; log in production.
		_ = err
	}

	// Publish to Redis for cross-replica fan-out.
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("sse: marshal event: %w", err)
	}
	channel := chanPrefix + kitchenID
	return redisx.Publish(ctx, h.redis, channel, data)
}

// fanOut subscribes to "chan:kitchen:*" and broadcasts to local subscribers.
func (h *Hub) fanOut(ctx context.Context) {
	msgCh := redisx.Subscribe(ctx, h.redis, chanPrefix+"*")
	for msg := range msgCh {
		var event Event
		if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
			continue
		}
		// Extract kitchenID from channel name.
		kitchenID := strings.TrimPrefix(msg.Channel, chanPrefix)
		h.broadcast(kitchenID, event)
	}
}

func (h *Hub) broadcast(kitchenID string, event Event) {
	h.mu.RLock()
	subs := h.subscribers[kitchenID]
	h.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// Slow consumer; drop event rather than block.
		}
	}
}

// appendRecent pushes the event JSON to the left of the ring-buffer list and
// trims to the last 200 entries.
func (h *Hub) appendRecent(ctx context.Context, kitchenID string, event Event) error {
	data, _ := json.Marshal(event)
	key := recentKeyPrefix + kitchenID
	pipe := h.redis.TxPipeline()
	pipe.LPush(ctx, key, data)
	pipe.LTrim(ctx, key, 0, recentListCap-1)
	_, err := pipe.Exec(ctx)
	return err
}

// SSEHandler returns a chi-compatible http.HandlerFunc for a kitchen's event
// stream. It reads Last-Event-ID to replay missed events and then streams live.
//
// Route example: GET /api/v1/kitchens/{kitchenID}/events
func (h *Hub) SSEHandler(w http.ResponseWriter, r *http.Request) {
	// chi URL parameter extraction.
	kitchenID := r.PathValue("kitchenID")
	if kitchenID == "" {
		// Fallback: read from query param.
		kitchenID = r.URL.Query().Get("kitchen_id")
	}
	if kitchenID == "" {
		http.Error(w, "missing kitchenID", http.StatusBadRequest)
		return
	}

	h.StreamKitchen(w, r, kitchenID)
}

// StreamKitchen streams a specific kitchen's events. Callers that have already
// authenticated the request (and therefore resolved the kitchen from the
// caller's own claims rather than from user input) should use this directly.
func (h *Hub) StreamKitchen(w http.ResponseWriter, r *http.Request, kitchenID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	// Tell the client immediately which kitchen it is attached to, so a
	// misconfigured client fails fast instead of waiting for the first event.
	writeEvent(w, Event{
		ID:   fmt.Sprintf("%d", time.Now().UnixNano()),
		Type: "connected",
		Data: fmt.Sprintf(`{"status":"connected","kitchen_id":%q}`, kitchenID),
		TS:   time.Now().UTC(),
	})
	flusher.Flush()

	// Replay recent events if Last-Event-ID is set.
	lastID := r.Header.Get("Last-Event-ID")
	if lastID != "" {
		h.replayRecent(ctx, w, flusher, kitchenID, lastID)
	}

	ch := h.Subscribe(kitchenID)
	defer h.Unsubscribe(kitchenID, ch)

	ticker := time.NewTicker(keepAliveEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(w, event)
			flusher.Flush()
		case <-ticker.C:
			// Keep-alive comment to prevent proxy/browser timeouts.
			fmt.Fprintf(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// replayRecent sends all events from the recent ring buffer that are newer
// than lastID (compared as nanosecond-timestamp strings).
func (h *Hub) replayRecent(ctx context.Context, w io.Writer, flusher http.Flusher, kitchenID, lastID string) {
	key := recentKeyPrefix + kitchenID
	items, err := h.redis.LRange(ctx, key, 0, recentListCap-1).Result()
	if err != nil {
		return
	}
	// Items are stored newest-first (LPUSH). Reverse to replay in order.
	for i := len(items) - 1; i >= 0; i-- {
		var event Event
		if err := json.Unmarshal([]byte(items[i]), &event); err != nil {
			continue
		}
		if event.ID <= lastID {
			continue
		}
		writeEvent(w, event)
		flusher.(http.Flusher).Flush()
	}
}

// writeEvent formats an Event as the SSE wire format.
func writeEvent(w io.Writer, e Event) {
	if e.ID != "" {
		fmt.Fprintf(w, "id: %s\n", e.ID)
	}
	if e.Type != "" {
		fmt.Fprintf(w, "event: %s\n", e.Type)
	}
	fmt.Fprintf(w, "data: %s\n\n", e.Data)
}
