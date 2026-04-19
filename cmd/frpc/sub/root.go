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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/samber/lo"
	"github.com/spf13/cobra"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/policy/featuregate"
	"github.com/fatedier/frp/pkg/policy/security"
	"github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/version"
)

var (
	cfgFile          string
	cfgDir           string
	showVersion      bool
	strictConfigMode bool
	allowUnsafe      []string
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "./frpc.ini", "config file of frpc")
	rootCmd.PersistentFlags().StringVarP(&cfgDir, "config_dir", "", "", "config directory, run one frpc service for each file in config directory")
	rootCmd.PersistentFlags().BoolVarP(&showVersion, "version", "v", false, "version of frpc")
	rootCmd.PersistentFlags().BoolVarP(&strictConfigMode, "strict_config", "", true, "strict config parsing mode, unknown fields will cause an errors")

	rootCmd.PersistentFlags().StringSliceVarP(&allowUnsafe, "allow-unsafe", "", []string{},
		fmt.Sprintf("allowed unsafe features, one or more of: %s", strings.Join(security.ClientUnsafeFeatures, ", ")))
}

var rootCmd = &cobra.Command{
	Use:   "frpc",
	Short: "frpc is the client of frp (https://github.com/fatedier/frp)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if showVersion {
			fmt.Println(version.Full())
			return nil
		}

		unsafeFeatures := security.NewUnsafeFeatures(allowUnsafe)

		// If cfgDir is not empty, run multiple frpc service for each config file in cfgDir.
		// Note that it's only designed for testing. It's not guaranteed to be stable.
		if cfgDir != "" {
			_ = runMultipleClients(cfgDir, unsafeFeatures)
			return nil
		}

		// Do not show command usage here.
		err := runClient(cfgFile, unsafeFeatures)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return nil
	},
}

