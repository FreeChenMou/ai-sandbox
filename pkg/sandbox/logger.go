//go:build linux

package sandbox

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LogConfig defines the configuration for sandbox logging.
type LogConfig struct {
	Level   string // "debug", "info", "warn", "error"; default "info"
	Dir     string // log directory; default "/var/log/ai-sandbox"
	Console bool   // also write to stderr; default true
}

// DefaultLogConfig returns default log configuration.
func DefaultLogConfig() LogConfig {
	return LogConfig{
		Level:   "info",
		Dir:     "/var/log/ai-sandbox",
		Console: true,
	}
}

// SandboxLogger wraps a zap.Logger with per-sandbox log file management.
type SandboxLogger struct {
	logger  *zap.Logger
	id      string
	logFile *os.File
	dir     string
}

// initLogEntry is the JSON format shared between parent and child process
// for log messages sent through the log pipe.
type initLogEntry struct {
	Level   string `json:"level"`
	Message string `json:"msg"`
}

// NewSandboxLogger creates a new logger that writes structured JSON logs
// to a per-sandbox log file, and optionally to stderr.
func NewSandboxLogger(config LogConfig) (*SandboxLogger, error) {
	if config.Dir == "" {
		config.Dir = "/var/log/ai-sandbox"
	}
	if config.Level == "" {
		config.Level = "info"
	}

	// Parse log level
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(config.Level)); err != nil {
		return nil, fmt.Errorf("logger: invalid level %q: %w", config.Level, err)
	}

	// Generate sandbox ID
	id := generateID()

	// Create log directory
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("logger: mkdir %s: %w", config.Dir, err)
	}

	// Create log file
	logPath := filepath.Join(config.Dir, "sandbox-"+id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("logger: create %s: %w", logPath, err)
	}

	// Build zap encoder config (production JSON)
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.EpochTimeEncoder

	encoder := zapcore.NewJSONEncoder(encoderCfg)

	// File core (always enabled)
	fileSyncer := zapcore.AddSync(logFile)
	fileCore := zapcore.NewCore(encoder, fileSyncer, zapLevel)

	var core zapcore.Core
	if config.Console {
		// Tee: file + stderr
		stderrSyncer := zapcore.AddSync(os.Stderr)
		stderrCore := zapcore.NewCore(encoder, stderrSyncer, zapLevel)
		core = zapcore.NewTee(fileCore, stderrCore)
	} else {
		core = fileCore
	}

	logger := zap.New(core).With(zap.String("sandbox_id", id))

	return &SandboxLogger{
		logger:  logger,
		id:      id,
		logFile: logFile,
		dir:     config.Dir,
	}, nil
}

// Logger returns the underlying *zap.Logger for use by components.
func (sl *SandboxLogger) Logger() *zap.Logger {
	return sl.logger
}

// ID returns the sandbox log ID.
func (sl *SandboxLogger) ID() string {
	return sl.id
}

// Close syncs the logger and closes the log file.
func (sl *SandboxLogger) Close() error {
	_ = sl.logger.Sync()
	return sl.logFile.Close()
}

// readLogPipe reads JSON log lines from r (the read end of the log pipe)
// and writes them to the zap logger with source=init.
// This function blocks until the reader returns EOF or an error.
func readLogPipe(r io.Reader, logger *zap.Logger) {
	scanner := bufio.NewScanner(r)
	initLogger := logger.With(zap.String("source", "init"))

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry initLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// If we can't parse, log the raw line
			initLogger.Warn("unparseable init log", zap.String("raw", string(line)))
			continue
		}

		switch entry.Level {
		case "debug":
			initLogger.Debug(entry.Message)
		case "info":
			initLogger.Info(entry.Message)
		case "warn":
			initLogger.Warn(entry.Message)
		case "error":
			initLogger.Error(entry.Message)
		default:
			initLogger.Info(entry.Message)
		}
	}
}

// writeInitLog writes a JSON log entry to w. Used by the child process
// to send structured logs through the log pipe without importing zap.
func writeInitLog(w io.Writer, level, msg string) {
	entry := initLogEntry{
		Level:   level,
		Message: msg,
	}
	// Errors are intentionally ignored: if the pipe is broken,
	// logging should not crash the init process.
	_ = json.NewEncoder(w).Encode(&entry)
}
