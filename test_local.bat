@echo off
REM 本地测试脚本 - 测试 Fallback 和 Privilege 认证

echo ========================================
echo 测试环境准备
echo ========================================

REM 创建测试配置
echo 创建 frps 配置 (带 fallback)...

(
echo bindPort = 7000
echo auth.token = "test123"
echo.
echo [[fallbacks]]
echo remotePort = 6000
echo fallbackAddr = "127.0.0.1:8080"
) > test_frps.toml

echo 创建 frpc 配置...
(
echo serverAddr = "127.0.0.1"
echo serverPort = 7000
echo auth.token = "test123"
echo.
echo [[proxies]]
echo name = "tcp_test"
echo type = "tcp"
echo localIP = "127.0.0.1"
echo localPort = 3389
echo remotePort = 6000
) > test_frpc.toml

echo.
echo ========================================
echo 测试步骤:
echo ========================================
echo.
echo 1. 启动一个简单的 HTTP 服务作为 fallback 目标 (端口 8080):
echo    python -m http.server 8080
echo    或者: npx http-server -p 8080
echo.
echo 2. 启动 frps:
echo    bin\frps.exe -c test_frps.toml
echo    (如果用DLL，需要写调用程序)
echo.
echo 3. 测试 Fallback (无 frpc 连接时):
echo    curl http://127.0.0.1:6000
echo    应该看到 fallback 服务器的响应
echo.
echo 4. 启动 frpc:
echo    bin\frpc.exe -c test_frpc.toml
echo.
echo 5. 测试正常代理 (有 frpc 连接时):
echo    连接 127.0.0.1:6000 应该转发到 frpc 的 localPort
echo.
echo ========================================
pause
