package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/jay/da-agents/internal/config"
	"github.com/jay/da-agents/internal/httpapi"
	"github.com/jay/da-agents/internal/pipeline"
	appruntime "github.com/jay/da-agents/internal/runtime"
)

func main() {
	product := flag.String("product", "goodnight", "config product name under config/<product>/")
	flag.Parse()

	log.Printf("cmd/server env=%s", appruntime.EnvName())
	cfg, err := config.Load(*product)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.ValidateServer(); err != nil {
		log.Fatalf("config: %v", err)
	}

	fb, err := pipeline.OpenFeedback(cfg)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer fb.Close()

	h := &httpapi.Handler{
		SigningSecret: cfg.Slack.SigningSecret,
		Feedback:      fb,
		EnqueueReplay: func(_ context.Context, job pipeline.ReplayJob) error {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				defer cancel()
				fresh, err := config.Load(*product)
				if err == nil {
					err = pipeline.ExecuteReplay(ctx, fresh, job)
				}
				pipeline.NotifyReplayResult(cfg, job, err)
			}()
			return nil
		},
	}
	addr := cfg.Server.ListenAddr
	log.Printf("listening on %s (POST /slack/interactions)", addr)
	if err := http.ListenAndServe(addr, h.Mux()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
