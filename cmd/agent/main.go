package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"yuanbohan/tunnel/internal/agent"
)

func main() {
	port := flag.Int("port", 8585, "port to listen on")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", agent.Handler)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("shutting down")
		srv.Shutdown(context.Background())
	}()

	log.Printf("listening on :%d", *port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
