package log

import (
	"context"
	"log/slog"
	"os"
)

type loggerKey struct{}

// WithLogger stores a logger in the context
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext retrieves a logger from the context
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// Setup creates a new logger with the specified level
func Setup(level string) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Simplify the time format
			if a.Key == slog.TimeKey {
				return slog.String("time", a.Value.Time().Format("15:04:05"))
			}
			return a
		},
	}))
}
