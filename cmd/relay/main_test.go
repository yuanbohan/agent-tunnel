package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	relayconfig "yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/logx"
)

func TestLogRelayStartedWritesListenAddr(t *testing.T) {
	var buf bytes.Buffer
	restore := logx.UseWriterForTest(&buf)
	defer restore()

	logRelayStarted("127.0.0.1:8586", "127.0.0.1:3478")

	got := buf.String()
	if !strings.Contains(got, `"event":"relay_started"`) {
		t.Fatalf("log = %q, want event relay_started", got)
	}
	if !strings.Contains(got, `"listen_addr":"127.0.0.1:8586"`) {
		t.Fatalf("log = %q, want listen_addr 127.0.0.1:8586", got)
	}
	if !strings.Contains(got, `"stun_listen_addr":"127.0.0.1:3478"`) {
		t.Fatalf("log = %q, want stun_listen_addr 127.0.0.1:3478", got)
	}
}

func TestLogSTUNStartedWritesListenAddr(t *testing.T) {
	var buf bytes.Buffer
	restore := logx.UseWriterForTest(&buf)
	defer restore()

	logSTUNStarted("127.0.0.1:3478")

	got := buf.String()
	if !strings.Contains(got, `"event":"stun_started"`) {
		t.Fatalf("log = %q, want event stun_started", got)
	}
	if !strings.Contains(got, `"listen_addr":"127.0.0.1:3478"`) {
		t.Fatalf("log = %q, want listen_addr 127.0.0.1:3478", got)
	}
}

func TestStartRelayDoesNotLogBeforeBind(t *testing.T) {
	var buf bytes.Buffer
	restoreLogs := logx.UseWriterForTest(&buf)
	defer restoreLogs()
	restoreCfg := relayconfig.UseRelayForTest(relayconfig.Relay{ListenAddr: "127.0.0.1:8586", STUNListenAddr: "off"})
	defer restoreCfg()

	err := startRelay(
		context.Background(),
		http.NewServeMux(),
		func(string, string) (net.Listener, error) {
			return nil, errors.New("bind failed")
		},
		net.ListenPacket,
		func(*http.Server, net.Listener) error {
			t.Fatal("serve should not be called when bind fails")
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected bind failure")
	}
	if got := buf.String(); got != "" {
		t.Fatalf("log = %q, want no startup log on bind failure", got)
	}
}

func TestStartSTUNDoesNotLogBeforeBind(t *testing.T) {
	var buf bytes.Buffer
	restoreLogs := logx.UseWriterForTest(&buf)
	defer restoreLogs()

	err := startSTUN(
		context.Background(),
		"127.0.0.1:3478",
		func(string, string) (net.PacketConn, error) {
			return nil, errors.New("stun bind failed")
		},
	)
	if err == nil {
		t.Fatal("expected STUN bind failure")
	}
	if !strings.Contains(err.Error(), `bind STUN UDP listener "127.0.0.1:3478" failed`) {
		t.Fatalf("error = %q, want bind failure context", err.Error())
	}
	if got := buf.String(); got != "" {
		t.Fatalf("log = %q, want no startup log on STUN bind failure", got)
	}
}

func TestStartSTUNLogsBoundListenerAddr(t *testing.T) {
	var buf bytes.Buffer
	restoreLogs := logx.UseWriterForTest(&buf)
	defer restoreLogs()

	ctx, cancel := context.WithCancel(context.Background())
	connReady := make(chan net.PacketConn, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- startSTUN(ctx, "127.0.0.1:0", func(network, address string) (net.PacketConn, error) {
			conn, err := net.ListenPacket(network, address)
			if err == nil {
				connReady <- conn
			}
			return conn, err
		})
	}()

	var boundAddr string
	select {
	case conn := <-connReady:
		boundAddr = conn.LocalAddr().String()
	case err := <-errCh:
		t.Fatalf("startSTUN returned before binding: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for STUN bind")
	}

	deadline := time.Now().Add(time.Second)
	for !strings.Contains(buf.String(), `"event":"stun_started"`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("startSTUN returned error: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, `"listen_addr":"`+boundAddr+`"`) {
		t.Fatalf("log = %q, want bound listen_addr %q", got, boundAddr)
	}
	if strings.Contains(got, `"listen_addr":"127.0.0.1:0"`) {
		t.Fatalf("log = %q, want actual bound address instead of configured :0 address", got)
	}
}

func TestStartRelayDoesNotLogBeforeSTUNBind(t *testing.T) {
	var buf bytes.Buffer
	restoreLogs := logx.UseWriterForTest(&buf)
	defer restoreLogs()
	restoreCfg := relayconfig.UseRelayForTest(relayconfig.Relay{ListenAddr: "127.0.0.1:0", STUNListenAddr: "127.0.0.1:3478"})
	defer restoreCfg()

	err := startRelay(
		context.Background(),
		http.NewServeMux(),
		net.Listen,
		func(string, string) (net.PacketConn, error) {
			return nil, errors.New("stun bind failed")
		},
		func(*http.Server, net.Listener) error {
			t.Fatal("serve should not be called when STUN bind fails")
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected STUN bind failure")
	}
	if got := buf.String(); got != "" {
		t.Fatalf("log = %q, want no startup log on STUN bind failure", got)
	}
}

func TestStartRelayLogsBoundListenerAddr(t *testing.T) {
	var buf bytes.Buffer
	restoreLogs := logx.UseWriterForTest(&buf)
	defer restoreLogs()
	restoreCfg := relayconfig.UseRelayForTest(relayconfig.Relay{ListenAddr: "127.0.0.1:0", STUNListenAddr: "127.0.0.1:0"})
	defer restoreCfg()

	var servedAddr string
	err := startRelay(
		context.Background(),
		http.NewServeMux(),
		net.Listen,
		net.ListenPacket,
		func(_ *http.Server, ln net.Listener) error {
			servedAddr = ln.Addr().String()
			return ln.Close()
		},
	)
	if err != nil {
		t.Fatalf("startRelay returned error: %v", err)
	}
	if servedAddr == "" {
		t.Fatal("servedAddr = empty, want bound listener address")
	}

	got := buf.String()
	if !strings.Contains(got, `"listen_addr":"`+servedAddr+`"`) {
		t.Fatalf("log = %q, want bound listen_addr %q", got, servedAddr)
	}
	if strings.Contains(got, `"listen_addr":"127.0.0.1:0"`) {
		t.Fatalf("log = %q, want actual bound address instead of configured :0 address", got)
	}
	if !strings.Contains(got, `"stun_listen_addr":"`) {
		t.Fatalf("log = %q, want bound STUN address", got)
	}
}

func TestNewHTTPServerConfiguresTimeouts(t *testing.T) {
	restoreCfg := relayconfig.UseRelayForTest(relayconfig.Relay{ListenAddr: "127.0.0.1:8586", STUNListenAddr: "off"})
	defer restoreCfg()

	srv := newHTTPServer(http.NewServeMux())

	if srv.Addr != "127.0.0.1:8586" {
		t.Fatalf("Addr = %q, want 127.0.0.1:8586", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %v, want > 0", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout = %v, want > 0", srv.ReadTimeout)
	}
	if srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %v, want > 0", srv.WriteTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, want > 0", srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout > srv.ReadTimeout {
		t.Fatalf("ReadHeaderTimeout = %v, want <= ReadTimeout %v", srv.ReadHeaderTimeout, srv.ReadTimeout)
	}
	if srv.IdleTimeout < 30*time.Second {
		t.Fatalf("IdleTimeout = %v, want >= 30s", srv.IdleTimeout)
	}
}
