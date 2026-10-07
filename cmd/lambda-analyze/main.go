package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jay/da-agents/internal/config"
	"github.com/jay/da-agents/internal/pipeline"
	appruntime "github.com/jay/da-agents/internal/runtime"
)

func handler(ctx context.Context, event map[string]any) error {
	log.Printf("lambda-analyze env=%s", appruntime.EnvName())
	product := os.Getenv("DA_AGENT_PRODUCT")
	if product == "" {
		product = "goodnight"
	}
	cfg, err := config.Load(product)
	if err != nil {
		return err
	}
	if date, ok := event["report_date"].(string); ok {
		cfg.Analyze.ReportDate = date
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	deps, err := pipeline.OpenAnalyze(cfg)
	if err != nil {
		return err
	}
	defer deps.Close()
	return deps.Run(ctx)
}

func main() {
	lambda.Start(handler)
}
