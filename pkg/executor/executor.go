// Package executor provides an interface for the executor as well as a local and remote implementation.
// The executor is used to run commands on the local machine or on a remote machine.
package executor

import (
	"context"
	"path"
	"strings"
	"time"
)

// Interface is an interface for the executor.
// Implemented by Remote and Local structs.
type Interface interface {
	Run(ctx context.Context, c string, opts *RunOpts) (out []string, err error)
	Upload(ctx context.Context, local, remote string, opts *UpDownOpts) (err error)
	Download(ctx context.Context, remote, local string, opts *UpDownOpts) (err error)
	Sync(ctx context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error)
	Delete(ctx context.Context, remoteFile string, opts *DeleteOpts) (err error)
	Close() error
}

// RunOpts is a struct for run options.
type RunOpts struct {
	Verbose bool // print more info to primary stdout
}

// UpDownOpts is a struct for upload and download options.
type UpDownOpts struct {
	Mkdir    bool     // create remote directory if it does not exist
	Checksum bool     // compare checksums of local and remote files, default is size and modtime
	Force    bool     // overwrite existing files on remote
	Exclude  []string // exclude files matching the given patterns
}

// SyncOpts is a struct for sync options.
type SyncOpts struct {
	Delete   bool     // delete extra files on remote
	Exclude  []string // exclude files matching the given patterns
	Checksum bool     // compare checksums of local and remote files, default is size and modtime
	Force    bool     // overwrite existing files on remote
}

// DeleteOpts is a struct for delete options.
type DeleteOpts struct {
	Recursive bool     // delete directories recursively
	Exclude   []string // exclude files matching the given patterns
}

func isExcluded(p string, excl []string) bool {
	pathSegments := strings.Split(p, string("/"))
	for i := range pathSegments {
		subpath := path.Join(pathSegments[:i+1]...)
		for _, ex := range excl {
			match, err := path.Match(ex, subpath)
			if err != nil {
				continue
			}
			if match {
				return true
			}
			// treat directory in exclusion list as excluding all of its contents
			if strings.TrimSuffix(ex, "/*") == subpath {
				return true
			}
		}
	}
	return false
}

func isExcludedSubPath(p string, excl []string) bool {
	subpath := path.Join(p, "*")
	for _, ex := range excl {
		match, err := path.Match(subpath, strings.TrimSuffix(ex, "/*"))
		if err != nil {
			continue
		}
		if match {
			return true
		}
	}
	return false
}

func isWithinOneSecond(t1, t2 time.Time) bool {
	diff := t1.Sub(t2)
	if diff < 0 {
		diff = -diff
	}
	return diff <= time.Second
}
