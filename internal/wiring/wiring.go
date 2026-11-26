// Package wiring 保存进程共用的触发运行时。
package wiring

import (
	"errors"
	"sync/atomic"

	"triggerEngine/internal/engine"
)

var instance atomic.Pointer[engine.Runtime]

// Install 注册宿主程序组装的运行时。应在首次调用公共接口前完成注册。
func Install(r *engine.Runtime) error {
	if r == nil || !instance.CompareAndSwap(nil, r) {
		return errors.New("scheduler runtime already initialized or missing")
	}
	return nil
}

// Instance 获取进程已注册的运行时，必要时建立默认实例。
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
