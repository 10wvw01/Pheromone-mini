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

// fresh 判断采样时间是否处于允许使用的时间范围。
func fresh(now, at time.Time) bool {
	age := now.Sub(at)
	return !at.IsZero() && age >= 0 && age <= sampleMaxAge
}

type sampleCache[T any] struct {
	mu      sync.Mutex
	value   T
	time    time.Time
	refresh chan struct{}
}

// get 返回仍有效的采样结果，或协调多个调用共享一次重新采样。
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

// pair 按规定间隔读取两次数据，返回样本、结束时间和实际经过时间。
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
