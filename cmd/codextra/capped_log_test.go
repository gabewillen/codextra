package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCappedLogWriterTruncatesBeforeLimit(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proxy.log")
	writer, err := newCappedLogWriter(path, 12)
	if err != nil {
		t.Fatalf("newCappedLogWriter() error = %v", err)
	}
	if _, err := writer.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	if _, err := writer.Write([]byte("second\n")); err != nil {
		t.Fatalf("Write(second) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(bytes); got != "second\n" {
		t.Fatalf("log contents = %q, want second entry only", got)
	}
}

func TestProxyLogMaxBytesUsesFallbackForInvalidValues(t *testing.T) {
	t.Setenv("CODEXTRA_PROXY_LOG_MAX_BYTES", "not-a-number")
	if got := proxyLogMaxBytes(); got != defaultProxyLogMaxBytes {
		t.Fatalf("proxyLogMaxBytes(invalid) = %d, want %d", got, defaultProxyLogMaxBytes)
	}
	t.Setenv("CODEXTRA_PROXY_LOG_MAX_BYTES", "-1")
	if got := proxyLogMaxBytes(); got != defaultProxyLogMaxBytes {
		t.Fatalf("proxyLogMaxBytes(negative) = %d, want %d", got, defaultProxyLogMaxBytes)
	}
	t.Setenv("CODEXTRA_PROXY_LOG_MAX_BYTES", "2048")
	if got := proxyLogMaxBytes(); got != 2048 {
		t.Fatalf("proxyLogMaxBytes(valid) = %d, want 2048", got)
	}
}

func TestCappedLogWriterCreatesPrivateLogFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "proxy.log")
	writer, err := newCappedLogWriter(path, 1024)
	if err != nil {
		t.Fatalf("newCappedLogWriter() error = %v", err)
	}
	if _, err := writer.Write([]byte("entry\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(bytes), "entry") {
		t.Fatalf("log contents = %q, want entry", string(bytes))
	}
}
