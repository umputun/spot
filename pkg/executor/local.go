package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-pkgz/fileutils"
)

// Local is a runner for local execution. Similar to remote, but without ssh, just exec on localhost and local copy/delete/sync
type Local struct {
	secrets []string
}

// SetSecrets sets the secrets for the remote executor.
func (l *Local) SetSecrets(secrets []string) {
	l.secrets = secrets
}

// Run executes command on local hostAddr, inside the shell
func (l *Local) Run(ctx context.Context, cmd string, opts *RunOpts) (out []string, err error) {
	shell := func() string {
		if strings.HasPrefix(cmd, "sh -c") {
			return "sh" // command has sh -c prefix, so use sh
		}
		if os.Getenv("SHELL") == "" {
			return "/bin/sh" // default to /bin/sh
		}
		return os.Getenv("SHELL") // use SHELL env var
	}

	if strings.HasPrefix(cmd, shell()+" -c ") {
		// strip sh -c 'command' to just command to avoid double shell
		cmd = strings.TrimPrefix(cmd, shell()+" -c ")
		cmd = strings.TrimPrefix(cmd, "'")
		cmd = strings.TrimSuffix(cmd, "'")
	}
	command := exec.CommandContext(ctx, shell(), "-c", cmd) //nolint

	outLog, errLog := MakeOutAndErrWriters("localhost", "", opts != nil && opts.Verbose, l.secrets)
	outLog.Write([]byte(cmd)) // nolint

	var stdoutBuf bytes.Buffer
	mwr := io.MultiWriter(outLog, &stdoutBuf)
	command.Stdout, command.Stderr = mwr, errLog
	err = command.Run()
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(&stdoutBuf)
	for scanner.Scan() {
		out = append(out, scanner.Text())
	}
	return out, scanner.Err()
}

// Upload just copy file from one place to another
func (l *Local) Upload(_ context.Context, src, dst string, opts *UpDownOpts) (err error) {

	// check if the local parameter contains a glob pattern
	matches, err := filepath.Glob(src)
	if err != nil {
		return fmt.Errorf("failed to expand glob pattern %s: %w", src, err)
	}

	if len(matches) == 0 { // no match
		return fmt.Errorf("source file %q not found", src)
	}

	var mkdir bool
	var exclude []string

	if opts != nil {
		mkdir = opts.Mkdir
		exclude = opts.Exclude
	}

	if mkdir {
		if err = os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return fmt.Errorf("can't create local dir %s: %w", filepath.Dir(dst), err)
		}
	}

	for _, match := range matches {
		relPath, e := filepath.Rel(filepath.Dir(src), match)
		if e != nil {
			return fmt.Errorf("failed to build relative path for %s: %w", match, err)
		}
		if isExcluded(relPath, exclude) {
			continue
		}

		destination := dst
		if len(matches) > 1 {
			destination = filepath.Join(dst, filepath.Base(match))
		}

		// check source file info
		srcInfo, err := os.Stat(match)
		if err != nil {
			return fmt.Errorf("failed to stat source file %s: %w", match, err)
		}

		// check destination file info
		dstInfo, err := os.Stat(destination)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat destination file %s: %w", destination, err)
		}

		// if destination file exists, and source and destination have the same size and modification time, skip copying
		forced := opts != nil && opts.Force
		isSame := func() bool {
			return srcInfo.Size() == dstInfo.Size() && srcInfo.ModTime().Equal(dstInfo.ModTime()) &&
				srcInfo.Mode() == dstInfo.Mode()
		}
		if err == nil && !forced && isSame() {
			log.Printf("[DEBUG] skip copying %s to %s, same size and modification time", match, destination)
			continue
		}

		if err = l.copyFile(match, destination); err != nil {
			return fmt.Errorf("can't copy local file from %s to %s: %w", match, dst, err)
		}
	}
	return nil
}

