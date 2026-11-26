package timing

import "time"

// Clock 为 V0.6 §14.2 的采样和 §14.5 的重试提供统一时钟。
type Clock interface {
	Now() time.Time
	NewTimerAt(time.Time) Timer
}

// Timer 封装一次定时通知，使调度和资源测试可按附录 E §E.5 控制时间推进。
type Timer interface {
	C() <-chan time.Time
	Stop()
}

// System 通过 Go 系统时钟实现生产环境的采样和定时。
type System struct{}

// Now 返回系统当前时间。
func (System) Now() time.Time { return time.Now() }

// NewTimerAt 创建到指定时间触发的定时器。
func (System) NewTimerAt(at time.Time) Timer {
	return systemTimer{time.NewTimer(time.Until(at))}
}

// systemTimer 将标准库定时器适配为采样和调度共用的 Timer。
type systemTimer struct{ *time.Timer }

// C 返回定时器的通知通道。
func (t systemTimer) C() <-chan time.Time { return t.Timer.C }

// Stop 停止定时器，释放尚未触发的定时任务。
func (t systemTimer) Stop() { t.Timer.Stop() }
