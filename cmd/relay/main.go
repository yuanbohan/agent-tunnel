package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"yuanbohan/tunnel/internal/relayserver"
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

	handler := relayserver.NewHandler(relayserver.HandlerConfig{
		Registry:        relayserver.NewRegistry(),
		BrowserUser:     cfg.BrowserUser,
		BrowserPassword: cfg.BrowserPassword,
		AgentToken:      cfg.AgentToken,
	})

	log.Fatal(http.ListenAndServe(cfg.ListenAddr, handler))
}
