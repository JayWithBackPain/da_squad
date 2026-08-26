package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/jay/da-agents/internal/config"
	"github.com/jay/da-agents/internal/httpapi"
	"github.com/jay/da-agents/internal/pipeline"
	appruntime "github.com/jay/da-agents/internal/runtime"
)

var adapter *httpadapter.HandlerAdapterV2

func init() {
	log.Printf("lambda-slack init env=%s", appruntime.EnvName())
	product := os.Getenv("DA_AGENT_PRODUCT")
	if product == "" {
		product = "goodnight"
	}
	cfg, err := config.Load(product)
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
	h := &httpapi.Handler{
		SigningSecret: cfg.Slack.SigningSecret,
		Feedback:      fb,
	}
	adapter = httpadapter.NewV2(h.Mux())
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return adapter.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(handler)
}
