package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	defaultLogger *Logger
	once          sync.Once
)

// Logger wraps slog with additional functionality.
type Logger struct {
	*slog.Logger
	level Level
}

// Level represents log severity levels.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "info"
	}
}

// ParseLevel converts a string level to Level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return Debug, nil
	case "info":
		return Info, nil
	case "warn", "warning":
		return Warn, nil
	case "error":
		return Error, nil
	default:
		return Info, fmt.Errorf("unknown log level: %s", s)
	}
}

// New creates a new Logger with the specified level and format.
func New(levelStr, format string) (*Logger, error) {
	level, err := ParseLevel(levelStr)
	if err != nil {
		return nil, err
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: levelToSlogLevel(level),
	}

	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("unknown log format: %s", format)
	}

	return &Logger{
		Logger: slog.New(handler),
		level:  level,
	}, nil
}

// MustNew creates a new Logger or panics on error.
func MustNew(levelStr, format string) *Logger {
	l, err := New(levelStr, format)
	if err != nil {
		panic(err)
	}
	return l
}

// SetDefault sets the global default logger.
func SetDefault(l *Logger) {
	once.Do(func() {
		defaultLogger = l
		slog.SetDefault(l.Logger)
	})
}

// Default returns the global default logger.
func Default() *Logger {
	if defaultLogger == nil {
		return MustNew("info", "json")
	}
	return defaultLogger
}

func levelToSlogLevel(l Level) slog.Level {
	switch l {
	case Debug:
		return slog.LevelDebug
	case Info:
		return slog.LevelInfo
	case Warn:
		return slog.LevelWarn
	case Error:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
