package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jay/da-agents/internal/config"
	"github.com/jay/da-agents/internal/pipeline"
	appruntime "github.com/jay/da-agents/internal/runtime"
)

func main() {
	product := flag.String("product", "goodnight", "config product name under config/<product>/")
	flag.Parse()

	log.Printf("cmd/analyze env=%s", appruntime.EnvName())
	cfg, err := config.Load(*product)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}
	deps, err := pipeline.OpenAnalyze(cfg)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer deps.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	if err := deps.Run(ctx); err != nil {
		log.Fatalf("analyze: %v", err)
	}
}
