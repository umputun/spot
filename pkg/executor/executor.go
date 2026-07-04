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
// Implemented by Remote, Local and Dry structs.
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

// normalizeSlashes converts windows separators to forward slashes,
// exclude patterns and remote paths always use forward slashes.
func normalizeSlashes(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// isExcluded reports whether fpath matches any of the exclude patterns. A pattern ending in "/*"
// also matches the directory it names, so the whole subtree is protected, but this directory match
// only applies when fpath is itself a directory. This prevents a pattern like "dir*/*" from
// excluding a plain file such as "data.txt". Patterns and paths are compared with forward slashes.
func isExcluded(fpath string, isDir bool, excl []string) bool {
	pathSegments := strings.Split(normalizeSlashes(fpath), "/")

	// normalize the patterns once, splitting off the optional "/*" directory suffix
	type exPattern struct {
		full, dir    string
		isDirPattern bool
	}
	patterns := make([]exPattern, 0, len(excl))
	for _, ex := range excl {
		ex = normalizeSlashes(ex)
		dir, isDirPattern := strings.CutSuffix(ex, "/*")
		patterns = append(patterns, exPattern{full: ex, dir: dir, isDirPattern: isDirPattern})
	}

	for i := range pathSegments {
		subpath := strings.Join(pathSegments[:i+1], "/")
		for _, p := range patterns {
			if match, err := path.Match(p.full, subpath); err == nil && match {
				return true
			}
			// a "dir/*" pattern also protects the named directory itself, so the walker can skip
			// it; restricted to directories so a file matching the glob prefix is not excluded
			if isDir && p.isDirPattern {
				if match, err := path.Match(p.dir, subpath); err == nil && match {
					return true
				}
			}
		}
	}
	return false
}

// isExcludedSubPath checks if the path is a proper ancestor of any excluded path,
// i.e. removing the path recursively would also remove an excluded entry.
func isExcludedSubPath(fpath string, excl []string) bool {
	var pathSegments []string
	if fpath != "." {
		pathSegments = strings.Split(normalizeSlashes(fpath), "/")
	}
	for _, ex := range excl {
		exTrimmed := strings.TrimSuffix(normalizeSlashes(ex), "/*")
		if exTrimmed == "" {
			continue
		}
		exSegments := strings.Split(exTrimmed, "/")
		if len(pathSegments) >= len(exSegments) {
			continue // path is not a proper ancestor of the excluded path
		}
		matched := true
		for i, seg := range pathSegments {
			ok, err := path.Match(exSegments[i], seg)
			if err != nil || !ok {
				matched = false
				break
			}
		}
		if matched {
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
