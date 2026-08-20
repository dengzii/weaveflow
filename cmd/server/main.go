package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/server"
	claudenode "github.com/dengzii/weaveflow/node/agents/claude"
	codexnode "github.com/dengzii/weaveflow/node/agents/codex"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/tools"

	"github.com/gin-gonic/gin"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dataDir := flag.String("data", ".local/server", "data directory for graph debug runs")
	secretDir := flag.String("secret-dir", "", "directory for file-backed secret references")
	prefix := flag.String("prefix", "", "route prefix")
	corsOrigins := flag.String("cors-origins", defaultCORSOrigins, "comma-separated WebUI origins allowed by CORS; use * to allow all")
	graphPath := flag.String("graph", "", "optional graph definition JSON file to preload")
	logLevel := flag.String("log-level", "debug", "log level: debug, info, or error")
	flag.Parse()
	managementToken := strings.TrimSpace(os.Getenv("WEAVEFLOW_MANAGEMENT_TOKEN"))
	if !isLoopbackListenAddress(*addr) && managementToken == "" {
		log.Fatal("non-loopback listen address requires WEAVEFLOW_MANAGEMENT_TOKEN")
	}
	level, err := parseLogLevel(*logLevel)
	if err != nil {
		log.Fatal(err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	claudeConfig, err := claudenode.RunnerConfigFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	claudeRunner, err := claudenode.NewProcessRunner(claudeConfig)
	if err != nil {
		log.Fatal(err)
	}
	codexConfig, err := codexnode.RunnerConfigFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	codexRunner, err := codexnode.NewProcessRunner(codexConfig)
	if err != nil {
		log.Fatal(err)
	}

	var graph *wfgraph.Graph
	if strings.TrimSpace(*graphPath) != "" {
		loaded, err := wfgraph.NewBuilder(builtin.NewDefaultRegistry()).BuildFile(*graphPath, &wfregistry.BuildContext{})
		if err != nil {
			log.Fatal(err)
		}
		graph = loaded
	}

	ctx := core.WithTools(context.Background(), defaultTools())

	srv, err := server.New(ctx, server.Config{
		Graph:               graph,
		BaseDir:             *dataDir,
		RuntimeStoreBackend: server.RuntimeStoreSQLite,
		SecretDirectory:     *secretDir,
		ManagementToken:     managementToken,
		RuntimeContextDecorators: []server.RuntimeContextDecorator{
			func(ctx context.Context) context.Context {
				return claudenode.WithRunner(ctx, claudeRunner)
			},
			func(ctx context.Context) context.Context {
				return codexnode.WithRunner(ctx, codexRunner)
			},
		},
	})
	if err != nil {
		log.Fatal("failed to initialize server")
	}
	if err := srv.Start(ctx); err != nil {
		log.Fatal("failed to start server")
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
	fmt.Printf("weaveflow server: %s%s\n", strings.TrimRight(display, "/"), routePrefix)

	if err := engine.Run(*addr); err != nil {
		log.Fatal(err)
	}
}

func isLoopbackListenAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

func defaultTools() map[string]core.Tool {
	return map[string]core.Tool{
		"bash":         tools.NewBash(),
		"calculator":   tools.NewCalculator(),
		"current_time": tools.NewCurrentTime(),
		"edit":         tools.NewEdit(),
		"glob":         tools.NewGlob(),
		"grep":         tools.NewGrep(),
		"outline":      tools.NewOutline(),
		"read":         tools.NewRead(),
		"tree":         tools.NewTree(),
		"web_fetch":    tools.NewWebFetch(),
		"web_search":   tools.NewWebSearch(),
		"write":        tools.NewWrite(),
	}
}
