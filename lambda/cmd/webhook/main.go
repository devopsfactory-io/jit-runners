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
	"github.com/devopsfactory-io/jit-runners/lambda/internal/github"
	sqspub "github.com/devopsfactory-io/jit-runners/lambda/internal/sqs"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error
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

	cfg, err := loadConfig(ctx)
	if err != nil {
		log.Printf("load config: %v", err)
		return response(500, "Configuration error"), nil
	}

	if err := github.VerifyWebhookSignature([]byte(body), sig, cfg.WebhookSecret); err != nil {
		log.Printf("verify signature: %v", err)
		return response(401, "Invalid signature"), nil
	}

	if eventType != "workflow_job" {
		return response(200, "OK"), nil
	}

	result, err := webhook.Parse([]byte(body))
	if err != nil {
		log.Printf("parse workflow_job: %v", err)
		return response(400, "Bad payload"), nil
	}

	if !result.ShouldScale {
		log.Printf("workflow_job %s action=%s: no scaling needed", result.Event.Repository.FullName, result.Action)
		return response(200, "OK"), nil
	}

	// Publish scale-up message to SQS.
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("load AWS config: %v", err)
		return response(500, "AWS config error"), nil
	}
	publisher := sqspub.NewPublisher(sqs.NewFromConfig(awsCfg), cfg.QueueURL)

	msg := &sqspub.ScaleUpMessage{
		EventAction:    result.Action,
		JobID:          result.Event.WorkflowJob.ID,
		RunID:          result.Event.WorkflowJob.RunID,
		RepositoryFull: result.Event.Repository.FullName,
		Labels:         result.Event.WorkflowJob.Labels,
		InstallationID: result.Event.Installation.ID,
	}
	if err := publisher.Publish(ctx, msg); err != nil {
		log.Printf("publish SQS message: %v", err)
		return response(500, "Queue error"), nil
	}

	log.Printf("queued scale-up for %s job=%d", msg.RepositoryFull, msg.JobID)
	return response(200, "OK"), nil
}

func loadConfig(ctx context.Context) (*appconfig.Config, error) {
	cfgOnce.Do(func() {
		appCfg, cfgErr = appconfig.Load(ctx)
	})
	return appCfg, cfgErr
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
