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
	"sync"
	"time"
	"unsafe"

	_ "github.com/fatedier/frp/assets/frpc"
	"github.com/fatedier/frp/cmd/frpc/sub"
	"github.com/fatedier/frp/pkg/util/system"
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

func main() {
	system.EnableCompatibilityMode()
	sub.Execute()
}
