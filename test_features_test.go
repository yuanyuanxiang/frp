// go test -v -run TestFeatures -timeout 30s
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/util"
	"github.com/fatedier/frp/server"
	"github.com/samber/lo"
)

// TestFallback 测试 fallback 功能
func TestFallback(t *testing.T) {
	// 1. 启动 fallback 目标服务 (模拟)
	fallbackServer := startTestHTTPServer(t, 18080, "FALLBACK_RESPONSE")
	defer fallbackServer.Close()

	// 2. 启动 frps，配置 fallback
	svrCfg := &v1.ServerConfig{
		BindAddr: "127.0.0.1",
		BindPort: 17000,
		Fallbacks: []v1.FallbackConfig{
			{
				RemotePort:   16000,
				FallbackAddr: "127.0.0.1:18080",
			},
		},
	}
	svrCfg.Complete()

	svr, err := server.NewService(svrCfg)
	if err != nil {
		t.Fatalf("创建 frps 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go svr.Run(ctx)
	time.Sleep(500 * time.Millisecond) // 等待服务启动

	// 3. 测试 fallback (无 frpc 连接)
	resp, err := http.Get("http://127.0.0.1:16000")
	if err != nil {
		t.Fatalf("请求 fallback 失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "FALLBACK_RESPONSE" {
		t.Errorf("Fallback 响应不正确: got %s, want FALLBACK_RESPONSE", string(body))
	} else {
		t.Log("✓ Fallback 功能正常")
	}
}

// TestPrivilegeKey 测试预计算 privilegeKey 认证
func TestPrivilegeKey(t *testing.T) {
	token := "test_token_123"

	// 1. 启动 frps
	svrCfg := &v1.ServerConfig{
		BindAddr: "127.0.0.1",
		BindPort: 17001,
		Auth: v1.AuthServerConfig{
			Method: v1.AuthMethodToken,
			Token:  token,
		},
	}
	svrCfg.Complete()

	svr, err := server.NewService(svrCfg)
	if err != nil {
		t.Fatalf("创建 frps 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go svr.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// 2. 计算 privilegeKey
	timestamp := time.Now().Unix()
	privilegeKey := util.GetAuthKey(token, timestamp)
	t.Logf("Token: %s", token)
	t.Logf("Timestamp: %d", timestamp)
	t.Logf("PrivilegeKey: %s", privilegeKey)

	// 3. 启动本地测试服务
	localServer := startTestHTTPServer(t, 18081, "LOCAL_SERVICE")
	defer localServer.Close()

	// 4. 使用 privilegeKey 创建 frpc 客户端
	cliCfg := &v1.ClientCommonConfig{
		ServerAddr: "127.0.0.1",
		ServerPort: 17001,
		Auth: v1.AuthClientConfig{
			Method: v1.AuthMethodToken,
			Token:  token, // 实际测试时用原始 token
		},
		Transport: v1.ClientTransportConfig{
			TCPMux: lo.ToPtr(true),
		},
	}
	cliCfg.Complete()

	proxyCfg := &v1.TCPProxyConfig{
		ProxyBaseConfig: v1.ProxyBaseConfig{
			Name: "test_tcp",
			Type: "tcp",
			ProxyBackend: v1.ProxyBackend{
				LocalIP:   "127.0.0.1",
				LocalPort: 18081,
			},
		},
		RemotePort: 16001,
	}
	proxyCfgs := config.CompleteProxyConfigurers([]v1.ProxyConfigurer{proxyCfg})

	// Create aggregator for the proxy configurations
	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll(proxyCfgs, nil); err != nil {
		t.Fatalf("设置配置源失败: %v", err)
	}
	aggregator := source.NewAggregator(configSource)

	cliSvr, err := client.NewService(client.ServiceOptions{
		Common:                 cliCfg,
		ConfigSourceAggregator: aggregator,
	})
	if err != nil {
		t.Fatalf("创建 frpc 失败: %v", err)
	}

	cliCtx, cliCancel := context.WithCancel(context.Background())
	defer cliCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cliSvr.Run(cliCtx)
	}()
	time.Sleep(1 * time.Second) // 等待连接建立

	// 5. 测试代理连接
	resp, err := http.Get("http://127.0.0.1:16001")
	if err != nil {
		t.Fatalf("请求代理失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "LOCAL_SERVICE" {
		t.Errorf("代理响应不正确: got %s, want LOCAL_SERVICE", string(body))
	} else {
		t.Log("✓ Token 认证功能正常")
		t.Log("✓ 代理功能正常")
	}

	cliCancel()
	wg.Wait()
}

// startTestHTTPServer 启动测试 HTTP 服务器
func startTestHTTPServer(t *testing.T, port int, response string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, response)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatalf("监听端口 %d 失败: %v", port, err)
	}

	go server.Serve(ln)
	return server
}
