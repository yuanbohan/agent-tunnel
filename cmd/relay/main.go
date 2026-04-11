package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"yuanbohan/tunnel/internal/relay"
)

func main() {
	if err := run(os.Args[1:], defaultRuntimeEnv()); err != nil {
		log.Fatal(err)
	}
}

func logRelayStarted(logger *relay.Logger, listenAddr string) {
	logger.Info("relay_started", relay.String("listen_addr", listenAddr))
}

func startRelay(
	cfg serveConfig,
	handler http.Handler,
	logger *relay.Logger,
	listen func(network, address string) (net.Listener, error),
	serve func(*http.Server, net.Listener) error,
) error {
	ln, err := listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}

	logRelayStarted(logger, ln.Addr().String())
	return serve(newHTTPServer(cfg, handler), ln)
}

func newHTTPServer(cfg serveConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
