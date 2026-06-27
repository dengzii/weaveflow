package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/server"
	wfregistry "github.com/dengzii/weaveflow/registry"

	"github.com/gin-gonic/gin"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data", ".local/server", "data directory for graph debug runs")
	prefix := flag.String("prefix", "", "route prefix")
	graphPath := flag.String("graph", "", "optional graph definition JSON file to preload")
	flag.Parse()

	var graph *wfgraph.Graph
	if strings.TrimSpace(*graphPath) != "" {
		loaded, err := wfgraph.LoadGraphFromFile(&wfregistry.BuildContext{}, *graphPath)
		if err != nil {
			log.Fatal(err)
		}
		graph = loaded
	}

	srv, err := server.New(context.Background(), server.Config{
		Graph:   graph,
		BaseDir: *dataDir,
	})
	if err != nil {
		log.Fatal(err)
	}

	engine := gin.Default()
	routePrefix := normalizePrefix(*prefix)
	srv.RegisterRoutes(engine.Group(routePrefix))

	display := *addr
	if strings.HasPrefix(display, ":") {
		display = "http://127.0.0.1" + display
	}
	fmt.Printf("weaveflow debug server: %s%s\n", strings.TrimRight(display, "/"), routePrefix)
	fmt.Printf("upload graph: POST %s%s/graph\n", strings.TrimRight(display, "/"), routePrefix)

	if err := engine.Run(*addr); err != nil {
		log.Fatal(err)
	}
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}