// Download just copy file from one place to another
func (l *Local) Download(_ context.Context, src, dst string, opts *UpDownOpts) (err error) {
	return l.Upload(context.Background(), src, dst, opts) // same as upload for local
}

// Sync directories from src to dst
func (l *Local) Sync(ctx context.Context, src, dst string, opts *SyncOpts) ([]string, error) {
	excl := []string{}
	if opts != nil {
		excl = opts.Exclude
	}
	copiedFiles, err := l.syncSrcToDst(ctx, src, dst, excl)
	if err != nil {
		return nil, err
	}

	if opts != nil && opts.Delete {
		if err := l.removeExtraDstFiles(ctx, src, dst); err != nil {
			return nil, err
		}
	}

	return copiedFiles, nil
}

// Delete file or directory
func (l *Local) Delete(ctx context.Context, remoteFile string, opts *DeleteOpts) (err error) {
	recursive := opts != nil && opts.Recursive
	if !recursive {
		return os.Remove(remoteFile)
	}

	var exclude []string
	if opts != nil {
		exclude = opts.Exclude
	}

	return l.deletePath(ctx, remoteFile, exclude)
}

// Close does nothing for local
func (l *Local) Close() error { return nil }

func (l *Local) syncSrcToDst(ctx context.Context, src, dst string, excl []string) ([]string, error) {
	var copiedFiles []string

	err := filepath.Walk(src, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		relPath, err := filepath.Rel(src, srcPath)
		if err != nil {
			return err
		}
		if isExcluded(relPath, excl) {
			return nil
		}

		dstPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			if _, err := os.Stat(dstPath); errors.Is(err, os.ErrNotExist) {
				err := os.Mkdir(dstPath, info.Mode())
				if err != nil {
					return err
				}
			}
			return nil
		}

		if err := fileutils.CopyFile(srcPath, dstPath); err != nil {
			return err
		}
		if err := os.Chmod(dstPath, info.Mode()); err != nil {
			return err
		}
		copiedFiles = append(copiedFiles, relPath)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return copiedFiles, nil
}

func (l *Local) removeExtraDstFiles(ctx context.Context, src, dst string) error {
	var pathsToDelete []string

	err := filepath.Walk(dst, func(dstPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		relPath, err := filepath.Rel(dst, dstPath)
		if err != nil {
			return err
		}

		srcPath := filepath.Join(src, relPath)
		if _, err := os.Stat(srcPath); errors.Is(err, os.ErrNotExist) {
			pathsToDelete = append(pathsToDelete, dstPath)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Remove files and directories in reverse order
	for i := len(pathsToDelete) - 1; i >= 0; i-- {
		dstPath := pathsToDelete[i]
		if e := os.RemoveAll(dstPath); e != nil {
			return e
		}
	}

	return nil
}

// nolint
func (l *Local) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	if err := dstFile.Sync(); err != nil {
		return err
	}

	fi, err := srcFile.Stat()
	if err != nil {
		return err
	}
	if err := os.Chmod(dst, fi.Mode()); err != nil {
		return err
	}

	return nil
}

func (l *Local) deletePath(ctx context.Context, src string, excl []string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !info.IsDir() || len(excl) == 0 {
		return os.RemoveAll(src)
	}

	hasExclusion := false
	err = filepath.Walk(src, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		relPath, err := filepath.Rel(src, filePath)
		if err != nil {
			return err
		}

		if info.IsDir() && isExcludedSubPath(relPath, excl) {
			return nil
		}

		if isExcluded(relPath, excl) {
			hasExclusion = true
			if info.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if !info.IsDir() {
			return os.Remove(filePath)
		}

		err = os.RemoveAll(filePath)
		if err != nil {
			return err
		}

		return filepath.SkipDir
	})

	if err != nil {
		return err
	}

	// remove the whole directory if there are no actual exclusions
	if !hasExclusion {
		return os.RemoveAll(src)
	}

	return nil
}
