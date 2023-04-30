package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-pkgz/fileutils"
)

// Local is a runner for local execution. Similar to remote, but without ssh, just exec on localhost and local copy/delete/sync
type Local struct{}

// Run executes command on local host, inside the shell
func (l *Local) Run(ctx context.Context, cmd string, verbose bool) (out []string, err error) {
	command := exec.CommandContext(ctx, "sh", "-c", cmd)
	var stdoutBuf bytes.Buffer
	mwr := io.MultiWriter(NewStdoutLogWriter(">", "DEBUG"), &stdoutBuf)
	command.Stdout, command.Stderr = mwr, NewStdoutLogWriter("!", "WARN")
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
func (l *Local) Upload(_ context.Context, src, dst string, mkdir bool) (err error) {
	if mkdir {
		if err = os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return fmt.Errorf("can't create local dir %s: %w", filepath.Dir(dst), err)
		}
	}
	if err = l.copyFile(src, dst); err != nil {
		return fmt.Errorf("can't copy local file from %s to %s: %w", src, dst, err)
	}
	return nil
}

// Download just copy file from one place to another
func (l *Local) Download(_ context.Context, src, dst string, mkdir bool) (err error) {
	return l.Upload(context.Background(), src, dst, mkdir) // same as upload for local
}

// Sync directories from src to dst
func (l *Local) Sync(ctx context.Context, src, dst string, del bool) ([]string, error) {
	copiedFiles, err := l.syncSrcToDst(ctx, src, dst)
	if err != nil {
		return nil, err
	}

	if del {
		if err := l.removeExtraDstFiles(ctx, src, dst); err != nil {
			return nil, err
		}
	}

	return copiedFiles, nil
}

// Delete file or directory
func (l *Local) Delete(_ context.Context, remoteFile string, recursive bool) (err error) {
	if !recursive {
		return os.Remove(remoteFile)
	}
	return os.RemoveAll(remoteFile)
}

// Close does nothing for local
func (l *Local) Close() error { return nil }

func (l *Local) syncSrcToDst(ctx context.Context, src, dst string) ([]string, error) {
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
	return filepath.Walk(dst, func(dstPath string, info os.FileInfo, err error) error {
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
			if e := os.RemoveAll(dstPath); e != nil {
				return e
			}
		}
		return nil
	})
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
