// 这个文件展示需要在 pkg/config/v1/server.go 中添加的配置字段
//
// 在 ServerConfig 结构体中添加:

/*
type ServerConfig struct {
    // ... 现有字段 ...

    // Fallback 配置，当 frpc 离线时，将端口流量转发到指定地址
    Fallbacks []FallbackConfig `json:"fallbacks,omitempty"`
}

type FallbackConfig struct {
    // 代理的远程端口
    RemotePort int `json:"remotePort"`
    // fallback 目标地址，如 "127.0.0.1:9090"
    FallbackAddr string `json:"fallbackAddr"`
}
*/

// frps.toml 配置示例:
//
// bindPort = 80
//
// [[fallbacks]]
// remotePort = 443
// fallbackAddr = "127.0.0.1:9090"
//
// [[fallbacks]]
// remotePort = 8080
// fallbackAddr = "127.0.0.1:9091"

package v1

// FallbackConfig fallback 配置
type FallbackConfig struct {
	// RemotePort 需要 fallback 的端口
	RemotePort int `json:"remotePort"`
	// FallbackAddr fallback 目标地址
	FallbackAddr string `json:"fallbackAddr"`
}
