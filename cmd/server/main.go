package main

import (
	"flag"
	"log"
	"net/http"

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
		// Load validates analyze fields; for server use softer path
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
	}
	addr := cfg.Server.ListenAddr
	log.Printf("listening on %s (POST /slack/interactions)", addr)
	if err := http.ListenAndServe(addr, h.Mux()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
