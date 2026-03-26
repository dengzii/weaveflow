package main

import (
	"context"
	"fmt"
	"os"
	"time"

	llamacpp "falcon/llama_cpp"
)

func main() {
	llamaModelPath, ok := os.LookupEnv("MODEL_PATH")
	if !ok {
		panic("MODEL_PATH is not found in env")
	}

	model, err := llamacpp.Load(llamaModelPath,
		llamacpp.LoadOptions{},
	)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	startAt := time.Now()

	resultCh, errCh := model.Generate(ctx,
		"User: What is the most important think in the world?\n\nAssistant: <think>",
		llamacpp.GenerateOptions{},
	)

	finalResult := llamacpp.GenerateResult{}
	for result := range resultCh {
		if result.Content != "" {
			fmt.Print(result.Content)
		}
		if result.StopReason != llamacpp.StopReasonNone {
			finalResult = result
		}
	}

	fmt.Println()
	if err, ok := <-errCh; ok && err != nil {
		panic(err)
	}

	fmt.Println("=======")

	span := time.Since(startAt).Seconds()
	fmt.Printf("cost=%fs\n", span)

	speed := int(float64(finalResult.TokenCount) / span)
	fmt.Printf("stop_reason=%s token_count=%d, speed=%dtokens/s\n", finalResult.StopReason, finalResult.TokenCount, speed)

	_ = model.Release()
}
