// Package wiring 保存进程共用的触发运行时。
package wiring

import (
	"errors"
	"sync/atomic"

	"triggerEngine/internal/engine"
)

var instance atomic.Pointer[engine.Runtime]

// Install 为内部测试安装替身运行时。须在首次公共 API 调用前执行。
func Install(r *engine.Runtime) error {
	if r == nil || !instance.CompareAndSwap(nil, r) {
		return errors.New("scheduler runtime already initialized or missing")
	}
	return nil
}

// Instance 获取 V0.6 §13.1 所述的进程级运行时，必要时建立默认实例。
func Instance() *engine.Runtime {
	if r := instance.Load(); r != nil {
		return r
	}
	r := engine.New(engine.Dependencies{})
	if instance.CompareAndSwap(nil, r) {
		return r
	}
	// 并发注册成功时关闭本次创建的备用实例。
	r.Close()
	return instance.Load()
}
