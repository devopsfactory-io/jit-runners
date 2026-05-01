package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/devopsfactory-io/jit-runners/lambda/internal/lifecycle"
	"github.com/devopsfactory-io/jit-runners/lambda/internal/provider"
)

func main() {
	ctx := context.Background()

	bundle, err := provider.New(ctx, os.Getenv("CLOUD_PROVIDER"))
	if err != nil {
		log.Fatalf("provider.New: %v", err)
	}
	defer bundle.Close()

	h := lifecycle.New(bundle.State, bundle.GitHub, log.Default())

	lambda.Start(func(ctx context.Context, ev events.SQSEvent) error {
		for _, rec := range ev.Records {
			if err := h.HandleSQS(ctx, []byte(rec.Body)); err != nil {
				return err
			}
		}
		return nil
	})
}
