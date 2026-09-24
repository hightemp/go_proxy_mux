package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/hightemp/go_proxy_mux/internal/config"
	"github.com/hightemp/go_proxy_mux/internal/proxy"
)

func main() {
	configFile := flag.String("config", "config.yaml", "Path to configuration file")
	envFile := flag.String("env", ".env", "Path to optional environment file (empty to disable)")
	flag.Parse()
	cfg, err := config.LoadConfigWithEnv(*configFile, *envFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := proxy.Run(ctx, cfg); err != nil {
		log.Fatalf("Proxy server stopped: %v", err)
	}
}
