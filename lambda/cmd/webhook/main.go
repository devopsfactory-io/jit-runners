package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	appconfig "github.com/devopsfactory-io/jit-runners/lambda/internal/config"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/webhook"
)

var (
	cfgOnce sync.Once
	appCfg  *appconfig.Config
	cfgErr  error

	bundleOnce sync.Once
	bundleRef  *provider.Bundle
	bundleErr  error

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
// Both publishers are obtained from the cloud-agnostic provider.Bundle so
// that the same binary works with AWS (SQS) and GCP (Pub/Sub).
func loadHandler(ctx context.Context) (*webhook.Handler, error) {
	handlerOnce.Do(func() {
		cfg, err := loadConfig(ctx)
		if err != nil {
			handlerErr = fmt.Errorf("load config: %w", err)
			return
		}
		bundleOnce.Do(func() {
			bundleRef, bundleErr = provider.New(ctx, os.Getenv("CLOUD_PROVIDER"))
		})
		if bundleErr != nil {
			handlerErr = fmt.Errorf("provider.New: %w", bundleErr)
			return
		}
		wHandler = webhook.NewHandler(bundleRef.JobsPublisher, bundleRef.LifecyclePublisher, []byte(cfg.WebhookSecret))
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
