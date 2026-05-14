package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	stunwire "yuanbohan/tunnel/internal/connectivity/stun"
)

func TestRunChecksBindingServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket server: %v", err)
	}
	go func() {
		_ = (&stunwire.Server{}).Serve(ctx, serverConn)
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = run([]string{"--timeout", "250ms", "--retries", "2", serverConn.LocalAddr().String()}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v\nstderr: %s", err, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "target=") || !strings.Contains(got, "mapped=") || !strings.Contains(got, "local=") {
		t.Fatalf("stdout = %q, want target/mapped/local details", got)
	}
}

func TestRunReportsUDPTimeout(t *testing.T) {
	blackhole, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket blackhole: %v", err)
	}
	defer blackhole.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = run([]string{"--timeout", "20ms", "--retries", "1", blackhole.LocalAddr().String()}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "UDP timeout waiting for valid STUN Binding response") {
		t.Fatalf("error = %q, want timeout context", err.Error())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty output on failure", stdout.String())
	}

	time.Sleep(10 * time.Millisecond)
}
