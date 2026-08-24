// Package executor provides an interface for the executor as well as a local and remote implementation.
// The executor is used to run commands on the local machine or on a remote machine.
package executor

import (
	"bytes"
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
// Note it is not comparable, since KeepLine is a func.
type RunOpts struct {
	// KeepLine decides which stdout lines Run returns; a nil predicate keeps every line.
	// Every line still reaches the log in full, so this bounds only what the caller holds:
	// a command printing a large log costs nothing extra when its output is not read.
	KeepLine func(line string) bool
}

// UpDownOpts is a struct for upload and download options.
type UpDownOpts struct {
	Mkdir   bool     // create remote directory if it does not exist
	Force   bool     // overwrite existing files on remote
	Exclude []string // exclude files matching the given patterns
}

// SyncOpts is a struct for sync options.
type SyncOpts struct {
	Delete  bool     // delete extra files on remote
	Exclude []string // exclude files matching the given patterns
}

// DeleteOpts is a struct for delete options.
type DeleteOpts struct {
	Recursive bool     // delete directories recursively
	Exclude   []string // exclude files matching the given patterns
}

// normalizeSlashes converts windows separators to forward slashes,
// exclude patterns and remote paths always use forward slashes.
func normalizeSlashes(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// splitOutputLines splits captured command output into lines the same way bufio.ScanLines does, i.e. dropping
// a trailing \r and the empty element after the final newline, but without the scanner's token size limit.
// All executors share it, so the same stdout produces the same lines regardless of the implementation.
// The output is already fully buffered, so a single line of any length is returned as is.
func splitOutputLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	res := make([]string, 0, len(lines))
	for _, line := range lines {
		res = append(res, strings.TrimSuffix(line, "\r"))
	}
	return res
}

// lineCapture splits everything written to it into lines exactly as splitOutputLines does, keeping
// only the ones keep accepts. Executors pass it alongside the log writer, so retention scales with
// what the caller reads rather than with what the command printed. A line of any length is handled,
// there is no scanner token limit, and an unterminated final line is returned like a terminated one.
type lineCapture struct {
	keep    func(line string) bool
	partial []byte
	lines   []string
}

func (lc *lineCapture) Write(p []byte) (n int, err error) {
	n = len(p)
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			lc.partial = append(lc.partial, p...)
			return n, nil
		}
		if len(lc.partial) == 0 {
			lc.take(p[:i]) // whole line arrived in this write, no need to stage it
		} else {
			lc.partial = append(lc.partial, p[:i]...)
			lc.take(lc.partial)
			lc.reset()
		}
		p = p[i+1:]
	}
}

// reset readies the staging buffer for the next line. A buffer that stayed small is kept, sparing an
// allocation per line, but a large one is released: holding it would pin the longest line for the rest
// of the command, which is the retention this type exists to avoid.
func (lc *lineCapture) reset() {
	const maxStageReuse = 64 << 10
	if cap(lc.partial) > maxStageReuse {
		lc.partial = nil
		return
	}
	lc.partial = lc.partial[:0]
}

// take converts one line and keeps it if the predicate accepts. The string conversion copies, so
// the retained line never pins the writer's buffer, and a rejected line is garbage at once.
func (lc *lineCapture) take(b []byte) {
	line := string(bytes.TrimSuffix(b, []byte("\r")))
	if lc.keep == nil || lc.keep(line) {
		lc.lines = append(lc.lines, line)
	}
}

// result flushes an unterminated final line and returns the kept lines, nil when nothing was kept.
func (lc *lineCapture) result() []string {
	if len(lc.partial) > 0 {
		lc.take(lc.partial)
		lc.partial = nil
	}
	return lc.lines
}

// newLineCapture builds a capture honoring opts, which may be nil.
func newLineCapture(opts *RunOpts) *lineCapture {
	if opts == nil {
		return &lineCapture{}
	}
	return &lineCapture{keep: opts.KeepLine}
}

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
