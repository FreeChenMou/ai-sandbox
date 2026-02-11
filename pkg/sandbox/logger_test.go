//go:build linux

package sandbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestDefaultLogConfig(t *testing.T) {
	cfg := DefaultLogConfig()

	if cfg.Level != "info" {
		t.Errorf("expected level 'info', got %q", cfg.Level)
	}
	if cfg.Dir != "/var/log/ai-sandbox" {
		t.Errorf("expected dir '/var/log/ai-sandbox', got %q", cfg.Dir)
	}
	if !cfg.Console {
		t.Error("expected console=true by default")
	}
}

func TestNewSandboxLogger(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := LogConfig{
		Level:   "debug",
		Dir:     tmpDir,
		Console: false,
	}

	sl, err := NewSandboxLogger(cfg)
	if err != nil {
		t.Fatalf("NewSandboxLogger: %v", err)
	}
	defer sl.Close()

	// Verify ID is non-empty
	if sl.ID() == "" {
		t.Error("expected non-empty ID")
	}

	// Verify logger is non-nil
	if sl.Logger() == nil {
		t.Error("expected non-nil logger")
	}

	// Write a log entry
	sl.Logger().Info("test message", zap.String("key", "value"))
	sl.Logger().Sync()

	// Verify log file was created
	logPath := filepath.Join(tmpDir, "sandbox-"+sl.ID()+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if !strings.Contains(string(data), "test message") {
		t.Errorf("log file does not contain 'test message': %s", string(data))
	}
	if !strings.Contains(string(data), "sandbox_id") {
		t.Errorf("log file does not contain 'sandbox_id': %s", string(data))
	}
}

func TestNewSandboxLoggerInvalidLevel(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := LogConfig{
		Level:   "invalid",
		Dir:     tmpDir,
		Console: false,
	}

	_, err := NewSandboxLogger(cfg)
	if err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestReadLogPipe(t *testing.T) {
	// Create an observed logger to capture log entries
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	// Simulate child process writing JSON log entries
	var buf bytes.Buffer
	entries := []initLogEntry{
		{Level: "info", Message: "mount succeeded"},
		{Level: "warn", Message: "mount private failed"},
		{Level: "error", Message: "critical failure"},
		{Level: "debug", Message: "debug detail"},
	}
	for _, e := range entries {
		json.NewEncoder(&buf).Encode(&e)
	}

	// Run readLogPipe (it reads until EOF)
	readLogPipe(&buf, logger)

	// Verify all entries were captured
	allLogs := logs.All()
	if len(allLogs) != 4 {
		t.Fatalf("expected 4 log entries, got %d", len(allLogs))
	}

	// Verify levels
	expectedLevels := []zapcore.Level{
		zapcore.InfoLevel,
		zapcore.WarnLevel,
		zapcore.ErrorLevel,
		zapcore.DebugLevel,
	}
	for i, l := range allLogs {
		if l.Level != expectedLevels[i] {
			t.Errorf("entry %d: expected level %v, got %v", i, expectedLevels[i], l.Level)
		}
	}

	// Verify messages
	expectedMsgs := []string{
		"mount succeeded",
		"mount private failed",
		"critical failure",
		"debug detail",
	}
	for i, l := range allLogs {
		if l.Message != expectedMsgs[i] {
			t.Errorf("entry %d: expected message %q, got %q", i, expectedMsgs[i], l.Message)
		}
	}

	// Verify source=init field
	for i, l := range allLogs {
		sourceField := ""
		for _, f := range l.ContextMap() {
			if v, ok := f.(string); ok {
				_ = v
			}
		}
		ctx := l.ContextMap()
		if v, ok := ctx["source"]; ok {
			sourceField = v.(string)
		}
		if sourceField != "init" {
			t.Errorf("entry %d: expected source='init', got %q", i, sourceField)
		}
	}
}

func TestReadLogPipeUnparseable(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	buf := bytes.NewBufferString("not json at all\n")
	readLogPipe(buf, logger)

	allLogs := logs.All()
	if len(allLogs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(allLogs))
	}
	if allLogs[0].Level != zapcore.WarnLevel {
		t.Errorf("expected warn level for unparseable line")
	}
	if allLogs[0].Message != "unparseable init log" {
		t.Errorf("unexpected message: %s", allLogs[0].Message)
	}
}

func TestWriteInitLog(t *testing.T) {
	var buf bytes.Buffer
	writeInitLog(&buf, "warn", "test warning message")

	var entry initLogEntry
	if err := json.NewDecoder(&buf).Decode(&entry); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if entry.Level != "warn" {
		t.Errorf("expected level 'warn', got %q", entry.Level)
	}
	if entry.Message != "test warning message" {
		t.Errorf("expected message 'test warning message', got %q", entry.Message)
	}
}

func TestLogLevels(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a logger with "warn" level — should filter out info/debug
	cfg := LogConfig{
		Level:   "warn",
		Dir:     tmpDir,
		Console: false,
	}

	sl, err := NewSandboxLogger(cfg)
	if err != nil {
		t.Fatalf("NewSandboxLogger: %v", err)
	}
	defer sl.Close()

	sl.Logger().Debug("debug msg")
	sl.Logger().Info("info msg")
	sl.Logger().Warn("warn msg")
	sl.Logger().Error("error msg")
	sl.Logger().Sync()

	logPath := filepath.Join(tmpDir, "sandbox-"+sl.ID()+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "debug msg") {
		t.Error("debug msg should be filtered at warn level")
	}
	if strings.Contains(content, "info msg") {
		t.Error("info msg should be filtered at warn level")
	}
	if !strings.Contains(content, "warn msg") {
		t.Error("warn msg should be present at warn level")
	}
	if !strings.Contains(content, "error msg") {
		t.Error("error msg should be present at warn level")
	}
}

func TestLoggerClose(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := LogConfig{
		Level:   "info",
		Dir:     tmpDir,
		Console: false,
	}

	sl, err := NewSandboxLogger(cfg)
	if err != nil {
		t.Fatalf("NewSandboxLogger: %v", err)
	}

	sl.Logger().Info("before close")

	if err := sl.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Verify log file exists and has content
	logPath := filepath.Join(tmpDir, "sandbox-"+sl.ID()+".log")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("log file is empty after close")
	}
}

