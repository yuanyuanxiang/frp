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

package main

import "C"

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/fatedier/frp/pkg/config"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	_ "github.com/fatedier/frp/pkg/metrics"
	"github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/system"
	"github.com/fatedier/frp/server"
	_ "github.com/fatedier/frp/web/frps"
)

const (
	STATUS_UNKNOWN = -1
	STATUS_RUN     = 0
	STATUS_STOP    = 1
	STATUS_EXIT    = 2
)

// cStringToGo safely converts C string to Go string with default value
func cStringToGo(cstr *C.char, defaultVal string) string {
	if cstr == nil {
		return defaultVal
	}
	s := C.GoString(cstr)
	if s == "" {
		return defaultVal
	}
	return s
}

// Run starts frps service with a configuration file path.
// The status can be controlled via the statusPtr pointer:
//   - STATUS_RUN (0): Start the service
//   - STATUS_STOP (1): Stop the service (can restart later)
//   - STATUS_EXIT (2): Stop and exit
//
// Parameters:
//   - cstr: Path to the configuration file (if empty, uses default "frps.toml")
//   - ptr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export Run
func Run(cstr *C.char, ptr *C.int) C.int {
	cfg := C.GoString(cstr)
	if cfg == "" {
		exePath, err := os.Executable()
		if err == nil {
			cfg = filepath.Join(filepath.Dir(exePath), "frps.toml")
		} else {
			cfg = "./frps.toml"
		}
	}

	return runWithStatusControl(ptr, func(ctx context.Context) error {
		return runServerFromFile(ctx, cfg)
	})
}

// RunSimple starts a simple frps server with minimal parameters.
//
// Status values:
//   - STATUS_RUN (0): Start the service
//   - STATUS_STOP (1): Stop the service (can restart later)
//   - STATUS_EXIT (2): Stop and exit
//
// Parameters:
//   - bindPort: Port to listen for client connections
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export RunSimple
func RunSimple(
	bindPort C.int,
	statusPtr *C.int,
) C.int {
	return RunWithTokenControlled(
		nil, // token (no authentication)
		nil, // bindAddr (default: 0.0.0.0)
		bindPort,
		nil, // logFile (default: ./frps.log)
		nil, // logLevel (default: info)
		0,   // logMaxDays (default: 3)
		statusPtr,
	)
}

// RunSimpleWithToken starts a simple frps server with token authentication.
//
// Parameters:
//   - token: Authentication token
//   - bindPort: Port to listen for client connections
//   - logFile: Log file path (nil = "./frps.log")
//   - logLevel: Log level: trace, debug, info, warn, error (nil = "info")
//   - logMaxDays: Log retention days (0 = default 60 days)
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export RunSimpleWithToken
func RunSimpleWithToken(
	token *C.char,
	bindPort C.int,
	logFile *C.char,
	logLevel *C.char,
	logMaxDays C.int,
	statusPtr *C.int,
) C.int {
	return RunWithTokenControlled(
		token,
		nil, // bindAddr (default: 0.0.0.0)
		bindPort,
		logFile,
		logLevel,
		logMaxDays,
		statusPtr,
	)
}

// RunWithToken starts frps service with token authentication.
// This function blocks until the service stops (no dynamic control).
//
// Parameters:
//   - token: Authentication token (nil = no authentication)
//   - bindAddr: Address to bind to (nil = "0.0.0.0")
//   - bindPort: Port to listen for client connections
//   - logFile: Log file path (nil = "./frps.log")
//   - logLevel: Log level: trace, debug, info, warn, error (nil = "info")
//   - logMaxDays: Log retention days (0 = default 60 days)
//
// Returns:
//   - 0: Success
//   - -1: Error occurred
//
//export RunWithToken
func RunWithToken(
	token *C.char,
	bindAddr *C.char,
	bindPort C.int,
	logFile *C.char,
	logLevel *C.char,
	logMaxDays C.int,
) C.int {
	status := C.int(STATUS_RUN)
	return RunWithTokenControlled(token, bindAddr, bindPort, logFile, logLevel, logMaxDays, &status)
}

