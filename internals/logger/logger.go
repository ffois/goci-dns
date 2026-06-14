// Package logger provides structured JSON logging using log/slog.
//
// It writes simultaneously to os.Stdout and a rotating log file in AppData.
// Log rotation is handled by lumberjack: files rotate at 10 MB, keeping
// 5 backups for up to 28 days, with gzip compression on old files.
//
// Log levels are controlled via the LOG_LEVEL environment variable
// (DEBUG, INFO, WARN, ERROR). Default level is INFO.
//
// Sensitive fields are automatically redacted via the Redacted type.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// ---------------------------------------------------------------------------
// Global state
// ---------------------------------------------------------------------------

var (
	// Logger is the global slog.Logger instance used across the application.
	Logger *slog.Logger

	// rotator is the lumberjack log writer (needed for CloseLogger).
	rotator *lumberjack.Logger

	// levelVar allows runtime log level changes.
	levelVar slog.LevelVar
)

// ---------------------------------------------------------------------------
// Initialisation
// ---------------------------------------------------------------------------

// InitLogger sets up the global structured logger.
// It writes JSON to both os.Stdout and a rotating log file at
// %APPDATA%/EMLy/logs/app.log. Rotation: 10 MB max, 5 backups, 28 days,
// gzip-compressed old files.
// The log level is read from the LOG_LEVEL env var (DEBUG|INFO|WARN|ERROR).
func InitLogger() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("logger: user config dir: %w", err)
	}

	logsDir := filepath.Join(configDir, "EMLy", "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("logger: create logs dir: %w", err)
	}

	logPath := filepath.Join(logsDir, "app.log")

	rotator = &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10,   // megabytes
		MaxBackups: 5,    // old files to keep
		MaxAge:     28,   // days
		Compress:   true, // gzip old files
	}

	// Parse log level from environment
	setLevelFromEnv()

	multi := io.MultiWriter(os.Stdout, rotator)
	handler := slog.NewJSONHandler(multi, &slog.HandlerOptions{
		Level:       &levelVar,
		ReplaceAttr: replaceAttr,
	})

	Logger = slog.New(handler)
	slog.SetDefault(Logger)

	Logger.Info("logger initialised", "log_file", logPath, "level", levelVar.Level().String())
	return nil
}

// SetLevelFromString dynamically adjusts the log level.
// Accepted values: "DEBUG", "INFO", "WARN", "ERROR" (case-insensitive).
func SetLevelFromString(level string) {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		levelVar.Set(slog.LevelDebug)
	case "WARN", "WARNING":
		levelVar.Set(slog.LevelWarn)
	case "ERROR":
		levelVar.Set(slog.LevelError)
	default:
		levelVar.Set(slog.LevelInfo)
	}
}

// CloseLogger closes the underlying rotating log file.
func CloseLogger() {
	if rotator != nil {
		rotator.Close()
	}
}

// ---------------------------------------------------------------------------
// Convenience wrappers (structured)
// ---------------------------------------------------------------------------

// Debug logs at DEBUG level.
func Debug(msg string, args ...any) { logWithCaller(slog.LevelDebug, msg, args...) }

// Info logs at INFO level.
func Info(msg string, args ...any) { logWithCaller(slog.LevelInfo, msg, args...) }

// Warn logs at WARN level.
func Warn(msg string, args ...any) { logWithCaller(slog.LevelWarn, msg, args...) }

// Error logs at ERROR level.
func Error(msg string, args ...any) { logWithCaller(slog.LevelError, msg, args...) }

func Fatal(msg string, args ...any) {
	logWithCaller(slog.LevelError, msg, args...)
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// Canonical Log Line helper
// ---------------------------------------------------------------------------

// CanonicalFields returns slog attributes for a canonical log line.
// Call at the beginning of a Wails-bound function with defer:
//
//	defer func(start time.Time) {
//	    logger.Info("canonical_line", logger.CanonicalFields("ReadEML", start, err)...)
//	}(time.Now())
func CanonicalFields(fn string, start time.Time, err error) []any {
	status := "success"
	fields := []any{
		"function", fn,
		"duration_ms", time.Since(start).Milliseconds(),
	}
	if err != nil {
		status = "error"
		fields = append(fields, "error", err.Error())
	}
	fields = append(fields, "status", status)
	return fields
}

// ---------------------------------------------------------------------------
// Sensitive-data redaction
// ---------------------------------------------------------------------------

// Redacted wraps a string so that slog renders it as "[REDACTED]".
// Use it for fields that may contain passwords, tokens, or API keys.
//
//	slog.String("password", logger.Redacted("s3cret").String())
//
// or embed it directly:
//
//	slog.Any("password", logger.Redacted("s3cret"))
type Redacted string

// LogValue implements slog.LogValuer — always returns "[REDACTED]".
func (Redacted) LogValue() slog.Value {
	return slog.StringValue("[REDACTED]")
}

// String returns the redacted placeholder (safe for fmt/print).
func (Redacted) String() string { return "[REDACTED]" }

// RedactStruct inspects a map and redacts known sensitive keys.
// Useful for logging arbitrary config/request maps.
func RedactStruct(m map[string]any) map[string]any {
	sensitiveKeys := map[string]bool{
		"password": true, "api_key": true, "apikey": true, "token": true,
		"secret": true, "authorization": true, "bugreport_api_key": true,
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sensitiveKeys[strings.ToLower(k)] {
			out[k] = "[REDACTED]"
		} else {
			out[k] = v
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Legacy compatibility
// ---------------------------------------------------------------------------

// Log provides a simple unstructured Info log for backward compatibility.
// Prefer the structured Debug/Info/Warn/Error functions for new code.
func Log(args ...any) {
	logWithCaller(slog.LevelInfo, fmt.Sprint(args...))
}

// LogDepth works like Log but allows caller-skip adjustment.
// skip=2 means the caller of the function that called LogDepth.
func LogDepth(skip int, args ...any) {
	if Logger == nil {
		return
	}
	msg := fmt.Sprint(args...)

	var pcs [1]uintptr
	runtime.Callers(skip+1, pcs[:])
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, pcs[0])
	_ = Logger.Handler().Handle(context.Background(), r)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func setLevelFromEnv() {
	SetLevelFromString(os.Getenv("LOG_LEVEL"))
}

// logWithCaller captures the correct caller frame (skipping this package).
func logWithCaller(level slog.Level, msg string, args ...any) {
	if Logger == nil {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:]) // skip logWithCaller → Debug/Info/Warn/Error → actual caller
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(args...)
	_ = Logger.Handler().Handle(context.Background(), r)
}

// replaceAttr customises the JSON output: shortens source paths.
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.SourceKey {
		if src, ok := a.Value.Any().(*slog.Source); ok {
			src.File = filepath.Base(src.File)
		}
	}
	return a
}
