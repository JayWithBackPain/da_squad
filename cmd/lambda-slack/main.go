package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jay/da-agents/internal/memory"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/jay/da-agents/internal/config"
	"github.com/jay/da-agents/internal/httpapi"
	"github.com/jay/da-agents/internal/pipeline"
	appruntime "github.com/jay/da-agents/internal/runtime"
)

// distillMarker tags an event as our async "process this correction" job rather
// than an HTTP request forwarded by the Function URL / API Gateway.
const distillMarker = "distill_correction"

// asyncEvent is the payload we self-invoke with (InvocationType=Event). It embeds
// CorrectionJob so the fields flatten into the same top-level JSON object.
type asyncEvent struct {
	DAJob string `json:"da_job"`
	pipeline.CorrectionJob
}

var (
	feedback     *pipeline.FeedbackDeps
	adapter      *httpadapter.HandlerAdapterV2
	lambdaClient *awslambda.Client
	replayCfg    *config.Config
	selfName     = os.Getenv("AWS_LAMBDA_FUNCTION_NAME") // set by the Lambda runtime
)

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
	replayCfg = cfg
	feedback, err = pipeline.OpenFeedback(cfg)
	if err != nil {
		log.Fatalf("open: %v", err)
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	lambdaClient = awslambda.NewFromConfig(awsCfg)

	h := &httpapi.Handler{
		SigningSecret:     cfg.Slack.SigningSecret,
		Feedback:          feedback,
		EnqueueCorrection: enqueueCorrection,
		EnqueueReplay:     enqueueReplay,
	}
	adapter = httpadapter.NewV2(h.Mux())
}

// enqueueCorrection fires an async (Event) self-invoke so the modal path can ack
// within Slack's 3s window. Requires the execution role to allow
// lambda:InvokeFunction on this function.
func enqueueCorrection(ctx context.Context, job pipeline.CorrectionJob) error {
	payload, err := json.Marshal(asyncEvent{DAJob: distillMarker, CorrectionJob: job})
	if err != nil {
		return err
	}
	_, err = lambdaClient.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   &selfName,
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        payload,
	})
	return err
}

func enqueueReplay(ctx context.Context, job pipeline.ReplayJob) error {
	payload, err := json.Marshal(struct {
		DAJob string `json:"da_job"`
		pipeline.ReplayJob
	}{"replay_day", job})
	if err != nil {
		return err
	}
	_, err = lambdaClient.Invoke(ctx, &awslambda.InvokeInput{FunctionName: &selfName, InvocationType: lambdatypes.InvocationTypeEvent, Payload: payload})
	return err
}

// handler dispatches on event shape: our async distill job vs an HTTP request.
func handler(ctx context.Context, raw json.RawMessage) (any, error) {
	var probe struct {
		DAJob string `json:"da_job"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.DAJob == "replay_day" {
		var job pipeline.ReplayJob
		if err := json.Unmarshal(raw, &job); err != nil {
			return nil, err
		}
		err := pipeline.ExecuteReplay(ctx, replayCfg, job)
		pipeline.NotifyReplayResult(replayCfg, job, err)
		// Review-gate rejections are user decisions, not AWS retryable jobs. A claimed
		// analysis failure is durably blocked and requires explicit operator recovery.
		if err != nil {
			log.Printf("replay: %v", err)
		}
		if errors.Is(err, memory.ErrReplayReview) {
			return nil, nil
		}
		return nil, err
	}
	if probe.DAJob == distillMarker {
		var evt asyncEvent
		if err := json.Unmarshal(raw, &evt); err != nil {
			return nil, err
		}
		return nil, feedback.ProcessCorrection(ctx, evt.CorrectionJob)
	}

	var req events.APIGatewayV2HTTPRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return adapter.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(handler)
}
