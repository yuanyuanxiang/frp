// Copyright 2018 fatedier, fatedier@gmail.com
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

package sub

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/featuregate"
	"github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/version"
)

var (
	cfgFile          string
	cfgDir           string
	showVersion      bool
	strictConfigMode bool
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "./frpc.ini", "config file of frpc")
	rootCmd.PersistentFlags().StringVarP(&cfgDir, "config_dir", "", "", "config directory, run one frpc service for each file in config directory")
	rootCmd.PersistentFlags().BoolVarP(&showVersion, "version", "v", false, "version of frpc")
	rootCmd.PersistentFlags().BoolVarP(&strictConfigMode, "strict_config", "", true, "strict config parsing mode, unknown fields will cause an errors")
}

var rootCmd = &cobra.Command{
	Use:   "frpc",
	Short: "frpc is the client of frp (https://github.com/fatedier/frp)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if showVersion {
			fmt.Println(version.Full())
			return nil
		}

		// If cfgDir is not empty, run multiple frpc service for each config file in cfgDir.
		// Note that it's only designed for testing. It's not guaranteed to be stable.
		if cfgDir != "" {
			_ = runMultipleClients(cfgDir)
			return nil
		}

		// Do not show command usage here.
		err := runClient(cfgFile)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return nil
	},
}

func runMultipleClients(cfgDir string) error {
	var wg sync.WaitGroup
	err := filepath.WalkDir(cfgDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		wg.Add(1)
		time.Sleep(time.Millisecond)
		go func() {
			defer wg.Done()
			err := runClient(path)
			if err != nil {
				fmt.Printf("frpc service error for config file [%s]\n", path)
			}
		}()
		return nil
	})
	wg.Wait()
	return err
}

func Execute() {
	rootCmd.SetGlobalNormalizationFunc(config.WordSepNormalizeFunc)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func handleTermSignal(svr *client.Service) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	svr.GracefulClose(500 * time.Millisecond)
}

func RunClient(ctx context.Context, cfgFilePath string) error {
	return runClientWithContext(ctx, cfgFilePath)
}

func runClient(cfgFilePath string) error {
	return runClientWithContext(context.Background(), cfgFilePath)
}

func runClientWithContext(ctx context.Context, cfgFilePath string) error {
	cfg, proxyCfgs, visitorCfgs, isLegacyFormat, err := config.LoadClientConfig(cfgFilePath, strictConfigMode)
	if err != nil {
		return err
	}
	if isLegacyFormat {
		fmt.Printf("WARNING: ini format is deprecated and the support will be removed in the future, " +
			"please use yaml/json/toml format instead!\n")
	}

	if len(cfg.FeatureGates) > 0 {
		if err := featuregate.SetFromMap(cfg.FeatureGates); err != nil {
			return err
		}
	}

	warning, err := validation.ValidateAllClientConfig(cfg, proxyCfgs, visitorCfgs)
	if warning != nil {
		fmt.Printf("WARNING: %v\n", warning)
	}
	if err != nil {
		return err
	}
	return startServiceWithContext(ctx, cfg, proxyCfgs, visitorCfgs, cfgFilePath)
}

func startService(
	cfg *v1.ClientCommonConfig,
	proxyCfgs []v1.ProxyConfigurer,
	visitorCfgs []v1.VisitorConfigurer,
	cfgFile string,
) error {
	return startServiceWithContext(context.Background(), cfg, proxyCfgs, visitorCfgs, cfgFile)
}

func startServiceWithContext(
	ctx context.Context,
	cfg *v1.ClientCommonConfig,
	proxyCfgs []v1.ProxyConfigurer,
	visitorCfgs []v1.VisitorConfigurer,
	cfgFile string,
) error {
	log.InitLogger(cfg.Log.To, cfg.Log.Level, int(cfg.Log.MaxDays), cfg.Log.DisablePrintColor)

	if cfgFile != "" {
		log.Infof("start frpc service for config file [%s]", cfgFile)
		defer log.Infof("frpc service for config file [%s] stopped", cfgFile)
	}
	svr, err := client.NewService(client.ServiceOptions{
		Common:         cfg,
		ProxyCfgs:      proxyCfgs,
		VisitorCfgs:    visitorCfgs,
		ConfigFilePath: cfgFile,
	})
	if err != nil {
		return err
	}

	shouldGracefulClose := cfg.Transport.Protocol == "kcp" || cfg.Transport.Protocol == "quic"
	// Capture the exit signal if we use kcp or quic.
	if shouldGracefulClose {
		go handleTermSignal(svr)
	}
	return svr.Run(ctx)
}

