package main

import (
	"context"
	"github.com/caarlos0/env/v11"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"go.uber.org/fx"
	"io"
	"log/slog"
	"net/url"
	"os"
)

var (
	version = "version" // Application version
	commit  = "commit"  // Git commit hash
	date    = "date"    // Build date
)

func main() {
	app := fx.New(
		//fx.WithLogger(func() fxevent.Logger {
		//	return &fxevent.SlogLogger{
		//		Logger: slog.New(
		//			slog.NewTextHandler(
		//				os.Stderr,
		//				&slog.HandlerOptions{Level: slog.LevelInfo},
		//			),
		//		),
		//	}
		//}),
		fx.Provide(
			func(cfg *config) *gorp.Client { return gorp.NewClient(&cfg.RpUrl, cfg.RpToken) },
			func() (*config, error) { return env.ParseAs[*config]() },
			fx.Annotate(provideMcpServer, fx.ParamTags(`group:"mcp-tools"`, `group:"mcp-prompts"`)),
			fx.Annotate(
				func() *McpTool { return &McpTool{} },
				fx.ResultTags(`group:"mcp-tools"`),
			),
			fx.Annotate(
				func() *McpPrompt { return &McpPrompt{} },
				fx.ResultTags(`group:"mcp-prompts"`),
			),
		),
		fx.Invoke(runServer),
	)
	app.Run()
}

type config struct {
	RpUrl     url.URL `env:"RP_HOST,required"`
	RpToken   string  `env:"RP_API_TOKEN,required"`
	RpProject string  `env:"RP_PROJECT,required"`
}

type (
	McpTool struct {
		mcp.Tool
		server.ToolHandlerFunc
	}
	McpPrompt struct {
		mcp.Prompt
		server.PromptHandlerFunc
	}
)

func provideMcpServer(tools []*McpTool, prompts []*McpPrompt) *server.MCPServer {
	srv := server.NewMCPServer("reportportal-mcp-server", version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
	)
	for _, tool := range tools {
		srv.AddTool(tool.Tool, tool.ToolHandlerFunc)
	}
	for _, prompt := range prompts {
		srv.AddPrompt(prompt.Prompt, prompt.PromptHandlerFunc)
	}
	return srv
}

func runServer(lc fx.Lifecycle, srv *server.MCPServer) error {
	stdioServer := server.NewStdioServer(srv)

	srvCtx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				in, out := io.Reader(os.Stdin), io.Writer(os.Stdout) // Use standard input/output
				if err := stdioServer.Listen(srvCtx, in, out); err != nil {
					slog.Error(err.Error())
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			cancel()
			return nil
		},
	})
	return nil
}
