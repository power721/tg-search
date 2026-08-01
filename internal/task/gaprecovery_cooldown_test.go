package task

import (
	"testing"
	"time"
)

func TestGapRecoveryCooldown(t *testing.T) {
	const window = 90 * time.Second
	c := NewGapRecoveryCooldown(window)
	if got := c.Window(); got != window {
		t.Fatalf("Window() = %v, want %v", got, window)
	}

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	// Unknown channel is not cooling down.
	if c.IsCoolingDown(7, now) {
		t.Fatal("expected channel 7 not cooling down before any failure")
	}

	// After a failure the channel cools down.
	c.RecordFailure(7, now)
	if !c.IsCoolingDown(7, now.Add(window-1)) {
		t.Fatal("expected channel 7 cooling down within the window")
	}

	// Exactly one window after the failure the cooldown lifts.
	if c.IsCoolingDown(7, now.Add(window)) {
		t.Fatal("expected channel 7 cooldown lifted after the window elapsed")
	}

	// Once lifted, the stale entry is gone (pruned on read).
	if _, ok := c.failures[7]; ok {
		t.Fatal("expected stale failure entry to be pruned")
	}
}

func TestGapRecoveryCooldownSuccessClears(t *testing.T) {
	c := NewGapRecoveryCooldown(time.Minute)
	now := time.Now().UTC()
	c.RecordFailure(3, now)
	if !c.IsCoolingDown(3, now) {
		t.Fatal("expected channel 3 cooling down after failure")
	}
	c.RecordSuccess(3)
	if c.IsCoolingDown(3, now) {
		t.Fatal("expected success to clear the cooldown")
	}
}

func TestGapRecoveryCooldownFailureExtendsWindow(t *testing.T) {
	c := NewGapRecoveryCooldown(time.Minute)
	t0 := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	c.RecordFailure(5, t0)
	// A second failure closer to the end of the window should reset the timer.
	t1 := t0.Add(50 * time.Second)
	c.RecordFailure(5, t1)
	// At t0+70s the original window would have expired (60s), but the second
	// failure at t0+50s keeps it cooling until t0+110s.
	if !c.IsCoolingDown(5, t0.Add(70*time.Second)) {
		t.Fatal("expected second failure to extend the cooldown window")
	}
	if c.IsCoolingDown(5, t1.Add(time.Minute)) {
		t.Fatal("expected cooldown to lift one window after the last failure")
	}
}

func TestGapRecoveryCooldownPrunesStaleEntries(t *testing.T) {
	c := NewGapRecoveryCooldown(time.Second)
	now := time.Now().UTC()
	// Record many channels; they are all beyond the 1s window once we advance.
	for i := int64(1); i <= 100; i++ {
		c.RecordFailure(i, now)
	}
	if len(c.failures) != 100 {
		t.Fatalf("expected 100 tracked failures, got %d", len(c.failures))
	}
	// Recording a fresh failure prunes the stale ones.
	later := now.Add(10 * time.Second)
	c.RecordFailure(1, later)
	if len(c.failures) != 1 {
		t.Fatalf("expected stale entries pruned to 1, got %d", len(c.failures))
	}
}

func TestGapRecoveryCooldownNilSafe(t *testing.T) {
	// A nil receiver must be safe so handlers can call it unconditionally.
	var c *GapRecoveryCooldown
	now := time.Now().UTC()
	c.RecordFailure(1, now)
	c.RecordSuccess(1)
	if c.IsCoolingDown(1, now) {
		t.Fatal("nil cooldown should never report cooling down")
	}
	if c.Window() != 0 {
		t.Fatalf("nil cooldown Window() = %v, want 0", c.Window())
	}
}

func TestNewGapRecoveryCooldownDefaultWindow(t *testing.T) {
	if c := NewGapRecoveryCooldown(0); c.Window() != DefaultGapRecoveryCooldown {
		t.Fatalf("non-positive window should select default, got %v", c.Window())
	}
	if c := NewGapRecoveryCooldown(-5 * time.Second); c.Window() != DefaultGapRecoveryCooldown {
		t.Fatalf("negative window should select default, got %v", c.Window())
	}
}
