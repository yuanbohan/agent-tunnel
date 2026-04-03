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
	ListenAddr      string
	BrowserUser     string
	BrowserPassword string
	AgentToken      string
}

func loadMainConfig(getenv func(string) string, portFlag string) (mainConfig, error) {
	cfg := mainConfig{
		BrowserUser:     getenv("AGENTUNNEL_BASIC_USER"),
		BrowserPassword: getenv("AGENTUNNEL_BASIC_PASSWORD"),
		AgentToken:      getenv("AGENTUNNEL_AGENT_TOKEN"),
	}
	port := "8586"
	if portFlag != "" {
		port = portFlag
	}
	cfg.ListenAddr = net.JoinHostPort("0.0.0.0", port)
	if cfg.BrowserUser == "" || cfg.BrowserPassword == "" || cfg.AgentToken == "" {
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
	logRelayStarted(logger, cfg)

	handler := relay.NewHandler(relay.HandlerConfig{
		Registry:        relay.NewRegistry(),
		BrowserUser:     cfg.BrowserUser,
		BrowserPassword: cfg.BrowserPassword,
		AgentToken:      cfg.AgentToken,
		Logger:          logger,
	})

	log.Fatal(newHTTPServer(cfg, handler).ListenAndServe())
}

func logRelayStarted(logger *relay.Logger, cfg mainConfig) {
	logger.Info("relay_started", relay.String("listen_addr", cfg.ListenAddr))
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
