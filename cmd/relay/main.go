package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"yuanbohan/tunnel/relay"
)

type mainConfig struct {
	ListenAddr string
	User       string
	Password   string
	AgentToken string
	RedisURL   string
}

func loadMainConfig(getenv func(string) string, portFlag string) (mainConfig, error) {
	cfg := mainConfig{
		User:       getenv("AGENTUNNEL_BASIC_USER"),
		Password:   getenv("AGENTUNNEL_BASIC_PASSWORD"),
		AgentToken: getenv("AGENTUNNEL_AGENT_TOKEN"),
		RedisURL:   getenv("AGENTUNNEL_REDIS_URL"),
	}
	port := "8586"
	if portFlag != "" {
		port = portFlag
	}
	cfg.ListenAddr = net.JoinHostPort("0.0.0.0", port)
	if cfg.User == "" || cfg.Password == "" || cfg.AgentToken == "" || cfg.RedisURL == "" {
		return mainConfig{}, fmt.Errorf("AGENTUNNEL_BASIC_USER, AGENTUNNEL_BASIC_PASSWORD, AGENTUNNEL_AGENT_TOKEN, and AGENTUNNEL_REDIS_URL are required")
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
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal(err)
	}
	redisClient := redis.NewClient(redisOpts)
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		_ = redisClient.Close()
		log.Fatal(err)
	}
	defer redisClient.Close()
	historyStore := relay.NewRedisHistoryStore(redisClient, relay.RedisHistoryStoreConfig{})

	handler := relay.NewHandler(relay.HandlerConfig{
		Registry:   relay.NewRegistryWithHistoryStore(historyStore),
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
