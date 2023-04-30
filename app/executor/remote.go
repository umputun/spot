package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Remote executes commands on remote server, via ssh. Not thread-safe.
type Remote struct {
	client   *ssh.Client
	hostAddr string
	hostName string
}

// Close connection to remote server.
func (ex *Remote) Close() error {
	if ex.client != nil {
		return ex.client.Close()
	}
	return nil
}

// Run command on remote server.
func (ex *Remote) Run(ctx context.Context, cmd string, verbose bool) (out []string, err error) {
	if ex.client == nil {
		return nil, fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] run %s", cmd)

	return ex.sshRun(ctx, ex.client, cmd, verbose)
}

// Upload file to remote server with scp
func (ex *Remote) Upload(ctx context.Context, local, remote string, mkdir bool) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] upload %s to %s", local, remote)

	host, port, err := net.SplitHostPort(ex.hostAddr)
	if err != nil {
		return fmt.Errorf("failed to split hostAddr and port: %w", err)
	}

	req := sftpReq{
		client:     ex.client,
		localFile:  local,
		remoteFile: remote,
		mkdir:      mkdir,
		remoteHost: host,
		remotePort: port,
	}
	return ex.sftpUpload(ctx, req)
}

// Download file from remote server with scp
func (ex *Remote) Download(ctx context.Context, remote, local string, mkdir bool) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] upload %s to %s", local, remote)

	host, port, err := net.SplitHostPort(ex.hostAddr)
	if err != nil {
		return fmt.Errorf("failed to split hostAddr and port: %w", err)
	}

	req := sftpReq{
		client:     ex.client,
		localFile:  local,
		remoteFile: remote,
		mkdir:      mkdir,
		remoteHost: host,
		remotePort: port,
	}
	return ex.sftpDownload(ctx, req)
}

// Sync compares local and remote files and uploads unmatched files, recursively.
func (ex *Remote) Sync(ctx context.Context, localDir, remoteDir string, del bool) ([]string, error) {
	localFiles, err := ex.getLocalFilesProperties(localDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files properties for %s: %w", localDir, err)
	}

	remoteFiles, err := ex.getRemoteFilesProperties(ctx, remoteDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote files properties for %s: %w", remoteDir, err)
	}

	unmatchedFiles, deletedFiles := ex.findUnmatchedFiles(localFiles, remoteFiles)
	for _, file := range unmatchedFiles {
		localPath := filepath.Join(localDir, file)
		remotePath := filepath.Join(remoteDir, file)
		if err = ex.Upload(ctx, localPath, remotePath, true); err != nil {
			return nil, fmt.Errorf("failed to upload %s to %s: %w", localPath, remotePath, err)
		}
		log.Printf("[INFO] synced %s to %s", localPath, remotePath)
	}

	if del {
		for _, file := range deletedFiles {
			if err = ex.Delete(ctx, filepath.Join(remoteDir, file), false); err != nil {
				return nil, fmt.Errorf("failed to delete %s: %w", file, err)
			}
		}
	}

	return unmatchedFiles, nil
}

// Delete file on remote server. Recursively if recursive is true.
// if file or directory does not exist, returns nil, i.e. no error.
func (ex *Remote) Delete(ctx context.Context, remoteFile string, recursive bool) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}

	sftpClient, err := sftp.NewClient(ex.client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

	fileInfo, err := sftpClient.Stat(remoteFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to stat %s: %w", remoteFile, err)
	}

	if fileInfo.IsDir() && recursive {
		walker := sftpClient.Walk(remoteFile)

		var pathsToDelete []string
		for walker.Step() {
			if walker.Err() != nil {
				continue
			}
			pathsToDelete = append(pathsToDelete, walker.Path())
		}

		// Delete files and directories in reverse order
		for i := len(pathsToDelete) - 1; i >= 0; i-- {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			path := pathsToDelete[i]
			fileInfo, stErr := sftpClient.Stat(path)
			if stErr != nil {
				return fmt.Errorf("failed to stat %s: %w", path, stErr)
			}

			if fileInfo.IsDir() {
				err = sftpClient.RemoveDirectory(path)
			} else {
				err = sftpClient.Remove(path)
			}

			if err != nil {
				return fmt.Errorf("failed to delete %s: %w", path, err)
			}
		}

		log.Printf("[INFO] deleted recursevly %s", remoteFile)
	} else {
		err = sftpClient.Remove(remoteFile)
		if err != nil {
			return fmt.Errorf("failed to delete %s: %w", remoteFile, err)
		}
		log.Printf("[INFO] deleted %s", remoteFile)
	}

	return nil
}