// RunWithTokenControlled starts frps service with token authentication and dynamic status control.
//
// Status values:
//   - STATUS_RUN (0): Start the service
//   - STATUS_STOP (1): Stop the service (can restart later)
//   - STATUS_EXIT (2): Stop and exit
//
// Parameters:
//   - token: Authentication token (nil = no authentication)
//   - bindAddr: Address to bind to (nil = "0.0.0.0")
//   - bindPort: Port to listen for client connections
//   - logFile: Log file path (nil = "./frps.log")
//   - logLevel: Log level: trace, debug, info, warn, error (nil = "info")
//   - logMaxDays: Log retention days (0 = default 60 days)
//   - statusPtr: Pointer to int for status control
//
// Returns:
//   - 0: Success (STATUS_EXIT)
//   - -1: Error occurred
//
//export RunWithTokenControlled
func RunWithTokenControlled(
	token *C.char,
	bindAddr *C.char,
	bindPort C.int,
	logFile *C.char,
	logLevel *C.char,
	logMaxDays C.int,
	statusPtr *C.int,
) C.int {
	goToken := cStringToGo(token, "")
	goBindAddr := cStringToGo(bindAddr, "0.0.0.0")
	goBindPort := int(bindPort)
	goLogFile := cStringToGo(logFile, "./frps.log")
	goLogLevel := cStringToGo(logLevel, "info")
	goLogMaxDays := int(logMaxDays)
	if goLogMaxDays <= 0 {
		goLogMaxDays = 60 // default
	}

	return runWithStatusControl(statusPtr, func(ctx context.Context) error {
		return startServerWithParams(ctx, goToken, goBindAddr, goBindPort, goLogFile, goLogLevel, goLogMaxDays)
	})
}

// runWithStatusControl is a common status control loop for all Run* functions
func runWithStatusControl(statusPtr *C.int, startFn func(ctx context.Context) error) C.int {
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
				return C.int(0)
			}
		}
	}
}

// runServerFromFile runs frps using a configuration file
func runServerFromFile(ctx context.Context, cfgFile string) error {
	svrCfg, _, err := config.LoadServerConfig(cfgFile, true)
	if err != nil {
		return err
	}

	validator := validation.NewConfigValidator(nil)
	warning, err := validator.ValidateServerConfig(svrCfg)
	if warning != nil {
		log.Warnf("WARNING: %v", warning)
	}
	if err != nil {
		return err
	}

	return runServerWithConfig(ctx, svrCfg)
}

// startServerWithParams starts frps with basic parameters
func startServerWithParams(
	ctx context.Context,
	token string,
	bindAddr string,
	bindPort int,
	logFile string,
	logLevel string,
	logMaxDays int,
) error {
	cfg := &v1.ServerConfig{
		BindAddr: bindAddr,
		BindPort: bindPort,
		Log: v1.LogConfig{
			To:      logFile,
			Level:   logLevel,
			MaxDays: int64(logMaxDays),
		},
	}

	if token != "" {
		cfg.Auth = v1.AuthServerConfig{
			Method: v1.AuthMethodToken,
			Token:  token,
		}
	}

	// Complete configuration with defaults
	if err := cfg.Complete(); err != nil {
		return err
	}

	// Validate configuration
	validator := validation.NewConfigValidator(nil)
	warning, err := validator.ValidateServerConfig(cfg)
	if warning != nil {
		log.Warnf("WARNING: %v", warning)
	}
	if err != nil {
		return err
	}

	return runServerWithConfig(ctx, cfg)
}

// runServerWithConfig runs frps with a ServerConfig
func runServerWithConfig(ctx context.Context, cfg *v1.ServerConfig) error {
	log.InitLogger(cfg.Log.To, cfg.Log.Level, int(cfg.Log.MaxDays), cfg.Log.DisablePrintColor)

	svr, err := server.NewService(cfg)
	if err != nil {
		return err
	}

	log.Infof("frps started successfully")
	svr.Run(ctx)
	return nil
}

func main() {
	system.EnableCompatibilityMode()
	Execute()
}
