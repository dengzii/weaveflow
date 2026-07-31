package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/server"
	"github.com/dengzii/weaveflow/llms/openai"
	"github.com/dengzii/weaveflow/memory"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/tools"

	"github.com/gin-gonic/gin"
	"github.com/tmc/langchaingo/llms"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data", ".local/server", "data directory for graph debug runs")
	prefix := flag.String("prefix", "", "route prefix")
	corsOrigins := flag.String("cors-origins", defaultCORSOrigins, "comma-separated WebUI origins allowed by CORS; use * to allow all")
	graphPath := flag.String("graph", "", "optional graph definition JSON file to preload")
	logLevel := flag.String("log-level", "info", "log level: debug, info, or error")
	flag.Parse()
	level, err := parseLogLevel(*logLevel)
	if err != nil {
		log.Fatal(err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	var graph *wfgraph.Graph
	if strings.TrimSpace(*graphPath) != "" {
		loaded, err := wfgraph.LoadGraphFromFile(&wfregistry.BuildContext{}, *graphPath)
		if err != nil {
			log.Fatal(err)
		}
		graph = loaded
	}

	baseCtx := newRuntimeContext()
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		model, err := openai.New()
		if err != nil {
			log.Fatal(err)
		}
		baseCtx = withModelServices(baseCtx, model, *dataDir)
		fmt.Printf("model service: openai")
		if name := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); name != "" {
			fmt.Printf(" (%s)", name)
		}
		fmt.Println()
	} else {
		fmt.Println("model service: disabled (set OPENAI_API_KEY to enable LLM/agent nodes)")
	}

	srv, err := server.New(baseCtx, server.Config{
		Graph:   graph,
		BaseDir: *dataDir,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.Start(baseCtx); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	engine := gin.Default()
	engine.Use(corsMiddleware(*corsOrigins))
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

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level %q: expected debug, info, or error", value)
	}
}

func newRuntimeContext() context.Context {
	ctx := context.Background()
	return core.WithTools(ctx, defaultTools())
}

func withModelServices(ctx context.Context, model llms.Model, baseDir string) context.Context {
	repo := memory.NewFileMemoryRepository(filepath.Join(baseDir, "memory"))
	ctx = core.WithModels(ctx, map[string]llms.Model{core.DefaultModelID: model})
	return core.WithMemory(ctx, memory.New(&memory.Options{Repository: repo, Retriever: memory.NewBM25Retriever(repo, nil)}))
}

func defaultTools() map[string]core.Tool {
	return map[string]core.Tool{
		"read":         tools.NewRead(),
		"write":        tools.NewWrite(),
		"edit":         tools.NewEdit(),
		"glob":         tools.NewGlob(),
		"grep":         tools.NewGrep(),
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
		"web_fetch":    tools.NewWebFetch(),
	}
}
