package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	relayconfig "yuanbohan/tunnel/internal/config"
	stunserver "yuanbohan/tunnel/internal/connectivity/stun"
	"yuanbohan/tunnel/internal/logx"
)

func main() {
	if err := run(os.Args[1:], defaultRuntimeEnv()); err != nil {
		log.Fatal(err)
	}
}

func logRelayStarted(listenAddr, stunListenAddr string) {
	fields := []logx.Field{logx.String("listen_addr", listenAddr)}
	if stunListenAddr != "" {
		fields = append(fields, logx.String("stun_listen_addr", stunListenAddr))
	}
	logx.Info("relay_started", fields...)
}

func startRelay(
	ctx context.Context,
	handler http.Handler,
	listen func(network, address string) (net.Listener, error),
	listenPacket func(network, address string) (net.PacketConn, error),
	serve func(*http.Server, net.Listener) error,
) error {
	ln, err := listen("tcp", relayconfig.RelayListenAddr())
	if err != nil {
		return err
	}
	defer ln.Close()

	var stunConn net.PacketConn
	if stunListenAddr := relayconfig.RelaySTUNListenAddr(); stunListenAddr != "" {
		if listenPacket == nil {
			listenPacket = net.ListenPacket
		}
		stunConn, err = listenPacket("udp", stunListenAddr)
		if err != nil {
			return fmt.Errorf("bind STUN UDP listener %q failed; set --stun-listen-addr/RELAY_STUN_LISTEN_ADDR to another address or \"off\" to disable: %w", stunListenAddr, err)
		}
	}

	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if stunConn != nil {
		defer stunConn.Close()
		go func() {
			if err := (&stunserver.Server{}).Serve(serveCtx, stunConn); err != nil && serveCtx.Err() == nil {
				logx.Warn("stun_server_stopped", logx.String("error", err.Error()))
			}
		}()
	}

	boundSTUNAddr := ""
	if stunConn != nil {
		boundSTUNAddr = stunConn.LocalAddr().String()
	}
	logRelayStarted(ln.Addr().String(), boundSTUNAddr)
	return serve(newHTTPServer(handler), ln)
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              relayconfig.RelayListenAddr(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
