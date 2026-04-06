package main

import (
	"context"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// 模拟一个简单的阻塞服务
func mockBlockingService(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// 模拟一个长时间运行的服务（类似真实的 frps）
func mockLongRunningService(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// 模拟服务在运行
		}
	}
}

func TestRunWithStatusControl_Exit(t *testing.T) {
	status := int32(STATUS_RUN)
	statusPtr := (*int32)(unsafe.Pointer(&status))

	var wg sync.WaitGroup
	wg.Add(1)

	var result int32
	go func() {
		defer wg.Done()
		result = int32(runWithStatusControlInt32(statusPtr, mockBlockingService))
	}()

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	// 设置退出状态
	*statusPtr = STATUS_EXIT

	// 等待退出，最多 2 秒
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("Service exited successfully with result: %d", result)
		if result != 0 {
			t.Errorf("Expected result 0, got %d", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout: service did not exit within 2 seconds")
	}
}

func TestRunWithStatusControl_StopThenExit(t *testing.T) {
	status := int32(STATUS_RUN)
	statusPtr := (*int32)(unsafe.Pointer(&status))

	var wg sync.WaitGroup
	wg.Add(1)

	var result int32
	go func() {
		defer wg.Done()
		result = int32(runWithStatusControlInt32(statusPtr, mockLongRunningService))
	}()

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	// 先停止
	*statusPtr = STATUS_STOP
	time.Sleep(100 * time.Millisecond)

	// 再退出
	*statusPtr = STATUS_EXIT

	// 等待退出
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("Service exited successfully with result: %d", result)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout: service did not exit within 2 seconds")
	}
}

// runWithStatusControlInt32 是 runWithStatusControl 的 int32 版本，用于测试
func runWithStatusControlInt32(statusPtr *int32, startFn func(ctx context.Context) error) int32 {
	var (
		ctx        context.Context
		cancel     func()
		lastStatus int32 = STATUS_UNKNOWN
		wg         sync.WaitGroup
		errCh      chan error = make(chan error, 100)
	)

	for {
		select {
		case <-errCh:
			if cancel != nil {
				cancel()
				cancel = nil
			}
			wg.Wait()
			return int32(-1)
		default:
			status := *statusPtr
			if status == lastStatus {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			lastStatus = status

			switch status {
			case STATUS_RUN:
				if cancel != nil {
					break
				}
				ctx, cancel = context.WithCancel(context.Background())
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := startFn(ctx); err != nil {
						select {
						case errCh <- err:
						default:
						}
					}
				}()
			case STATUS_STOP:
				if cancel != nil {
					cancel()
					wg.Wait()
					cancel = nil
					ctx = nil
				}
			case STATUS_EXIT:
				if cancel != nil {
					cancel()
					wg.Wait()
					cancel = nil
					ctx = nil
				}
				return int32(0)
			}
		}
	}
}
