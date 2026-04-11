package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"time"

	relayconfig "yuanbohan/tunnel/internal/config"
	"yuanbohan/tunnel/internal/logx"
)

func main() {
	if err := run(os.Args[1:], defaultRuntimeEnv()); err != nil {
		log.Fatal(err)
	}
}

func logRelayStarted(listenAddr string) {
	logx.Info("relay_started", logx.String("listen_addr", listenAddr))
}

func startRelay(
	handler http.Handler,
	listen func(network, address string) (net.Listener, error),
	serve func(*http.Server, net.Listener) error,
) error {
	ln, err := listen("tcp", relayconfig.RelayListenAddr())
	if err != nil {
		return err
	}

	logRelayStarted(ln.Addr().String())
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
