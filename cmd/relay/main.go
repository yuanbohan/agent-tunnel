package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"yuanbohan/tunnel/relay"
)

type mainConfig struct {
	ListenAddr string
	User       string
	Password   string
	AgentToken string
}

func loadMainConfig(getenv func(string) string, portFlag string) (mainConfig, error) {
	cfg := mainConfig{
		User:       getenv("AGENTUNNEL_BASIC_USER"),
		Password:   getenv("AGENTUNNEL_BASIC_PASSWORD"),
		AgentToken: getenv("AGENTUNNEL_AGENT_TOKEN"),
	}
	port := "8586"
	if portFlag != "" {
		port = portFlag
	}
	cfg.ListenAddr = net.JoinHostPort("0.0.0.0", port)
	if cfg.User == "" || cfg.Password == "" || cfg.AgentToken == "" {
		return mainConfig{}, fmt.Errorf("AGENTUNNEL_BASIC_USER, AGENTUNNEL_BASIC_PASSWORD, and AGENTUNNEL_AGENT_TOKEN are required")
	}

	return cfg, nil
}

func main() {
	port := flag.String("port", "", "listen port")
	flag.Parse()

	cfg, err := loadMainConfig(os.Getenv, *port)
	if err != nil {
		log.Fatal(err)
	}

	logger := relay.NewLogger(os.Stderr)

	handler := relay.NewHandler(relay.HandlerConfig{
		Registry:   relay.NewRegistry(),
		User:       cfg.User,
		Password:   cfg.Password,
		AgentToken: cfg.AgentToken,
		Logger:     logger,
	})

	log.Fatal(startRelay(cfg, handler, logger, net.Listen, func(srv *http.Server, ln net.Listener) error {
		return srv.Serve(ln)
	}))
}

func logRelayStarted(logger *relay.Logger, listenAddr string) {
	logger.Info("relay_started", relay.String("listen_addr", listenAddr))
}

func startRelay(
	cfg mainConfig,
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

func newHTTPServer(cfg mainConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
