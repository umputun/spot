// Package executor provides an interface for the executor as well as a local and remote implementation.
// The executor is used to run commands on the local machine or on a remote machine.
package executor

import (
	"context"
	"log"
	"strings"
)

// Interface is an interface for the executor.
// Implemented by Remote and Local structs.
type Interface interface {
	Run(ctx context.Context, c string) (out []string, err error)
	Upload(ctx context.Context, local, remote string, mkdir bool) (err error)
	Download(ctx context.Context, remote, local string, mkdir bool) (err error)
	Sync(ctx context.Context, localDir, remoteDir string, del bool) ([]string, error)
	Delete(ctx context.Context, remoteFile string, recursive bool) (err error)
	Close() error
}

// StdOutLogWriter is a writer that writes log with a prefix and a log level.
type StdOutLogWriter struct {
	prefix string
	level  string
}

// NewStdOutLogWriter creates a new StdOutLogWriter.
func NewStdOutLogWriter(prefix, level string) *StdOutLogWriter {
	return &StdOutLogWriter{prefix: prefix, level: level}
}

func (w *StdOutLogWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		log.Printf("[%s] %s %s", w.level, w.prefix, line)
	}
	return len(p), nil
}
