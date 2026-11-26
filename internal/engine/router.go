package engine

import (
	"container/heap"
	"context"
	"log/slog"
	"sync"
	"time"

	"triggerEngine/internal/config"
	"triggerEngine/internal/contract"
	"triggerEngine/internal/resource"
	"triggerEngine/internal/timing"
)

type pendingTask struct {
	task          contract.TaskInvocation
	domain        config.Domain
	delay         config.Delay
	log           *slog.Logger
	firstDeferred time.Time
	deadline      time.Time
	next          time.Time
}

type delayHeap []*pendingTask

// Len 返回等待重试的任务数量。
func (h delayHeap) Len() int { return len(h) }

// Less 按下一次尝试时间排列任务，最早到期的任务排在前面。
func (h delayHeap) Less(i, j int) bool { return h[i].next.Before(h[j].next) }

// Swap 交换等待队列中两个任务的位置，供堆调整顺序。
func (h delayHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push 将任务追加到堆的底层切片，位置由容器堆调整。
func (h *delayHeap) Push(x any) { *h = append(*h, x.(*pendingTask)) }

// Pop 移除堆调整后放在切片末尾的任务，并清除原位置的引用。
func (h *delayHeap) Pop() any {
	last := len(*h) - 1
	x := (*h)[last]
	(*h)[last] = nil
	*h = (*h)[:last]
	return x
}

type router struct {
	pool  contract.TaskPool
	probe resource.Probe
	clock timing.Clock
	log   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	worker sync.WaitGroup

	mu      sync.Mutex
	started bool
	pending delayHeap
	// count 包括已从队列取出、正在锁外采集资源的任务。
	count int
	limit int
}

// newRouter 保存调度所需的依赖，并创建统一的取消信号。
func newRouter(d Dependencies) *router {
	ctx, cancel := context.WithCancel(context.Background())
	return &router{
		pool: d.Pool, probe: d.Probe, clock: d.Clock, log: d.Logger,
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), limit: d.DelayLimit,
	}
}

// submit 立即尝试提交任务，并为允许重试的任务安排等待。
func (r *router) submit(task contract.TaskInvocation, domain config.Domain, delay config.Delay, log *slog.Logger) {
	p := &pendingTask{task: task, domain: domain, delay: delay, log: log}
	if r.attempt(p) {
		r.schedule(p, false)
	}
}

// stopAttempt 判断任务是否已超过等待期限，或调度器是否正在关闭。
func (r *router) stopAttempt(p *pendingTask) bool {
	if !p.deadline.IsZero() && !r.clock.Now().Before(p.deadline) {
		p.log.Info("TASK_DELAY_EXPIRED", "delay_state", "expired", "first_deferred", p.firstDeferred, "deadline", p.deadline)
		return true
	}
	if r.ctx.Err() != nil {
		p.log.Info("TASK_CANCELLED", "delay_state", "closed")
		return true
	}
	return false
}

// attempt 采集资源并尝试把任务交给任务池。返回 true 表示还可重试。
func (r *router) attempt(p *pendingTask) bool {
	if r.stopAttempt(p) {
		return false
	}
	// 资源采集可能阻塞，采集完成后再次检查期限和取消状态。
	s, err := r.probe.Read(r.ctx, p.domain, p.task.Function)
	if r.stopAttempt(p) {
		return false
	}
	dimension := ""
	if err == nil {
		dimension, err = resource.Evaluate(p.task.Function.Resource, s)
	}
	if err != nil {
		return r.deferTask(p, "RESOURCE_PROBE_FAILED", "probe_failure_reason", resource.Reason(err), "error", err)
	}
	if dimension != "" {
		return r.deferTask(p, "RESOURCE_INSUFFICIENT", "dimension", dimension,
			"cpu_available", s.CPUAvailable, "memory_available", s.MemoryAvailable,
			"disk_available", s.DiskAvailable, "network_available", s.NetworkAvailable,
			"sample_time", s.SampleTime, "source", s.Source, "resource_request", p.task.Function.Resource)
	}
	if r.pool == nil {
		p.log.Error("POOL_ERROR", "reason", "Pool is not assembled", "delay_state", "terminated")
		return false
	}
	if r.stopAttempt(p) {
		return false
	}
	result := r.pool.TryEnqueue(p.task)
	// 只有暂时没有容量的情况可以重试。
	switch result {
	case contract.EnqueueAccepted:
		p.log.Debug("TASK_ACCEPTED", "pool_result", result, "delay_state", "accepted")
		return false
	case contract.EnqueueNoCapacity:
		if !p.delay.Enabled {
			p.log.Info("PROCESS_DISPATCH_REQUIRED", "pool_result", result, "delay_reason", "POOL_NO_CAPACITY",
				"delay_enabled", false, "delay_state", "rejected")
			return false
		}
		return r.deferTask(p, "POOL_NO_CAPACITY", "pool_result", result)
	default:
		p.log.Error("POOL_ERROR", "pool_result", result, "delay_state", "terminated")
		return false
	}
}

