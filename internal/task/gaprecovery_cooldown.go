package task

import (
	"sync"
	"time"
)

// DefaultGapRecoveryCooldown is how long a channel's gap recovery stays
// suppressed after a failure. Tuned to be short enough that a transient
// transport blip clears quickly, but long enough to stop the per-live-message
// re-enqueue storm that otherwise burns ~20 Telegram RPC retries per attempt.
const DefaultGapRecoveryCooldown = 90 * time.Second

// GapRecoveryCooldown suppresses re-enqueuing gap recovery for a channel that
// recently failed.
//
// Gap recovery is triggered on every incoming live message that leaves a gap
// behind the history cursor. Coalescing keeps at most one queued task per
// channel, but a queued task that runs and fails (flaky Telegram transport,
// CHANNEL_PRIVATE, ...) leaves the gap unfilled, so the next live message
// enqueues a fresh task that fails the same way — a network and write storm.
// Recording a failure cools the channel for Window; the first success (or the
// window elapsing) lifts the cooldown. Healthy channels never enter the
// cooldown, so normal recovery is unaffected.
//
// The failure map is pruned lazily so it cannot grow without bound.
type GapRecoveryCooldown struct {
	mu       sync.Mutex
	failures map[int64]time.Time
	window   time.Duration
}

// NewGapRecoveryCooldown returns a cooldown tracker with the given window. A
// non-positive window selects DefaultGapRecoveryCooldown.
func NewGapRecoveryCooldown(window time.Duration) *GapRecoveryCooldown {
	if window <= 0 {
		window = DefaultGapRecoveryCooldown
	}
	return &GapRecoveryCooldown{
		failures: map[int64]time.Time{},
		window:   window,
	}
}

// Window returns the active cooldown window.
func (c *GapRecoveryCooldown) Window() time.Duration {
	if c == nil {
		return 0
	}
	return c.window
}

// RecordFailure marks channelID as having failed recovery at now. Safe to call
// on a nil receiver (no-op), so handlers can call it unconditionally.
func (c *GapRecoveryCooldown) RecordFailure(channelID int64, now time.Time) {
	if c == nil || channelID <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[channelID] = now
	c.pruneLocked(now)
}

// RecordSuccess clears any cooldown for channelID.
func (c *GapRecoveryCooldown) RecordSuccess(channelID int64) {
	if c == nil || channelID <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failures, channelID)
}

// IsCoolingDown reports whether channelID is within the cooldown window after
// its most recent failure. A nil receiver or unknown channel reports false.
func (c *GapRecoveryCooldown) IsCoolingDown(channelID int64, now time.Time) bool {
	if c == nil || channelID <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.failures[channelID]
	if !ok {
		return false
	}
	if now.Sub(last) >= c.window {
		delete(c.failures, channelID)
		return false
	}
	return true
}

// pruneLocked drops entries older than the window. Caller holds c.mu.
func (c *GapRecoveryCooldown) pruneLocked(now time.Time) {
	for ch, at := range c.failures {
		if now.Sub(at) >= c.window {
			delete(c.failures, ch)
		}
	}
}
