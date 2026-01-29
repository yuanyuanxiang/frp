// Copyright 2016 fatedier, fatedier@gmail.com
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

package main

import "C"

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/fatedier/frp/cmd/frpc/sub"
	"github.com/fatedier/frp/pkg/util/system"
	_ "github.com/fatedier/frp/web/frpc"
)

const (
	STATUS_UNKNOWN = -1
	STATUS_RUN     = 0
	STATUS_STOP    = 1
	STATUS_EXIT    = 2
)

//export Run
func Run(cstr *C.char, ptr *C.int) C.int {
	cfg := C.GoString(cstr)
	if cfg == "" {
		exePath, err := os.Executable()
		if err == nil {
			cfg = filepath.Join(filepath.Dir(exePath), "frpc.ini")
		} else {
			cfg = "./frpc.ini"
		}
	}

	var (
		ctx        context.Context
		cancel     func()
		lastStatus C.int = STATUS_UNKNOWN
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
			return C.int(-1)
		default:
			status := *(*C.int)(unsafe.Pointer(ptr))
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
					if err := sub.RunClient(ctx, cfg); err != nil {
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
				return C.int(0)
			}
		}
	}
}

// RunSimpleTcp starts a simple TCP proxy with minimal parameters and status control.
//
// Status values:
//   - STATUS_RUN (0): Start the service
//   - STATUS_STOP (1): Stop the service (can restart later)
//   - STATUS_EXIT (2): Stop and exit
//
// Parameters:
//   - privilegeKey: Pre-calculated MD5 authentication key
//   - timestamp: Unix timestamp used for privilegeKey calculation (0 = current time)
//   - serverAddr: FRP server address
//   - serverPort: FRP server port
//   - localPort: Local port to expose
//   - remotePort: Remote port on the server
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export RunSimpleTcp
func RunSimpleTcp(
	privilegeKey *C.char,
	timestamp C.long,
	serverAddr *C.char,
	serverPort C.int,
	localPort C.int,
	remotePort C.int,
	statusPtr *C.int,
) C.int {
	return RunWithPrivilegeKeyControlled(
		privilegeKey,
		timestamp,
		serverAddr,
		serverPort,
		nil, // user
		nil, // proxyName (auto-generated: tcp_localPort_remotePort)
		nil, // proxyType (default: tcp)
		nil, // localIP (default: 127.0.0.1)
		localPort,
		remotePort,
		nil, // customDomains
		nil, // subDomain
		0,   // useEncryption
		0,   // useCompression
		nil, // bandwidthLimit
		nil, // secretKey
		nil, // allowUsers
		statusPtr,
	)
}

// RunSimpleTcpWithToken starts a simple TCP proxy using a token (password) for authentication.
// This is the token-based counterpart of RunSimpleTcp.
//
// Parameters:
//   - token: FRP authentication token (password)
//   - serverAddr: FRP server address
//   - serverPort: FRP server port
//   - localPort: Local port to expose
//   - remotePort: Remote port on the server
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export RunSimpleTcpWithToken
func RunSimpleTcpWithToken(
	token *C.char,
	serverAddr *C.char,
	serverPort C.int,
	localPort C.int,
	remotePort C.int,
	statusPtr *C.int,
) C.int {
	return RunWithTokenControlled(
		token,
		serverAddr,
		serverPort,
		nil, // user
		nil, // proxyName (auto-generated: tcp_localPort_remotePort)
		nil, // proxyType (default: tcp)
		nil, // localIP (default: 127.0.0.1)
		localPort,
		remotePort,
		nil, // customDomains
		nil, // subDomain
		0,   // useEncryption
		0,   // useCompression
		nil, // bandwidthLimit
		nil, // secretKey
		nil, // allowUsers
		statusPtr,
	)
}

