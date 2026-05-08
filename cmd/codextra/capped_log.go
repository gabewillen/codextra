package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type cappedLogWriter struct {
	mu       sync.Mutex
	file     *os.File
	maxBytes int64
}

func newCappedLogWriter(path string, maxBytes int64) (*cappedLogWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create proxy log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open proxy log: %w", err)
	}
	return &cappedLogWriter{file: file, maxBytes: maxBytes}, nil
}

func (w *cappedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	info, err := w.file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size()+int64(len(p)) > w.maxBytes {
		if err := w.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := w.file.Seek(0, 0); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *cappedLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