// StartServiceWithCommand starts frpc service with command line parameters
// using a pre-calculated PrivilegeKey and timestamp.
//
// This function supports two usage modes:
//
// 1. Traditional mode (pass original token):
//    Simply pass timestamp=0, and it will use current time automatically.
//    privilegeKey, timestamp := CalculatePrivilegeKey(token, 0)
//    StartServiceWithCommand(ctx, privilegeKey, timestamp, ...)
//
// 2. Secure mode (pre-calculated credentials):
//    Server calculates and distributes privilegeKey+timestamp to clients.
//    Client uses them directly without knowing the original token.
//
// Parameters:
//   - privilegeKey: Pre-calculated authentication key (MD5(token + timestamp))
//   - timestamp: Unix timestamp used to calculate the privilegeKey (use 0 for current time)
//   - serverAddr: FRP server address
//   - serverPort: FRP server port
//   - user: User name (optional, can be empty)
//   - proxies: List of proxy configurations
//
// Example (traditional mode):
//
//	privilegeKey, timestamp := CalculatePrivilegeKey("my_token", 0)
//	err := StartServiceWithCommand(ctx, privilegeKey, timestamp, "frp.example.com", 7000, "", proxies)
//
// Example (secure mode):
//
//	// Server side: calculate credentials with expiry
//	privilegeKey, timestamp, _ := CalculatePrivilegeKeyWithDuration("server_token", 24*time.Hour)
//	// Distribute to client...
//	// Client side: use pre-calculated credentials
//	err := StartServiceWithCommand(ctx, privilegeKey, timestamp, "frp.example.com", 7000, "", proxies)
func StartServiceWithCommand(
	ctx context.Context,
	privilegeKey string,
	timestamp int64,
	serverAddr string,
	serverPort int,
	user string,
	proxies []ProxyParams,
) error {
	// Initialize ClientCommonConfig
	// Note: We set a placeholder token because Complete() expects it,
	// but we'll use the pre-calculated privilegeKey instead
	cfg := &v1.ClientCommonConfig{
		ServerAddr: serverAddr,
		ServerPort: serverPort,
		User:       user,
		Auth: v1.AuthClientConfig{
			Method: v1.AuthMethodToken,
			Token:  "placeholder", // Placeholder, actual auth uses privilegeKey
		},
	}

	// Complete configuration with defaults
	if err := cfg.Complete(); err != nil {
		return fmt.Errorf("complete config error: %v", err)
	}

	// Validate common configuration
	if _, err := validation.ValidateClientCommonConfig(cfg); err != nil {
		return fmt.Errorf("invalid common config: %v", err)
	}

	// Build proxy configurations
	var proxyCfgs []v1.ProxyConfigurer
	for _, p := range proxies {
		pc, err := createProxyConfigurer(p)
		if err != nil {
			return fmt.Errorf("create proxy [%s] error: %v", p.Name, err)
		}
		proxyCfgs = append(proxyCfgs, pc)
	}

	// Complete all proxy configurations
	for _, pc := range proxyCfgs {
		pc.Complete(cfg.User)
		if err := validation.ValidateProxyConfigurerForClient(pc); err != nil {
			return fmt.Errorf("invalid proxy config [%s]: %v", pc.GetBaseConfig().Name, err)
		}
	}

	// Initialize logger
	log.InitLogger(cfg.Log.To, cfg.Log.Level, int(cfg.Log.MaxDays), cfg.Log.DisablePrintColor)

	log.Infof("start frpc service with %d proxies using pre-calculated credentials", len(proxyCfgs))
	log.Infof("privilegeKey: %s, timestamp: %d", privilegeKey, timestamp)
	defer log.Infof("frpc service stopped")

	// Create service
	svr, err := client.NewService(client.ServiceOptions{
		Common:      cfg,
		ProxyCfgs:   proxyCfgs,
		VisitorCfgs: nil, // Visitors not supported in this simplified API
	})
	if err != nil {
		return err
	}

	shouldGracefulClose := cfg.Transport.Protocol == "kcp" || cfg.Transport.Protocol == "quic"
	if shouldGracefulClose {
		go handleTermSignal(svr)
	}

	// Use RunWithPrivilegeKey to bypass token-based auth calculation
	// and use the pre-calculated privilegeKey and timestamp directly
	return svr.RunWithPrivilegeKey(ctx, privilegeKey, timestamp)
}

