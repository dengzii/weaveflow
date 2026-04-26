package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"weaveflow/internal/neo"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"weaveflow"
)

const defaultAddr = "localhost:9090"

func main() {
	weaveflow.SetLogger(zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.AddSync(os.Stderr),
		zapcore.WarnLevel,
	)))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runServe(ctx, []string{})

	//runClient(ctx, args)
}

func runServe(ctx context.Context, args []string) {
	addr := defaultAddr
	for i, a := range args {
		if a == "--addr" && i+1 < len(args) {
			addr = args[i+1]
		}
	}

	baseDir := filepath.Join(".local", "neo")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		fatal(err)
	}

	api, err := neo.NewNeoAPI(baseDir)
	fatal(err)

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(neo.CORSMiddleware())

	engine.POST("/neo/chat", wrapChatHandler(api.Chat))
	engine.GET("/neo/status", wrapHandler(api.Status))
	engine.POST("/neo/stop", wrapHandler(api.Stop))
	engine.GET("/neo/settings", wrapHandler(api.GetSettings))
	engine.PUT("/neo/settings", wrapBodyHandler(api.UpdateSettings))
	engine.GET("/neo/tools", wrapHandler(api.ListTools))
	engine.PUT("/neo/tools/:name", wrapBodyHandler(api.UpdateTool))

	srv := &http.Server{Addr: addr, Handler: engine}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	fmt.Fprintf(os.Stderr, "neo server listening on %s\n", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

func runClient(ctx context.Context, args []string) {
	addr := defaultAddr
	var messageArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--addr" && i+1 < len(args) {
			addr = args[i+1]
			i++
		} else {
			messageArgs = append(messageArgs, args[i])
		}
	}

	client := newNeoClient(addr)

	message := strings.TrimSpace(strings.Join(messageArgs, " "))
	if message != "" {
		answer, err := client.Chat(ctx, message)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if answer != "" {
			fmt.Println(answer)
		}
		return
	}

	runInteractive(ctx, client)
}

func runInteractive(ctx context.Context, client *neoClient) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	fmt.Println("Neo - 通用任务 Agent (client mode)")
	fmt.Println("命令: exit 退出, clear 清屏")
	fmt.Println(strings.Repeat("─", 40))

	for {
		fmt.Print("\n\033[36mYou>\033[0m ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch input {
		case "exit", "quit":
			fmt.Println("bye")
			return
		case "clear":
			fmt.Print("\033[2J\033[H")
			continue
		}

		if ctx.Err() != nil {
			break
		}

		fmt.Println()
		answer, err := client.Chat(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Println("\n(interrupted)")
				break
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		if answer == "" {
			answer = "(no answer generated)"
		}
		fmt.Printf("\n\033[33mNeo>\033[0m %s\n", answer)
	}
}

func wrapChatHandler(h func(*gin.Context, *neo.ChatRequest) error) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req neo.ChatRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
			return
		}
		if err := h(ctx, &req); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		}
	}
}

func wrapHandler(h func(*gin.Context) error) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if err := h(ctx); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		}
	}
}

func wrapBodyHandler[T any](h func(*gin.Context, *T) error) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req T
		if err := ctx.ShouldBindJSON(&req); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
			return
		}
		if err := h(ctx, &req); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		}
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