// sshRun executes command on remote server. context close sends interrupt signal to remote process.
func (ex *Remote) sshRun(ctx context.Context, client *ssh.Client, command string, verbose bool) (out []string, err error) {
	log.Printf("[DEBUG] run ssh command %q on %s", command, client.RemoteAddr().String())
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	outLog, errLog := MakeOutAndErrWriters(ex.hostAddr, ex.hostName, verbose)
	outLog.Write([]byte(command)) //nolint

	var stdoutBuf bytes.Buffer
	mwr := io.MultiWriter(outLog, &stdoutBuf)
	session.Stdout, session.Stderr = mwr, errLog

	done := make(chan error)
	go func() {
		done <- session.Run(command)
	}()

	select {
	case err = <-done:
		if err != nil {
			return nil, fmt.Errorf("failed to run command on remote server: %w", err)
		}
	case <-ctx.Done():
		if err = session.Signal(ssh.SIGINT); err != nil {
			return nil, fmt.Errorf("failed to send interrupt signal to remote process: %w", err)
		}
		return nil, fmt.Errorf("canceled: %w", ctx.Err())
	}

	for _, line := range strings.Split(stdoutBuf.String(), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

type sftpReq struct {
	localFile  string
	remoteHost string
	remotePort string
	remoteFile string
	mkdir      bool
	client     *ssh.Client
}

func (ex *Remote) sftpUpload(ctx context.Context, req sftpReq) error {
	log.Printf("[DEBUG] upload %s to %s:%s", req.localFile, req.remoteHost, req.remoteFile)
	defer func(st time.Time) {
		log.Printf("[INFO] uploaded %s to %s:%s in %s", req.localFile, req.remoteHost, req.remoteFile, time.Since(st))
	}(time.Now())

	sftpClient, err := sftp.NewClient(req.client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

	if req.mkdir {
		if e := sftpClient.MkdirAll(filepath.Dir(req.remoteFile)); e != nil {
			return fmt.Errorf("failed to create remote directory: %v", e)
		}
	}

	inpFh, err := os.Open(req.localFile)
	if err != nil {
		return fmt.Errorf("failed to open local file %s: %v", req.localFile, err)
	}
	defer inpFh.Close() // nolint

	inpFi, err := os.Stat(req.localFile)
	if err != nil {
		return fmt.Errorf("failed to stat local file %s: %v", req.localFile, err)
	}
	log.Printf("[DEBUG] file mode for %s: %s", req.localFile, fmt.Sprintf("%04o", inpFi.Mode().Perm()))

	remoteFh, err := sftpClient.Create(req.remoteFile)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %v", err)
	}
	defer remoteFh.Close() // nolint

	errCh := make(chan error, 1)
	go func() {
		_, e := io.Copy(remoteFh, inpFh)
		errCh <- e
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("failed to copy file: %v", ctx.Err())
	case err = <-errCh:
		if err != nil {
			return fmt.Errorf("failed to copy file: %v", err)
		}
	}

	if err = remoteFh.Chmod(inpFi.Mode().Perm()); err != nil {
		return fmt.Errorf("failed to set permissions on remote file: %v", err)
	}

	if err = sftpClient.Chtimes(req.remoteFile, inpFi.ModTime(), inpFi.ModTime()); err != nil {
		return fmt.Errorf("failed to set modification time of remote file %s: %v", req.remoteFile, err)
	}

	return nil
}
func (ex *Remote) sftpDownload(ctx context.Context, req sftpReq) error {
	log.Printf("[INFO] download %s from %s:%s", req.localFile, req.remoteHost, req.remoteFile)
	defer func(st time.Time) { log.Printf("[DEBUG] download done for %q in %s", req.localFile, time.Since(st)) }(time.Now())

	if req.mkdir {
		if err := os.MkdirAll(filepath.Dir(req.localFile), 0o750); err != nil {
			return fmt.Errorf("failed to create local directory: %w", err)
		}
	}

	sftpClient, err := sftp.NewClient(req.client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

	outFh, err := os.Create(req.localFile)
	if err != nil {
		return fmt.Errorf("failed to open local file %s: %v", req.localFile, err)
	}
	defer outFh.Close() // nolint

	remoteFh, err := sftpClient.Open(req.remoteFile)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %v", err)
	}
	defer remoteFh.Close() // nolint

	errCh := make(chan error, 1)
	go func() {
		_, e := io.Copy(outFh, remoteFh)
		errCh <- e
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err = <-errCh:
		if err != nil {
			return fmt.Errorf("failed to copy file: %v", err)
		}
	}

	return outFh.Sync() // nolint
}

type fileProperties struct {
	Size     int64
	Time     time.Time
	FileName string
}

// getLocalFilesProperties returns map of file properties for all files in the local directory.
func (ex *Remote) getLocalFilesProperties(dir string) (map[string]fileProperties, error) {
	fileProps := make(map[string]fileProperties)

	// walk local directory and get file properties
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		fileProps[relPath] = fileProperties{Size: info.Size(), Time: info.ModTime(), FileName: info.Name()}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk local directory %s: %w", dir, err)
	}

	return fileProps, nil
}

func (ex *Remote) getRemoteFilesProperties(ctx context.Context, dir string) (map[string]fileProperties, error) {
	sftpClient, err := sftp.NewClient(ex.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

	fileProps := make(map[string]fileProperties)

	walker := sftpClient.Walk(dir)

	for walker.Step() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if walker.Err() != nil {
			if walker.Err().Error() == "file does not exist" {
				return fileProps, nil
			}
			return nil, fmt.Errorf("failed to walk remote directory: %v", walker.Err())
		}

		fileInfo, fullPath := walker.Stat(), walker.Path()
		if fileInfo.IsDir() {
			continue
		}

		relPath, err := filepath.Rel(dir, fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get relative path for %s: %w", fullPath, err)
		}

		fileProps[relPath] = fileProperties{Size: fileInfo.Size(), Time: fileInfo.ModTime(), FileName: fullPath}
	}

	return fileProps, nil
}

func (ex *Remote) findUnmatchedFiles(local, remote map[string]fileProperties) (updatedFiles, deletedFiles []string) {
	isWithinOneSecond := func(t1, t2 time.Time) bool {
		diff := t1.Sub(t2)
		if diff < 0 {
			diff = -diff
		}
		return diff <= time.Second
	}

	updatedFiles = []string{}
	deletedFiles = []string{}

	for localPath, localProps := range local {
		remoteProps, exists := remote[localPath]
		if !exists || localProps.Size != remoteProps.Size || !isWithinOneSecond(localProps.Time, remoteProps.Time) {
			updatedFiles = append(updatedFiles, localPath)
		}
	}

	// Check for deleted files
	for remotePath := range remote {
		if _, exists := local[remotePath]; !exists {
			deletedFiles = append(deletedFiles, remotePath)
		}
	}

	sort.Slice(updatedFiles, func(i, j int) bool { return updatedFiles[i] < updatedFiles[j] })
	sort.Slice(deletedFiles, func(i, j int) bool { return deletedFiles[i] < deletedFiles[j] })

	return updatedFiles, deletedFiles
}
