// Package testclock provides deterministic time for internal tests.
package testclock

import (
	"context"
	"sync"
	"time"

	"triggerEngine/internal/timing"
)

type Clock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*timer]time.Time
	changed chan struct{}
}

func New() *Clock {
	return &Clock{now: time.Unix(1_000, 0), timers: make(map[*timer]time.Time), changed: make(chan struct{})}
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) NewTimerAt(at time.Time) timing.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{clock: c, ch: make(chan time.Time, 1)}
	if !at.After(c.now) {
		t.ch <- c.now
	} else {
		c.timers[t] = at
		c.signal()
	}
	return t
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for t, due := range c.timers {
		if !c.now.Before(due) {
			t.ch <- c.now
			delete(c.timers, t)
		}
	}
	c.signal()
}

func (c *Clock) WaitTimer(ctx context.Context, at time.Time) error {
	for {
		c.mu.Lock()
		for _, due := range c.timers {
			if due.Equal(at) {
				c.mu.Unlock()
				return nil
			}
		}
		wait := c.changed
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Clock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *Clock) signal() {
	close(c.changed)
	c.changed = make(chan struct{})
}

type timer struct {
	clock *Clock
	ch    chan time.Time
}

func (t *timer) C() <-chan time.Time { return t.ch }

func (t *timer) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	delete(t.clock.timers, t)
	t.clock.signal()
}
