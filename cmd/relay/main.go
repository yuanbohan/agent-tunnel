package main

import (
	"fmt"
	"log"
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

func loadMainConfig(getenv func(string) string) (mainConfig, error) {
	cfg := mainConfig{
		ListenAddr:      getenv("AGENTUNNEL_RELAY_ADDR"),
		BrowserUser:     getenv("AGENTUNNEL_BASIC_USER"),
		BrowserPassword: getenv("AGENTUNNEL_BASIC_PASSWORD"),
		AgentToken:      getenv("AGENTUNNEL_AGENT_TOKEN"),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8586"
	}
	if cfg.BrowserUser == "" || cfg.BrowserPassword == "" || cfg.AgentToken == "" {
		return mainConfig{}, fmt.Errorf("AGENTUNNEL_BASIC_USER, AGENTUNNEL_BASIC_PASSWORD, and AGENTUNNEL_AGENT_TOKEN are required")
	}

	return cfg, nil
}

func main() {
	cfg, err := loadMainConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	handler := relay.NewHandler(relay.HandlerConfig{
		Registry:        relay.NewRegistry(),
		BrowserUser:     cfg.BrowserUser,
		BrowserPassword: cfg.BrowserPassword,
		AgentToken:      cfg.AgentToken,
	})

	log.Fatal(newHTTPServer(cfg, handler).ListenAndServe())
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
