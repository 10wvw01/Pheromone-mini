package timing

import "time"

type Clock interface {
	Now() time.Time
	NewTimerAt(time.Time) Timer
}

type Timer interface {
	C() <-chan time.Time
	Stop()
}

type System struct{}

// Now 返回系统当前时间。
func (System) Now() time.Time { return time.Now() }

// NewTimerAt 创建到指定时间触发的定时器。
func (System) NewTimerAt(at time.Time) Timer {
	return systemTimer{time.NewTimer(time.Until(at))}
}

type systemTimer struct{ *time.Timer }

// C 返回定时器的通知通道。
func (t systemTimer) C() <-chan time.Time { return t.Timer.C }

// Stop 停止定时器，释放尚未触发的定时任务。
func (t systemTimer) Stop() { t.Timer.Stop() }
