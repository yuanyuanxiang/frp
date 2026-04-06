// Copyright 2026 The frp Authors
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

package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/log"
)

func TestWriteWithDeadlineTimesOutAndClearsDeadline(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	err := writeWithDeadline(serverConn, 50*time.Millisecond, func() error {
		_, writeErr := serverConn.Write([]byte("x"))
		return writeErr
	})
	require.Error(t, err)

	var netErr net.Error
	require.True(t, errors.As(err, &netErr))
	require.True(t, netErr.Timeout())

	readCh := make(chan byte, 1)
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		if _, readErr := clientConn.Read(buf); readErr != nil {
			errCh <- readErr
			return
		}
		readCh <- buf[0]
	}()

	_, err = serverConn.Write([]byte("y"))
	require.NoError(t, err)

	select {
	case b := <-readCh:
		require.Equal(t, byte('y'), b)
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for write after deadline reset")
	}
}

func TestServiceRun_ContextCancel(t *testing.T) {
	log.InitLogger("console", "info", 3, true)

	cfg := &v1.ServerConfig{
		BindAddr: "127.0.0.1",
		BindPort: 17000,
	}
	cfg.Complete()

	svr, err := NewService(cfg)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		svr.Run(ctx)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	t.Log("Service started, cancelling context...")

	cancel()

	select {
	case <-done:
		t.Log("Service exited successfully after context cancel")
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout: service did not exit within 3 seconds after context cancel")
	}
}
