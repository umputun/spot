package runner

import (
	"context"
)

// Process is a struct that holds the information needed to run a process.
type Process struct {
	RemoteHosts []string
	Concurrency int
	Executors   []remoteExecuter // executors pool
}

// remoteExecuter is an interface for remote execution.
type remoteExecuter interface {
	Connect(ctx context.Context, host string) (err error)
	Close() error
	Run(ctx context.Context, cmd string) (out []string, err error)
	Upload(ctx context.Context, local, remote string, mkdir bool) (err error)
}