// deferTask 记录本次未能提交的原因，并在首轮等待时确定最终期限。
func (r *router) deferTask(p *pendingTask, reason string, fields ...any) bool {
	now := r.clock.Now()
	state := "rejected"
	if p.delay.Enabled {
		// 以首次延迟时间计算固定期限，后续重试沿用该期限。
		if p.deadline.IsZero() {
			p.firstDeferred = now
			p.deadline = now.Add(p.delay.MaxWait)
		}
		state = "deferred"
	}
	p.log.Info(reason, append(fields, "delay_reason", reason, "delay_enabled", p.delay.Enabled,
		"delay_state", state, "first_deferred", p.firstDeferred, "deadline", p.deadline)...)
	return p.delay.Enabled && !r.stopAttempt(p)
}

// schedule 安排下一次尝试。已在等待队列中的任务重试时沿用原有名额。
func (r *router) schedule(p *pendingTask, retry bool) {
	// 下一次唤醒时间不得晚于最终期限。
	p.next = r.clock.Now().Add(p.delay.RetryInterval)
	if p.deadline.Before(p.next) {
		p.next = p.deadline
	}

	r.mu.Lock()
	if r.ctx.Err() != nil {
		if retry {
			r.count--
		}
		r.mu.Unlock()
		p.log.Info("TASK_CANCELLED", "delay_state", "closed")
		return
	}
	// 首次加入队列时检查容量，正在重试的任务已经占用一个名额。
	if !retry && r.count >= r.limit {
		r.mu.Unlock()
		p.log.Info("TASK_DELAY_FULL", "delay_state", "rejected", "deadline", p.deadline)
		return
	}
	if !retry {
		r.count++
	}
	heap.Push(&r.pending, p)
	if !r.started {
		r.started = true
		r.worker.Go(r.run)
	}
	r.mu.Unlock()
	if !retry {
		// 新任务可能更早到期，通知重试协程重新检查队列。
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// run 等待下一项任务到期并重试，遇到新的调度安排时重新计算等待时间。
func (r *router) run() {
	for r.ctx.Err() == nil {
		r.mu.Lock()
		var timer timing.Timer
		var fired <-chan time.Time
		if len(r.pending) != 0 {
			next := r.pending[0].next
			if !next.After(r.clock.Now()) {
				// 在锁外采集资源和提交任务，让其他调用仍可加入队列。
				p := heap.Pop(&r.pending).(*pendingTask)
				r.mu.Unlock()
				if r.attempt(p) {
					r.schedule(p, true)
				} else {
					r.mu.Lock()
					r.count--
					r.mu.Unlock()
				}
				continue
			}
			timer = r.clock.NewTimerAt(next)
			fired = timer.C()
		}
		r.mu.Unlock()
		// 等待取消、新任务或定时器通知。
		select {
		case <-r.ctx.Done():
		case <-r.wake:
		case <-fired:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// close 取消等待中的任务，并等重试协程退出。
func (r *router) close() {
	r.mu.Lock()
	r.cancel()
	cancelled := r.pending
	r.count -= len(cancelled)
	r.pending = nil
	r.mu.Unlock()
	for _, p := range cancelled {
		p.log.Info("TASK_CANCELLED", "delay_state", "closed", "deadline", p.deadline)
	}
	r.worker.Wait()
}
