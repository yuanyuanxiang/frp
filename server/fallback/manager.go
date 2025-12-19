// Copyright 2024 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fallback

import (
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fatedier/frp/pkg/util/log"
)

// PortFallback 管理单个端口的 fallback 状态
type PortFallback struct {
	Port         int
	BindAddr     string
	FallbackAddr string // 如 "127.0.0.1:9090"

	listener net.Listener
	closeCh  chan struct{}
	running  bool
	mu       sync.Mutex

	// 活跃连接跟踪
	activeConns map[net.Conn]struct{}
	connsMu     sync.Mutex
}

// Manager 管理所有端口的 fallback
type Manager struct {
	// 按端口号索引
	fallbacks map[int]*PortFallback
	mu        sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		fallbacks: make(map[int]*PortFallback),
	}
}

// Register 注册一个端口的 fallback 配置
// 调用时机：frps 启动时读取配置，或者 frpc 首次注册代理时
func (m *Manager) Register(port int, bindAddr, fallbackAddr string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.fallbacks[port]; exists {
		return
	}

	m.fallbacks[port] = &PortFallback{
		Port:         port,
		BindAddr:     bindAddr,
		FallbackAddr: fallbackAddr,
		running:      false,
		activeConns:  make(map[net.Conn]struct{}),
	}
	log.Infof("fallback registered for port [%d] -> [%s]", port, fallbackAddr)
}

// Unregister 取消注册
func (m *Manager) Unregister(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fb, exists := m.fallbacks[port]; exists {
		fb.Stop()
		delete(m.fallbacks, port)
		log.Infof("fallback unregistered for port [%d]", port)
	}
}

// OnProxyOnline frpc 代理上线时调用，停止 fallback
func (m *Manager) OnProxyOnline(port int) {
	m.mu.RLock()
	fb, exists := m.fallbacks[port]
	m.mu.RUnlock()

	if exists {
		fb.Stop()
	}
}

// OnProxyOffline frpc 代理离线时调用，启动 fallback
func (m *Manager) OnProxyOffline(port int) {
	m.mu.RLock()
	fb, exists := m.fallbacks[port]
	m.mu.RUnlock()

	if exists {
		fb.Start()
	}
}

// StartAll 启动所有已注册的 fallback（用于 frps 启动时）
func (m *Manager) StartAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, fb := range m.fallbacks {
		fb.Start()
	}
}

// StopAll 停止所有 fallback（用于 frps 关闭时）
func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, fb := range m.fallbacks {
		fb.Stop()
	}
}

// Close 关闭管理器
func (m *Manager) Close() {
	m.StopAll()
}

// Start 启动 fallback 监听
func (pf *PortFallback) Start() {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	if pf.running {
		return
	}

	if pf.FallbackAddr == "" {
		return
	}

	// 等待端口释放
	time.Sleep(100 * time.Millisecond)

	addr := net.JoinHostPort(pf.BindAddr, strconv.Itoa(pf.Port))

	// 重试几次，因为端口可能还没完全释放
	var ln net.Listener
	var err error
	for i := 0; i < 5; i++ {
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
		log.Debugf("fallback listen attempt %d failed: %v", i+1, err)
		time.Sleep(200 * time.Millisecond)
	}

	if err != nil {
		log.Warnf("fallback listen on [%s] error: %v", addr, err)
		return
	}

	pf.listener = ln
	pf.closeCh = make(chan struct{})
	pf.running = true

	log.Infof("fallback started: [%s] -> [%s]", addr, pf.FallbackAddr)

	go pf.acceptLoop()
}

// Stop 停止 fallback 监听
func (pf *PortFallback) Stop() {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	if !pf.running {
		return
	}

	pf.running = false
	close(pf.closeCh)

	if pf.listener != nil {
		pf.listener.Close()
		pf.listener = nil
	}

	// 关闭所有活跃连接
	pf.connsMu.Lock()
	connCount := len(pf.activeConns)
	for conn := range pf.activeConns {
		conn.Close()
	}
	pf.activeConns = make(map[net.Conn]struct{})
	pf.connsMu.Unlock()

	if connCount > 0 {
		log.Infof("fallback closed %d active connections for port [%d]", connCount, pf.Port)
	}

	log.Infof("fallback stopped for port [%d]", pf.Port)

	// 等待端口释放
	time.Sleep(100 * time.Millisecond)
}

// IsRunning 检查是否正在运行
func (pf *PortFallback) IsRunning() bool {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return pf.running
}

func (pf *PortFallback) acceptLoop() {
	for {
		conn, err := pf.listener.Accept()
		if err != nil {
			select {
			case <-pf.closeCh:
				return
			default:
				// 可能是临时错误，继续
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		go pf.handleConn(conn)
	}
}

func (pf *PortFallback) handleConn(conn net.Conn) {
	// 注册连接
	pf.connsMu.Lock()
	pf.activeConns[conn] = struct{}{}
	pf.connsMu.Unlock()

	// 函数返回时注销连接
	defer func() {
		pf.connsMu.Lock()
		delete(pf.activeConns, conn)
		pf.connsMu.Unlock()
		conn.Close()
	}()

	// 连接后端
	backend, err := net.DialTimeout("tcp", pf.FallbackAddr, 5*time.Second)
	if err != nil {
		// 后端不可用时静默失败，避免日志刷屏
		return
	}

	// 注册后端连接
	pf.connsMu.Lock()
	pf.activeConns[backend] = struct{}{}
	pf.connsMu.Unlock()

	defer func() {
		pf.connsMu.Lock()
		delete(pf.activeConns, backend)
		pf.connsMu.Unlock()
		backend.Close()
	}()

	// 双向转发，任意一方结束就关闭所有连接
	done := make(chan struct{}, 1)

	// 客户端 -> 后端
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				backend.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		select {
		case done <- struct{}{}:
		default:
		}
	}()

	// 后端 -> 客户端
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := backend.Read(buf)
			if n > 0 {
				conn.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		select {
		case done <- struct{}{}:
		default:
		}
	}()

	// 等待任意一方结束
	<-done

	// 关闭两个连接，确保另一个 goroutine 也能退出
	conn.Close()
	backend.Close()
}