// RunWithToken starts frpc service with a token (password) and single proxy configuration.
// This is the token-based counterpart of RunWithPrivilegeKey.
// This function blocks until the service stops (no dynamic control).
//
// Parameters:
//   - token: FRP authentication token (password)
//   - serverAddr: FRP server address
//   - serverPort: FRP server port
//   - user: User name (empty string for default)
//   - proxyName: Proxy name
//   - proxyType: Proxy type (tcp/udp/http/https/stcp/xtcp/sudp/tcpmux)
//   - localIP: Local IP address (empty string for 127.0.0.1)
//   - localPort: Local port
//   - remotePort: Remote port (for tcp/udp proxies)
//   - customDomains: Custom domains, comma-separated (for http/https/tcpmux proxies)
//   - subDomain: Sub domain (for http/https/tcpmux proxies)
//   - useEncryption: Enable encryption (1=true, 0=false)
//   - useCompression: Enable compression (1=true, 0=false)
//   - bandwidthLimit: Bandwidth limit string (e.g., "1MB"), empty for unlimited
//   - secretKey: Secret key for P2P proxies (stcp/xtcp/sudp)
//   - allowUsers: Allowed users for P2P proxies, comma-separated
//
// Returns:
//   - 0: Success
//   - -1: Error occurred
//
//export RunWithToken
func RunWithToken(
	token *C.char,
	serverAddr *C.char,
	serverPort C.int,
	user *C.char,
	proxyName *C.char,
	proxyType *C.char,
	localIP *C.char,
	localPort C.int,
	remotePort C.int,
	customDomains *C.char,
	subDomain *C.char,
	useEncryption C.int,
	useCompression C.int,
	bandwidthLimit *C.char,
	secretKey *C.char,
	allowUsers *C.char,
) C.int {
	status := C.int(STATUS_RUN)

	return RunWithTokenControlled(
		token,
		serverAddr,
		serverPort,
		user,
		proxyName,
		proxyType,
		localIP,
		localPort,
		remotePort,
		customDomains,
		subDomain,
		useEncryption,
		useCompression,
		bandwidthLimit,
		secretKey,
		allowUsers,
		&status,
	)
}

// RunWithTokenControlled starts frpc service with a token (password) and dynamic status control.
// This is the token-based counterpart of RunWithPrivilegeKeyControlled.
//
// Status values:
//   - STATUS_RUN (0): Start the service
//   - STATUS_STOP (1): Stop the service (can restart later)
//   - STATUS_EXIT (2): Stop and exit
//
// Parameters: Same as RunWithToken, plus:
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export RunWithTokenControlled
func RunWithTokenControlled(
	token *C.char,
	serverAddr *C.char,
	serverPort C.int,
	user *C.char,
	proxyName *C.char,
	proxyType *C.char,
	localIP *C.char,
	localPort C.int,
	remotePort C.int,
	customDomains *C.char,
	subDomain *C.char,
	useEncryption C.int,
	useCompression C.int,
	bandwidthLimit *C.char,
	secretKey *C.char,
	allowUsers *C.char,
	statusPtr *C.int,
) C.int {
	cStringToGo := func(cstr *C.char, defaultVal string) string {
		if cstr == nil {
			return defaultVal
		}
		s := C.GoString(cstr)
		if s == "" {
			return defaultVal
		}
		return s
	}

	goToken := cStringToGo(token, "")
	goServerAddr := cStringToGo(serverAddr, "")
	goServerPort := int(serverPort)
	goUser := cStringToGo(user, "")
	goProxyName := cStringToGo(proxyName, "")
	goProxyType := cStringToGo(proxyType, "tcp")
	goLocalIP := cStringToGo(localIP, "")
	goLocalPort := int(localPort)
	goRemotePort := int(remotePort)
	goCustomDomains := cStringToGo(customDomains, "")
	goSubDomain := cStringToGo(subDomain, "")
	goUseEncryption := useEncryption != 0
	goUseCompression := useCompression != 0
	goBandwidthLimit := cStringToGo(bandwidthLimit, "")
	goSecretKey := cStringToGo(secretKey, "")
	goAllowUsers := cStringToGo(allowUsers, "")

	proxy := sub.ProxyParams{
		Name:           goProxyName,
		Type:           goProxyType,
		LocalIP:        goLocalIP,
		LocalPort:      goLocalPort,
		RemotePort:     goRemotePort,
		SubDomain:      goSubDomain,
		UseEncryption:  goUseEncryption,
		UseCompression: goUseCompression,
		BandwidthLimit: goBandwidthLimit,
		SecretKey:      goSecretKey,
	}

	if goCustomDomains != "" {
		domains := strings.Split(goCustomDomains, ",")
		for i := range domains {
			domains[i] = strings.TrimSpace(domains[i])
		}
		proxy.CustomDomains = domains
	}

	if goAllowUsers != "" {
		users := strings.Split(goAllowUsers, ",")
		for i := range users {
			users[i] = strings.TrimSpace(users[i])
		}
		proxy.AllowUsers = users
	}

	var (
		ctx        context.Context
		cancel     func()
		lastStatus C.int = STATUS_UNKNOWN
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
			return C.int(-1)
		default:
			status := *(*C.int)(unsafe.Pointer(statusPtr))
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
					err := sub.StartServiceWithToken(
						ctx,
						goToken,
						goServerAddr,
						goServerPort,
						goUser,
						[]sub.ProxyParams{proxy},
					)
					if err != nil {
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
				return C.int(0)
			}
		}
	}
}

