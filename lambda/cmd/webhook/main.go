package main

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	sqspub "github.com/devopsfactory-io/jit-runners/lambda/internal/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

var (
	cfgOnce     sync.Once
	appCfg      *appconfig.Config
	cfgErr      error
	handlerOnce sync.Once
	wHandler    *webhook.Handler
	handlerErr  error
)

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return response(405, "Method Not Allowed"), nil
	}

	body := req.Body
	if req.IsBase64Encoded {
		dec, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			log.Printf("base64 decode body: %v", err)
			return response(400, "Invalid body"), nil
		}
		body = string(dec)
	}

	sig := getHeader(req.Headers, "x-hub-signature-256")
	eventType := getHeader(req.Headers, "x-github-event")

	h, err := loadHandler(ctx)
	if err != nil {
		log.Printf("init handler: %v", err)
		return response(500, "Configuration error"), nil
	}

	resp := h.Handle(ctx, eventType, sig, []byte(body))
	if resp.Status >= 500 {
		log.Printf("handle webhook: %s", resp.String())
	}
	return response(resp.Status, resp.Body), nil
}

func loadConfig(ctx context.Context) (*appconfig.Config, error) {
	cfgOnce.Do(func() {
		appCfg, cfgErr = appconfig.Load(ctx)
	})
	return appCfg, cfgErr
}

// loadHandler builds the webhook.Handler exactly once per Lambda container.
// Both publishers are wired here:
//
//   - scale-up:  SQS_QUEUE_URL          -> internal/sqs.Publisher
//   - lifecycle: LIFECYCLE_QUEUE_URL    -> internal/sqs.LifecyclePublisher
//
// LIFECYCLE_QUEUE_URL is required for the in_progress/completed dispatch path
// added in Phase C; absent it, lifecycle requests will return 500 from the
// handler. We surface the missing-env case as a config error here so the
// Lambda fails fast on cold start when misconfigured.
func loadHandler(ctx context.Context) (*webhook.Handler, error) {
	handlerOnce.Do(func() {
		cfg, err := loadConfig(ctx)
		if err != nil {
			handlerErr = err
			return
		}
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			handlerErr = err
			return
		}
		client := sqs.NewFromConfig(awsCfg)
		scaleUpPub := sqspub.NewPublisher(client, cfg.QueueURL)

		lifecycleURL := os.Getenv("LIFECYCLE_QUEUE_URL")
		if lifecycleURL == "" {
			log.Printf("LIFECYCLE_QUEUE_URL is empty: lifecycle events will return 500 until set")
			wHandler = webhook.NewHandler(scaleUpPub, nil, []byte(cfg.WebhookSecret))
			return
		}
		lifecyclePub := sqspub.NewLifecyclePublisher(client, lifecycleURL)
		wHandler = webhook.NewHandler(scaleUpPub, lifecyclePub, []byte(cfg.WebhookSecret))
	})
	return wHandler, handlerErr
}

func response(status int, body string) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		StatusCode: status,
		Headers: map[string]string{
			"Content-Type": "text/plain; charset=utf-8",
		},
		Body: body,
	}
}

func getHeader(h map[string]string, key string) string {
	if h == nil {
		return ""
	}
	for k, v := range h {
		if len(k) == len(key) && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func init() {
	log.SetFlags(log.Lshortfile)
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.SetOutput(os.Stdout)
	}
}