// ProxyParams defines parameters for creating a proxy configuration
type ProxyParams struct {
	Name      string   // Proxy name
	Type      string   // Proxy type: tcp, udp, http, https, stcp, xtcp, sudp, tcpmux
	LocalIP   string   // Local IP address (default: 127.0.0.1)
	LocalPort int      // Local port

	// For TCP/UDP proxies
	RemotePort int      // Remote port

	// For HTTP/HTTPS/TCPMUX proxies
	CustomDomains []string // Custom domains
	SubDomain     string   // Sub domain

	// Optional settings
	UseEncryption    bool   // Enable encryption
	UseCompression   bool   // Enable compression
	BandwidthLimit   string // Bandwidth limit (e.g., "1MB")

	// For STCP/XTCP/SUDP proxies
	SecretKey  string   // Secret key for P2P proxies
	AllowUsers []string // Allowed users for P2P proxies
}

// createProxyConfigurer creates a ProxyConfigurer from ProxyParams
func createProxyConfigurer(p ProxyParams) (v1.ProxyConfigurer, error) {
	// Set default local IP
	if p.LocalIP == "" {
		p.LocalIP = "127.0.0.1"
	}

	// Auto-generate proxy name if not provided
	if p.Name == "" {
		p.Name = fmt.Sprintf("%s_%d_%d", p.Type, p.LocalPort, p.RemotePort)
	}
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local port must be greater than 0")
	}

	// Create base config
	base := v1.ProxyBaseConfig{
		Name: p.Name,
		Type: p.Type,
		ProxyBackend: v1.ProxyBackend{
			LocalIP:   p.LocalIP,
			LocalPort: p.LocalPort,
		},
		Transport: v1.ProxyTransport{
			UseEncryption:  p.UseEncryption,
			UseCompression: p.UseCompression,
		},
	}

	// Set bandwidth limit if specified
	if p.BandwidthLimit != "" {
		bwLimit, err := types.NewBandwidthQuantity(p.BandwidthLimit)
		if err != nil {
			return nil, fmt.Errorf("invalid bandwidth limit: %v", err)
		}
		base.Transport.BandwidthLimit = bwLimit
	}

	// Create specific proxy type
	switch p.Type {
	case "tcp":
		if p.RemotePort <= 0 {
			return nil, fmt.Errorf("remote port is required for TCP proxy")
		}
		return &v1.TCPProxyConfig{
			ProxyBaseConfig: base,
			RemotePort:      p.RemotePort,
		}, nil

	case "udp":
		if p.RemotePort <= 0 {
			return nil, fmt.Errorf("remote port is required for UDP proxy")
		}
		return &v1.UDPProxyConfig{
			ProxyBaseConfig: base,
			RemotePort:      p.RemotePort,
		}, nil

	case "http":
		if len(p.CustomDomains) == 0 && p.SubDomain == "" {
			return nil, fmt.Errorf("custom domains or subdomain is required for HTTP proxy")
		}
		return &v1.HTTPProxyConfig{
			ProxyBaseConfig: base,
			DomainConfig: v1.DomainConfig{
				CustomDomains: p.CustomDomains,
				SubDomain:     p.SubDomain,
			},
		}, nil

	case "https":
		if len(p.CustomDomains) == 0 && p.SubDomain == "" {
			return nil, fmt.Errorf("custom domains or subdomain is required for HTTPS proxy")
		}
		return &v1.HTTPSProxyConfig{
			ProxyBaseConfig: base,
			DomainConfig: v1.DomainConfig{
				CustomDomains: p.CustomDomains,
				SubDomain:     p.SubDomain,
			},
		}, nil

	case "tcpmux":
		if len(p.CustomDomains) == 0 && p.SubDomain == "" {
			return nil, fmt.Errorf("custom domains or subdomain is required for TCPMUX proxy")
		}
		return &v1.TCPMuxProxyConfig{
			ProxyBaseConfig: base,
			DomainConfig: v1.DomainConfig{
				CustomDomains: p.CustomDomains,
				SubDomain:     p.SubDomain,
			},
		}, nil

	case "stcp":
		if p.SecretKey == "" {
			return nil, fmt.Errorf("secret key is required for STCP proxy")
		}
		return &v1.STCPProxyConfig{
			ProxyBaseConfig: base,
			Secretkey:       p.SecretKey,
			AllowUsers:      p.AllowUsers,
		}, nil

	case "xtcp":
		if p.SecretKey == "" {
			return nil, fmt.Errorf("secret key is required for XTCP proxy")
		}
		return &v1.XTCPProxyConfig{
			ProxyBaseConfig: base,
			Secretkey:       p.SecretKey,
			AllowUsers:      p.AllowUsers,
		}, nil

	case "sudp":
		if p.SecretKey == "" {
			return nil, fmt.Errorf("secret key is required for SUDP proxy")
		}
		return &v1.SUDPProxyConfig{
			ProxyBaseConfig: base,
			Secretkey:       p.SecretKey,
			AllowUsers:      p.AllowUsers,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported proxy type: %s", p.Type)
	}
}