func TestConcurrentLoggers(t *testing.T) {
	tmpDir := t.TempDir()

	const n = 5
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			cfg := LogConfig{
				Level:   "info",
				Dir:     tmpDir,
				Console: false,
			}

			sl, err := NewSandboxLogger(cfg)
			if err != nil {
				errs <- err
				return
			}

			sl.Logger().Info("concurrent test message")
			sl.Logger().Sync()
			sl.Close()
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent logger error: %v", err)
	}

	// Verify multiple log files were created
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	logCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sandbox-") && strings.HasSuffix(e.Name(), ".log") {
			logCount++
		}
	}
	if logCount != n {
		t.Errorf("expected %d log files, got %d", n, logCount)
	}
}

func TestWriteInitLogMultiple(t *testing.T) {
	var buf bytes.Buffer

	writeInitLog(&buf, "info", "first message")
	writeInitLog(&buf, "warn", "second message")
	writeInitLog(&buf, "error", "third message")

	decoder := json.NewDecoder(&buf)

	var entries []initLogEntry
	for decoder.More() {
		var e initLogEntry
		if err := decoder.Decode(&e); err != nil {
			t.Fatalf("decode: %v", err)
		}
		entries = append(entries, e)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	expected := []struct {
		level, msg string
	}{
		{"info", "first message"},
		{"warn", "second message"},
		{"error", "third message"},
	}
	for i, e := range entries {
		if e.Level != expected[i].level {
			t.Errorf("entry %d: level=%q, want %q", i, e.Level, expected[i].level)
		}
		if e.Message != expected[i].msg {
			t.Errorf("entry %d: msg=%q, want %q", i, e.Message, expected[i].msg)
		}
	}
}
