package main

import (
	"context"
	"fmt"
	"os"
	"time"

	llamacpp "weaveflow/llama_cpp"

	"github.com/tmc/langchaingo/llms"
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

	resultCh := make(chan llamacpp.GenerateResult, 1)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "What is the most important think in the world?"),
	}

	streamingFunc := llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
		fmt.Print(string(chunk))
		return nil
	})
	_, err = model.GenerateContent(ctx, messages,
		streamingFunc,
		llms.WithReturnThinking(true),
		llms.WithStopWords([]string{"\n\n"}),
	)

	if err != nil {
		panic(err)
	}

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

	fmt.Println("=======")

	span := time.Since(startAt).Seconds()
	fmt.Printf("cost=%fs\n", span)

	speed := int(float64(finalResult.TokenCount) / span)
	fmt.Printf("stop_reason=%s token_count=%d, speed=%dtokens/s\n", finalResult.StopReason, finalResult.TokenCount, speed)

	_ = model.Release()
}