func runMultipleClients(cfgDir string, unsafeFeatures *security.UnsafeFeatures) error {
	var wg sync.WaitGroup
	err := filepath.WalkDir(cfgDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		wg.Add(1)
		time.Sleep(time.Millisecond)
		go func() {
			defer wg.Done()
			err := runClient(path, unsafeFeatures)
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

// RunClient is exported for DLL usage, allowing external control via context
func RunClient(ctx context.Context, cfgFilePath string) error {
	return runClientWithContext(ctx, cfgFilePath, security.NewUnsafeFeatures(nil))
}

func runClient(cfgFilePath string, unsafeFeatures *security.UnsafeFeatures) error {
	return runClientWithContext(context.Background(), cfgFilePath, unsafeFeatures)
}

func runClientWithContext(ctx context.Context, cfgFilePath string, unsafeFeatures *security.UnsafeFeatures) error {
	// Load configuration
	result, err := config.LoadClientConfigResult(cfgFilePath, strictConfigMode)
	if err != nil {
		return err
	}
	if result.IsLegacyFormat {
		fmt.Printf("WARNING: ini format is deprecated and the support will be removed in the future, " +
			"please use yaml/json/toml format instead!\n")
	}

	if len(result.Common.FeatureGates) > 0 {
		if err := featuregate.SetFromMap(result.Common.FeatureGates); err != nil {
			return err
		}
	}

	return runClientWithAggregatorContext(ctx, result, unsafeFeatures, cfgFilePath)
}

// runClientWithAggregatorContext runs the client using the internal source aggregator with context support.
func runClientWithAggregatorContext(ctx context.Context, result *config.ClientConfigLoadResult, unsafeFeatures *security.UnsafeFeatures, cfgFilePath string) error {
	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll(result.Proxies, result.Visitors); err != nil {
		return fmt.Errorf("failed to set config source: %w", err)
	}

	var storeSource *source.StoreSource

	if result.Common.Store.IsEnabled() {
		storePath := result.Common.Store.Path
		if storePath != "" && cfgFilePath != "" && !filepath.IsAbs(storePath) {
			storePath = filepath.Join(filepath.Dir(cfgFilePath), storePath)
		}

		s, err := source.NewStoreSource(source.StoreSourceConfig{
			Path: storePath,
		})
		if err != nil {
			return fmt.Errorf("failed to create store source: %w", err)
		}
		storeSource = s
	}

	aggregator := source.NewAggregator(configSource)
	if storeSource != nil {
		aggregator.SetStoreSource(storeSource)
	}

	proxyCfgs, visitorCfgs, err := aggregator.Load()
	if err != nil {
		return fmt.Errorf("failed to load config from sources: %w", err)
	}

	proxyCfgs, visitorCfgs = config.FilterClientConfigurers(result.Common, proxyCfgs, visitorCfgs)
	proxyCfgs = config.CompleteProxyConfigurers(proxyCfgs)
	visitorCfgs = config.CompleteVisitorConfigurers(visitorCfgs)

	warning, err := validation.ValidateAllClientConfig(result.Common, proxyCfgs, visitorCfgs, unsafeFeatures)
	if warning != nil {
		fmt.Printf("WARNING: %v\n", warning)
	}
	if err != nil {
		return err
	}

	return startServiceWithAggregatorContext(ctx, result.Common, aggregator, unsafeFeatures, cfgFilePath)
}

func startServiceWithAggregatorContext(
	ctx context.Context,
	cfg *v1.ClientCommonConfig,
	aggregator *source.Aggregator,
	unsafeFeatures *security.UnsafeFeatures,
	cfgFile string,
) error {
	log.InitLogger(cfg.Log.To, cfg.Log.Level, int(cfg.Log.MaxDays), cfg.Log.DisablePrintColor)

	if cfgFile != "" {
		log.Infof("start frpc service for config file [%s]", cfgFile)
		defer log.Infof("frpc service for config file [%s] stopped", cfgFile)
	}
	svr, err := client.NewService(client.ServiceOptions{
		Common:                 cfg,
		ConfigSourceAggregator: aggregator,
		UnsafeFeatures:         unsafeFeatures,
		ConfigFilePath:         cfgFile,
	})
	if err != nil {
		return err
	}

	shouldGracefulClose := cfg.Transport.Protocol == "kcp" || cfg.Transport.Protocol == "quic"
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
//  1. Traditional mode (pass original token):
//     Simply pass timestamp=0, and it will use current time automatically.
//     privilegeKey, timestamp := CalculatePrivilegeKey(token, 0)
//     StartServiceWithCommand(ctx, privilegeKey, timestamp, ...)
//
//  2. Secure mode (pre-calculated credentials):
//     Server calculates and distributes privilegeKey+timestamp to clients.
//     Client uses them directly without knowing the original token.
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
		Transport: v1.ClientTransportConfig{
			TCPMux: lo.ToPtr(true), // Enable TCPMux (default)
		},
		// Disable LoginFailExit so that loopLoginUntilSuccess will retry on first failure
		// instead of immediately giving up. This is important for DLL usage where
		// Go runtime's network stack may not be fully ready on the first call.
		LoginFailExit: lo.ToPtr(false),
	}

	// Complete configuration with defaults
	if err := cfg.Complete(); err != nil {
		return fmt.Errorf("complete config error: %v", err)
	}

	// Validate common configuration
	validator := validation.NewConfigValidator(nil)
	if _, err := validator.ValidateClientCommonConfig(cfg); err != nil {
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

	// Create service
	// Note: We use ClientSpec with Type="ssh-tunnel" to disable connection encryption
	// because we don't have the original token (only pre-calculated privilegeKey).
	// The connection encryption uses token as the key, which we don't have.
	svr, err := client.NewService(client.ServiceOptions{
		Common:      cfg,
		ProxyCfgs:   proxyCfgs,
		VisitorCfgs: nil, // Visitors not supported in this simplified API
		ClientSpec: &msg.ClientSpec{
			Type: "ssh-tunnel", // This disables connection encryption (connEncrypted = false)
		},
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

// StartServiceWithToken starts frpc service with a token (password) and single proxy configuration.
// Unlike StartServiceWithCommand which requires a pre-calculated privilegeKey,
// this function takes the original token and handles authentication internally.
//
// Since the original token is available, this function uses the standard connection
// flow with encryption support (no need for ssh-tunnel workaround).
//
// Parameters:
//   - token: The FRP authentication token (password)
//   - serverAddr: FRP server address
//   - serverPort: FRP server port
//   - user: User name (optional, can be empty)
//   - proxies: List of proxy configurations
//
// Example:
//
//	err := StartServiceWithToken(ctx, "my_secret_token", "frp.example.com", 7000, "", proxies)
func StartServiceWithToken(
	ctx context.Context,
	token string,
	serverAddr string,
	serverPort int,
	user string,
	proxies []ProxyParams,
) error {
	// Initialize ClientCommonConfig with the actual token
	cfg := &v1.ClientCommonConfig{
		ServerAddr: serverAddr,
		ServerPort: serverPort,
		User:       user,
		Auth: v1.AuthClientConfig{
			Method: v1.AuthMethodToken,
			Token:  token,
		},
		Transport: v1.ClientTransportConfig{
			TCPMux: lo.ToPtr(true),
		},
		// Disable LoginFailExit so that loopLoginUntilSuccess will retry on first failure
		// instead of immediately giving up. This is important for DLL usage where
		// transient network issues should not cause immediate exit.
		LoginFailExit: lo.ToPtr(false),
	}

	// Complete configuration with defaults
	if err := cfg.Complete(); err != nil {
		return fmt.Errorf("complete config error: %v", err)
	}

	// Validate common configuration
	validator := validation.NewConfigValidator(nil)
	if _, err := validator.ValidateClientCommonConfig(cfg); err != nil {
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

	// Create service - no need for ssh-tunnel workaround since we have the actual token
	svr, err := client.NewService(client.ServiceOptions{
		Common:      cfg,
		ProxyCfgs:   proxyCfgs,
		VisitorCfgs: nil,
	})
	if err != nil {
		return err
	}

	shouldGracefulClose := cfg.Transport.Protocol == "kcp" || cfg.Transport.Protocol == "quic"
	if shouldGracefulClose {
		go handleTermSignal(svr)
	}

	// Use standard Run since we have the actual token for authentication
	return svr.Run(ctx)
}

// ProxyParams defines parameters for creating a proxy configuration
type ProxyParams struct {
	Name      string // Proxy name
	Type      string // Proxy type: tcp, udp, http, https, stcp, xtcp, sudp, tcpmux
	LocalIP   string // Local IP address (default: 127.0.0.1)
	LocalPort int    // Local port

	// For TCP/UDP proxies
	RemotePort int // Remote port

	// For HTTP/HTTPS/TCPMUX proxies
	CustomDomains []string // Custom domains
	SubDomain     string   // Sub domain

	// Optional settings
	UseEncryption  bool   // Enable encryption
	UseCompression bool   // Enable compression
	BandwidthLimit string // Bandwidth limit (e.g., "1MB")

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
