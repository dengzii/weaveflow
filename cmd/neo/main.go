package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/internal/neo"

	"github.com/dengzii/weaveflow/llms/openai"

	"github.com/gin-gonic/gin" ///
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	dataDir := flag.String("data", ".local/neo", "data directory for memory and run artifacts")
	flag.Parse()

	model, err := openai.New()
	if err != nil {
		log.Fatal(err)
	}

	err = os.MkdirAll(*dataDir, 0755)
	if err != nil {
		log.Fatal(err)
	}

	ctx := neo.NewContext(model, *dataDir)
	srv, err := neo.NewServer(ctx, neo.DefaultConfig(), *dataDir)
	if err != nil {
		log.Fatal(err)
	}

	engine := gin.Default()
	srv.RegisterRoutes(engine.Group("/neo"))
	neo.NewReplayServer(*dataDir, srv.Hub()).RegisterGinRoutes(engine.Group("/api"))

	display := *addr
	if strings.HasPrefix(display, ":") {
		display = "http://127.0.0.1" + display
	}
	fmt.Printf("neo agent: %s/neo/\n", display)

	if err := engine.Run(*addr); err != nil {
		log.Fatal(err)
	}
}
