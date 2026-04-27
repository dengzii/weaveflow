package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"weaveflow/internal/neo"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms/openai"
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	dataDir := flag.String("data", "neo_data", "data directory for memory and run artifacts")
	flag.Parse()

	model, err := openai.New()
	if err != nil {
		log.Fatal(err)
	}

	buildCtx := neo.NewBuildContext(model, *dataDir)
	srv := neo.NewServer(buildCtx, neo.DefaultConfig(), *dataDir)

	engine := gin.Default()
	srv.RegisterRoutes(engine.Group("/neo"))

	display := *addr
	if strings.HasPrefix(display, ":") {
		display = "http://127.0.0.1" + display
	}
	fmt.Printf("neo agent: %s/neo/\n", display)

	if err := engine.Run(*addr); err != nil {
		log.Fatal(err)
	}
}
