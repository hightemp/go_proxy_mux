package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hightemp/go_proxy_mux/internal/config"
	"github.com/hightemp/go_proxy_mux/internal/proxy"
)

func main() {
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	server := proxy.NewProxyServer(cfg)

	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Failed to start proxy server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down proxy server...")
}
