package executor

import "context"

// Interface is an interface for the executor.
// Implemented by Remote and Local structs.
type Interface interface {
	Run(ctx context.Context, c string) (out []string, err error)
	Upload(ctx context.Context, local, remote string, mkdir bool) (err error)
	Download(ctx context.Context, remote, local string, mkdir bool) (err error)
	Sync(ctx context.Context, localDir, remoteDir string, del bool) ([]string, error)
	Delete(ctx context.Context, remoteFile string, recursive bool) (err error)
}