// RunWithPrivilegeKey starts frpc service with pre-calculated privilegeKey and single proxy configuration.
// This is a simplified version of RunWithPrivilegeKeyControlled that runs until completion.
// This function blocks until the service stops (no dynamic control).
//
// Parameters:
//   - privilegeKey: Pre-calculated MD5 authentication key (from CalculatePrivilegeKey)
//   - timestamp: Unix timestamp used for privilegeKey calculation (0 = current time)
//   - serverAddr: FRP server address
//   - serverPort: FRP server port
//   - user: User name (empty string for default)
//   - proxyName: Proxy name
//   - proxyType: Proxy type (tcp/udp/http/https/stcp/xtcp/sudp/tcpmux)
//   - localIP: Local IP address (empty string for 127.0.0.1)
//   - localPort: Local port
//   - remotePort: Remote port (for tcp/udp proxies)
//   - customDomains: Custom domains, comma-separated (for http/https/tcpmux proxies)
//   - subDomain: Sub domain (for http/https/tcpmux proxies)
//   - useEncryption: Enable encryption (1=true, 0=false)
//   - useCompression: Enable compression (1=true, 0=false)
//   - bandwidthLimit: Bandwidth limit string (e.g., "1MB"), empty for unlimited
//   - secretKey: Secret key for P2P proxies (stcp/xtcp/sudp)
//   - allowUsers: Allowed users for P2P proxies, comma-separated
//
// Returns:
//   - 0: Success
//   - -1: Error occurred
//
// Note: This function internally uses RunWithPrivilegeKeyControlled with a fixed status (RUN).
//
//export RunWithPrivilegeKey
func RunWithPrivilegeKey(
	privilegeKey *C.char,
	timestamp C.long,
	serverAddr *C.char,
	serverPort C.int,
	user *C.char,
	proxyName *C.char,
	proxyType *C.char,
	localIP *C.char,
	localPort C.int,
	remotePort C.int,
	customDomains *C.char,
	subDomain *C.char,
	useEncryption C.int,
	useCompression C.int,
	bandwidthLimit *C.char,
	secretKey *C.char,
	allowUsers *C.char,
) C.int {
	// Use a fixed status pointer that always stays at RUN
	// This makes the function behave like a simple blocking call
	status := C.int(STATUS_RUN)

	// Delegate to RunWithPrivilegeKeyControlled
	return RunWithPrivilegeKeyControlled(
		privilegeKey,
		timestamp,
		serverAddr,
		serverPort,
		user,
		proxyName,
		proxyType,
		localIP,
		localPort,
		remotePort,
		customDomains,
		subDomain,
		useEncryption,
		useCompression,
		bandwidthLimit,
		secretKey,
		allowUsers,
		&status,
	)
}

