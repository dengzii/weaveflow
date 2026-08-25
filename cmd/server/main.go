package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/dengzii/weaveflow/builtin"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/internal/assistant"
	"github.com/dengzii/weaveflow/internal/server"
	"github.com/dengzii/weaveflow/llms/openai"
	claudenode "github.com/dengzii/weaveflow/node/agents/claude"
	codexnode "github.com/dengzii/weaveflow/node/agents/codex"
	wfregistry "github.com/dengzii/weaveflow/registry"
	"github.com/dengzii/weaveflow/tools"

	"github.com/gin-gonic/gin"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dataDir := flag.String("data", ".local/wf", "data directory for graph debug runs")
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
			log.Printf("warning: failed to load graph %q: %v; continuing without preloaded graph", *graphPath, err)
		} else {
			graph = loaded
		}
	}

	ctx := core.WithTools(context.Background(), defaultTools())
	assistantConfig, err := assistantConfigFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}

	serverConfig := server.Config{
		Graph:               graph,
		Version:             version,
		BuildTime:           buildTime,
		BaseDir:             *dataDir,
		RuntimeStoreBackend: server.RuntimeStoreSQLite,
		SecretDirectory:     *secretDir,
		ManagementToken:     managementToken,
		Assistant:           assistantConfig,
		RuntimeContextDecorators: []server.RuntimeContextDecorator{
			func(ctx context.Context) context.Context {
				return claudenode.WithRunner(ctx, claudeRunner)
			},
			func(ctx context.Context) context.Context {
				return codexnode.WithRunner(ctx, codexRunner)
			},
		},
	}
	srv, err := server.New(ctx, serverConfig)
	if err != nil && graph != nil {
		log.Printf("warning: failed to initialize server with preloaded graph: %v; retrying without preloaded graph", err)
		serverConfig.Graph = nil
		srv, err = server.New(ctx, serverConfig)
	}
	if err != nil {
		log.Fatalf("failed to initialize server: %v", err)
	}
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = srv.Close() }()

	engine := gin.Default()
	engine.Use(corsMiddleware(*corsOrigins))
	routePrefix := normalizePrefix(*prefix)
	srv.RegisterRoutes(engine.Group(routePrefix))
	if srv.Assistant() != nil {
		srv.SetAssistantAPICaller(func(ctx context.Context, call assistant.APICall) (assistant.APIResult, error) {
			request := httptest.NewRequestWithContext(ctx, call.Method, routePrefix+call.Path, bytes.NewReader(call.Body))
			request.Header.Set("Content-Type", "application/json")
			if managementToken != "" {
				request.Header.Set("Authorization", "Bearer "+managementToken)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			return assistant.APIResult{Status: response.Code, Body: response.Body.Bytes()}, nil
		})
	}

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

func assistantConfigFromEnvironment() (*assistant.Config, error) {
	modelID := strings.TrimSpace(os.Getenv("WEAVEFLOW_ASSISTANT_MODEL"))
	apiKey := strings.TrimSpace(os.Getenv("WEAVEFLOW_ASSISTANT_API_KEY"))
	if modelID == "" && apiKey == "" {
		return nil, nil
	}
	if modelID == "" || apiKey == "" {
		return nil, fmt.Errorf("WEAVEFLOW_ASSISTANT_MODEL and WEAVEFLOW_ASSISTANT_API_KEY must be configured together")
	}
	provider := openai.Provider(strings.TrimSpace(os.Getenv("WEAVEFLOW_ASSISTANT_PROVIDER")))
	if provider == "" {
		provider = openai.ProviderOpenAI
	}
	apiFormat := openai.APIFormat(strings.TrimSpace(os.Getenv("WEAVEFLOW_ASSISTANT_API_FORMAT")))
	if apiFormat == "" {
		apiFormat = openai.APIFormatResponses
	}
	model, err := openai.New(
		openai.WithToken(apiKey),
		openai.WithModel(modelID),
		openai.WithBaseURL(strings.TrimSpace(os.Getenv("WEAVEFLOW_ASSISTANT_BASE_URL"))),
		openai.WithProvider(provider),
		openai.WithAPIFormat(apiFormat),
	)
	if err != nil {
		return nil, fmt.Errorf("configure assistant model: %w", err)
	}
	return &assistant.Config{Model: model, ModelID: modelID}, nil
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
