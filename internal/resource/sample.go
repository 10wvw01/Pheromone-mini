package resource

import (
	"context"
	"fmt"
	"sync"
	"time"

	"triggerEngine/internal/timing"
)

const (
	sampleInterval = 100 * time.Millisecond
	sampleMaxAge   = 200 * time.Millisecond
)

// fresh 按 V0.6 §14.2 检查采样时间，年龄超过 200 ms 或位于未来时需刷新。
func fresh(now, at time.Time) bool {
	age := now.Sub(at)
	return !at.IsZero() && age >= 0 && age <= sampleMaxAge
}

// sampleCache 按 V0.6 §14.2 共享有效结果，并合并同一资源的并发刷新。
type sampleCache[T any] struct {
	mu      sync.Mutex
	value   T
	time    time.Time
	refresh chan struct{}
}

// get 按 V0.6 §14.2 返回有效样本。缓存过期时，同一资源的并发调用共享一次读取。
func (c *sampleCache[T]) get(ctx context.Context, clock timing.Clock, read func() (T, time.Time, error)) (T, time.Time, error) {
	for {
		c.mu.Lock()
		if fresh(clock.Now(), c.time) {
			value, at := c.value, c.time
			c.mu.Unlock()
			return value, at, nil
		}
		// 第一个发现结果过期的调用负责采样，其余调用等待采样结束。
		if c.refresh == nil {
			c.refresh = make(chan struct{})
			c.mu.Unlock()
			value, at, err := read()
			c.mu.Lock()
			if err == nil {
				c.value, c.time = value, at
			}
			close(c.refresh)
			c.refresh = nil
			c.mu.Unlock()
			return value, at, err
		}
		// 等待期间释放互斥锁，并允许当前请求独立取消。
		wait := c.refresh
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			var zero T
			return zero, time.Time{}, ctx.Err()
		case <-wait:
		}
	}
}

// pair 按 V0.6 §14.2 的 100 ms 周期采样两次，以实际经过时间作为计算分母。
func pair[T any](ctx context.Context, clock timing.Clock, read func() (T, error)) (a, b T, elapsed time.Duration, at time.Time, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if a, err = read(); err != nil {
		return
	}
	// 从第一次读取完成时计时，按实际经过的时长计算资源使用率。
	start := clock.Now()
	timer := clock.NewTimerAt(start.Add(sampleInterval))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		err = ctx.Err()
		return
	case <-timer.C():
	}
	if b, err = read(); err != nil {
		return
	}
	at = clock.Now()
	elapsed = at.Sub(start)
	if elapsed <= 0 {
		err = ComputeFailure(fmt.Errorf("nonpositive sampling interval"))
	}
	return
}