// RunWithPrivilegeKeyControlled is similar to Run but uses pre-calculated privilegeKey.
// It supports dynamic start/stop/exit control via status pointer.
//
// Status values:
//   - STATUS_RUN (0): Start the service
//   - STATUS_STOP (1): Stop the service (can restart later)
//   - STATUS_EXIT (2): Stop and exit
//
// Parameters: Same as RunWithPrivilegeKey, plus:
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
// This function blocks until STATUS_EXIT is set.
//
//export RunWithPrivilegeKeyControlled
func RunWithPrivilegeKeyControlled(
	privilegeKey *C.char,
	timestamp C.long,
	serverAddr *C.char,
	serverPort C.int,
	user *C.char,
	proxyName *C.char,
	proxyType *C.char,
	localIP *C.char,
	localPort C.int,
	remotePort C.int,
	customDomains *C.char,
	subDomain *C.char,
	useEncryption C.int,
	useCompression C.int,
	bandwidthLimit *C.char,
	secretKey *C.char,
	allowUsers *C.char,
	statusPtr *C.int,
) C.int {
	// Helper function to safely convert C string to Go string with default value
	cStringToGo := func(cstr *C.char, defaultVal string) string {
		if cstr == nil {
			return defaultVal
		}
		s := C.GoString(cstr)
		if s == "" {
			return defaultVal
		}
		return s
	}

	// Convert parameters
	goPrivilegeKey := cStringToGo(privilegeKey, "")
	goTimestamp := int64(timestamp)
	goServerAddr := cStringToGo(serverAddr, "")
	goServerPort := int(serverPort)
	goUser := cStringToGo(user, "")
	goProxyName := cStringToGo(proxyName, "")
	goProxyType := cStringToGo(proxyType, "tcp")
	goLocalIP := cStringToGo(localIP, "")
	goLocalPort := int(localPort)
	goRemotePort := int(remotePort)
	goCustomDomains := cStringToGo(customDomains, "")
	goSubDomain := cStringToGo(subDomain, "")
	goUseEncryption := useEncryption != 0
	goUseCompression := useCompression != 0
	goBandwidthLimit := cStringToGo(bandwidthLimit, "")
	goSecretKey := cStringToGo(secretKey, "")
	goAllowUsers := cStringToGo(allowUsers, "")

	// Build proxy parameters
	proxy := sub.ProxyParams{
		Name:           goProxyName,
		Type:           goProxyType,
		LocalIP:        goLocalIP,
		LocalPort:      goLocalPort,
		RemotePort:     goRemotePort,
		SubDomain:      goSubDomain,
		UseEncryption:  goUseEncryption,
		UseCompression: goUseCompression,
		BandwidthLimit: goBandwidthLimit,
		SecretKey:      goSecretKey,
	}

	if goCustomDomains != "" {
		domains := strings.Split(goCustomDomains, ",")
		for i := range domains {
			domains[i] = strings.TrimSpace(domains[i])
		}
		proxy.CustomDomains = domains
	}

	if goAllowUsers != "" {
		users := strings.Split(goAllowUsers, ",")
		for i := range users {
			users[i] = strings.TrimSpace(users[i])
		}
		proxy.AllowUsers = users
	}

	// State management similar to Run function
	var (
		ctx        context.Context
		cancel     func()
		lastStatus C.int = STATUS_UNKNOWN
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
			return C.int(-1)
		default:
			status := *(*C.int)(unsafe.Pointer(statusPtr))
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
					err := sub.StartServiceWithCommand(
						ctx,
						goPrivilegeKey,
						goTimestamp,
						goServerAddr,
						goServerPort,
						goUser,
						[]sub.ProxyParams{proxy},
					)
					if err != nil {
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
				return C.int(0)
			}
		}
	}
}

func main() {
	system.EnableCompatibilityMode()
	sub.Execute()
}
