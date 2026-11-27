package main

import (
	"fmt"
	"slices"
	"unsafe"

	"triggerEngine/api"
)

// initialize 是上游接入入口，通过公共 API 逐份注册 App。
func initialize(paths ...string) error {
	// 一份配置注册一个 App；全部成功后才能发送业务通知。
	for _, path := range paths {
		if err := api.TriggerInit(path); err != nil {
			return fmt.Errorf("initialize %s: %w", path, err)
		}
		fmt.Println("init", path)
	}

	return nil
}

const session = "session-001"

type batch struct {
	values []int
}

type batchKey struct {
	app, bucket, key string
}

type committedBatch struct {
	session string
	batch   *batch
}

// demoSDK 模拟 SDK 的提交和按 App 读取。示例串行消费队列，整个会话内保留所有批次。
type demoSDK map[batchKey]committedBatch

func (s demoSDK) commit(key batchKey, sessionID string, values []int, source api.FunctionIdentity) error {
	if _, exists := s[key]; exists {
		return fmt.Errorf("batch already committed: %+v", key)
	}
	// 先发布完整批次，再通知。复制切片以保证调用方后续修改不会影响已提交数据。
	data := &batch{values: slices.Clone(values)}
	s[key] = committedBatch{session: sessionID, batch: data}
	event := api.DataReadyEvent{
		AppName: key.app, Bucket: key.bucket, Key: key.key,
		SessionID: sessionID, DataAddr: unsafe.Pointer(data),
	}
	if err := api.EventTriggerNotify(event, source); err != nil {
		return fmt.Errorf("notify %+v: %w", key, err)
	}
	return nil
}

func (s demoSDK) read(key batchKey, sessionID string) ([]int, error) {
	record, ok := s[key]
	if !ok || record.session != sessionID {
		return nil, fmt.Errorf("batch not found for session %s: %+v", sessionID, key)
	}
	// 跨 App 读取发生在业务函数与 SDK 之间。返回副本以保护原批次。
	return slices.Clone(record.batch.values), nil
}
