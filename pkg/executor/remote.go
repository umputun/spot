package executor

import (
	"bytes"
	"context"
	"errors"
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
	logs     Logs
}

// Close connection to remote server.
func (ex *Remote) Close() error {
	if ex.client != nil {
		return ex.client.Close()
	}
	return nil
}

// Run command on remote server.
func (ex *Remote) Run(ctx context.Context, cmd string, _ *RunOpts) (out []string, err error) {
	if ex.client == nil {
		return nil, fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] run %s", cmd)

	return ex.sshRun(ctx, ex.client, cmd)
}

// Upload file to remote server with scp
func (ex *Remote) Upload(ctx context.Context, local, remote string, opts *UpDownOpts) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] upload %s to %s", local, remote)

	host, port, err := net.SplitHostPort(ex.hostAddr)
	if err != nil {
		return fmt.Errorf("failed to split hostAddr and port: %w", err)
	}

	// check if the local parameter contains a glob pattern
	matches, err := filepath.Glob(local)
	if err != nil {
		return fmt.Errorf("failed to expand glob pattern %s: %w", local, err)
	}

	if len(matches) == 0 { // no match
		return fmt.Errorf("source file %q not found", local)
	}

	var exclude []string
	if opts != nil {
		exclude = opts.Exclude
	}

	// upload each file matching the glob pattern. If no glob pattern is found, the file is matched as is
	for _, match := range matches {
		relPath, e := filepath.Rel(filepath.Dir(local), match)
		if e != nil {
			return fmt.Errorf("failed to build relative path for %s: %w", match, err)
		}
		if isExcluded(relPath, exclude) {
			continue
		}

		remoteFile := remote
		if len(matches) > 1 { // if there are multiple files, treat remote as a directory
			remoteFile = filepath.Join(remote, filepath.Base(match))
		}
		req := sftpReq{
			client:     ex.client,
			localFile:  match,
			remoteFile: remoteFile,
			mkdir:      opts != nil && opts.Mkdir,
			force:      opts != nil && opts.Force,
			remoteHost: host,
			remotePort: port,
		}
		if err := ex.sftpUpload(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// Download file from remote server with scp
func (ex *Remote) Download(ctx context.Context, remote, local string, opts *UpDownOpts) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] download %s to %s", local, remote)

	host, port, err := net.SplitHostPort(ex.hostAddr)
	if err != nil {
		return fmt.Errorf("failed to split hostAddr and port: %w", err)
	}

	var mkdir bool
	var exclude []string

	if opts != nil {
		mkdir = opts.Mkdir
		exclude = opts.Exclude
	}

	remoteFiles, err := ex.findMatchedFiles(remote, exclude)
	if err != nil {
		return fmt.Errorf("failed to list remote files by glob for %s: %w", remote, err)
	}

	for _, remoteFile := range remoteFiles {
		localFile := local

		// if the remote basename does not equal the remoteFile basename,
		// treat remote as a glob pattern and local as a directory
		if filepath.Base(remote) != filepath.Base(remoteFile) {
			localFile = filepath.Join(local, filepath.Base(remoteFile))
		}

		req := sftpReq{
			client:     ex.client,
			localFile:  localFile,
			remoteFile: remoteFile,
			mkdir:      mkdir,
			remoteHost: host,
			remotePort: port,
		}
		err = ex.sftpDownload(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to download remote file %s: %w", remoteFile, err)
		}
	}

	return nil
}

// Sync compares local and remote files and uploads unmatched files, recursively.
func (ex *Remote) Sync(ctx context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error) {
	localFiles, err := ex.getLocalFilesProperties(localDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files properties for %s: %w", localDir, err)
	}

	excl := []string{}
	if opts != nil {
		excl = opts.Exclude
	}
	remoteFiles, err := ex.getRemoteFilesProperties(ctx, remoteDir, excl)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote files properties for %s: %w", remoteDir, err)
	}

	unmatchedFiles, deletedFiles := ex.findUnmatchedFiles(localFiles, remoteFiles, excl)
	for _, file := range unmatchedFiles {
		localPath := filepath.Join(localDir, file)
		remotePath := filepath.Join(remoteDir, file)
		if err = ex.Upload(ctx, localPath, remotePath, &UpDownOpts{Mkdir: true}); err != nil {
			return nil, fmt.Errorf("failed to upload %s to %s: %w", localPath, remotePath, err)
		}
		log.Printf("[INFO] synced %s to %s", localPath, remotePath)
	}

	if opts != nil && opts.Delete {
		// delete remote files which are not in local.
		// if the missing file is a directory, delete it recursively.
		// note: this may cause attempts to remove files from already deleted directories, but it's ok, Delete is idempotent.
		for _, file := range deletedFiles {
			deleteOpts := &DeleteOpts{Recursive: remoteFiles[file].IsDir}
			if err = ex.Delete(ctx, filepath.Join(remoteDir, file), deleteOpts); err != nil {
				return nil, fmt.Errorf("failed to delete %s: %w", file, err)
			}
		}
	}

	return unmatchedFiles, nil
}

// Delete file on remote server. Recursively if recursive is true.
// if a file or directory does not exist, returns nil, i.e. no error.
func (ex *Remote) Delete(ctx context.Context, remoteFile string, opts *DeleteOpts) (err error) {
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

	var recursive bool
	var exclude []string

	if opts != nil {
		recursive = opts.Recursive
		exclude = opts.Exclude
	}

	if fileInfo.IsDir() && recursive {
		hasExclusion := false
		walker := sftpClient.Walk(remoteFile)

		var pathsToDelete []string
		for walker.Step() {
			if walker.Err() != nil {
				continue
			}

			path := walker.Path()
			relPath, e := filepath.Rel(remoteFile, path)
			if e != nil {
				return e
			}

			// skip parent directories of the excluded files
			if walker.Stat().IsDir() && (relPath == "." || isExcludedSubPath(relPath, exclude)) {
				continue
			}

			if isExcluded(relPath, exclude) {
				hasExclusion = true
				if walker.Stat().IsDir() {
					walker.SkipDir()
				}

				continue
			}

			pathsToDelete = append(pathsToDelete, walker.Path())
		}

		// there is no actual exclusions
		if !hasExclusion {
			pathsToDelete = append([]string{remoteFile}, pathsToDelete...)
		}

		// Delete files and directories in reverse order
		for i := len(pathsToDelete) - 1; i >= 0; i-- {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			path := pathsToDelete[i]
			fi, stErr := sftpClient.Stat(path)
			if stErr != nil {
				return fmt.Errorf("failed to stat %s: %w", path, stErr)
			}

			if fi.IsDir() {
				err = sftpClient.RemoveDirectory(path)
			} else {
				err = sftpClient.Remove(path)
			}

			if err != nil {
				return fmt.Errorf("failed to delete %s: %w", path, err)
			}
		}

		log.Printf("[INFO] deleted recursively %s from %s", remoteFile, ex.hostName)
	}

	if fileInfo.IsDir() && !recursive {
		if err = sftpClient.RemoveDirectory(remoteFile); err != nil {
			return fmt.Errorf("failed to delete %s: %w", remoteFile, err)
		}
		log.Printf("[INFO] deleted directory %s", remoteFile)
	}

	if !fileInfo.IsDir() {
		if err = sftpClient.Remove(remoteFile); err != nil {
			return fmt.Errorf("failed to delete %s: %w", remoteFile, err)
		}
		log.Printf("[INFO] deleted %s", remoteFile)
	}

	return nil
}

// sshRun executes command on remote server. context close sends interrupt signal to the remote process.
func (ex *Remote) sshRun(ctx context.Context, client *ssh.Client, command string) (out []string, err error) {
	log.Printf("[DEBUG] run ssh command %q on %s", command, client.RemoteAddr().String())
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	ex.logs.Out.Write([]byte(command)) // nolint

	var stdoutBuf bytes.Buffer
	mwr := io.MultiWriter(ex.logs.Out, &stdoutBuf)
	session.Stdout, session.Stderr = mwr, ex.logs.Err

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
	force      bool
	client     *ssh.Client
}

func (ex *Remote) sftpUpload(ctx context.Context, req sftpReq) error {
	log.Printf("[DEBUG] upload %s to %s:%s", req.localFile, req.remoteHost, req.remoteFile)
	defer func(st time.Time) {
		log.Printf("[INFO] uploaded %s to %s:%s in %s", req.localFile, req.remoteHost, req.remoteFile, time.Since(st))
	}(time.Now())

	sftpClient, err := sftp.NewClient(req.client, sftp.UseConcurrentWrites(true))
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

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

	remoteFi, err := sftpClient.Stat(req.remoteFile)
	if err == nil {
		// if remote file exists, and has the same size, mod time and mode, skip upload. Force flag overrides this.
		isSame := !req.force && remoteFi.Size() == inpFi.Size() &&
			isWithinOneSecond(remoteFi.ModTime(), inpFi.ModTime()) && remoteFi.Mode() == inpFi.Mode()
		if isSame {
			log.Printf("[INFO] remote file %s identical to local file %s, skipping upload", req.remoteFile, req.localFile)
			return nil
		}
	}

	if req.mkdir {
		rdir := filepath.Dir(req.remoteFile)
		if e := sftpClient.MkdirAll(rdir); e != nil {
			return fmt.Errorf("failed to create remote directory %q: %v", rdir, e)
		}
	}

	remoteFh, err := sftpClient.Create(req.remoteFile)
	if err != nil {
		return fmt.Errorf("failed to create remote file %q: %v", req.remoteFile, err)
	}
	defer remoteFh.Close()

	errCh := make(chan error, 1)
	go func() {
		_, e := io.Copy(remoteFh, inpFh)
		errCh <- e
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("failed to copy file %q: %v", req.remoteFile, ctx.Err())
	case err = <-errCh:
		if err != nil {
			return fmt.Errorf("failed to copy file %q: %v", req.remoteFile, err)
		}
	}

	if err = remoteFh.Chmod(inpFi.Mode().Perm()); err != nil {
		return fmt.Errorf("failed to set permissions on remote file %q: %v", req.remoteFile, err)
	}

	if err = sftpClient.Chtimes(req.remoteFile, inpFi.ModTime(), inpFi.ModTime()); err != nil {
		return fmt.Errorf("failed to set modification time of remote file %q: %v", req.remoteFile, err)
	}

	return nil
}

func (ex *Remote) sftpDownload(ctx context.Context, req sftpReq) error {
	log.Printf("[INFO] download %s from %s:%s", req.localFile, req.remoteHost, req.remoteFile)
	defer func(st time.Time) { log.Printf("[DEBUG] download done for %q in %s", req.localFile, time.Since(st)) }(time.Now())

	sftpClient, err := sftp.NewClient(req.client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

	remoteFh, err := sftpClient.Open(req.remoteFile)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %v", err)
	}
	defer remoteFh.Close() // nolint

	remoteFi, err := remoteFh.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat remote file: %v", err)
	}

	// Check if local file exists, if not create it.
	if _, stErr := os.Stat(req.localFile); stErr != nil {
		if !os.IsNotExist(stErr) {
			return fmt.Errorf("failed to stat local file: %v", stErr)
		}
		outFh, crErr := os.Create(req.localFile)
		if crErr != nil {
			return fmt.Errorf("failed to open local file %s: %v", req.localFile, crErr)
		}
		defer outFh.Close() // nolint
	}

	localFh, err := os.OpenFile(req.localFile, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open local file: %v", err)
	}
	defer localFh.Close() // nolint

	localFi, err := localFh.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat local file: %v", err)
	}

	// if the local file size and mod time are the same as the remote file, don't download. Force flag overrides this.
	if !req.force && localFi.Size() == remoteFi.Size() && isWithinOneSecond(localFi.ModTime(), remoteFi.ModTime()) {
		log.Printf("[INFO] local file %s is up-to-date.", req.localFile)
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		_, e := io.Copy(localFh, remoteFh)
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

	return localFh.Sync() // nolint
}

type fileProperties struct {
	Size     int64
	Time     time.Time
	FileName string
	IsDir    bool
}

// getLocalFilesProperties returns map of file properties for all files in the local directory.
func (ex *Remote) getLocalFilesProperties(dir string) (map[string]fileProperties, error) {
	fileProps := make(map[string]fileProperties)

	// walk local directory and get file properties
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == "." {
			return nil
		}
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		fileProps[relPath] = fileProperties{Size: info.Size(), Time: info.ModTime(), FileName: info.Name(), IsDir: info.IsDir()}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk local directory %s: %w", dir, err)
	}

	return fileProps, nil
}

// this function is used to get file properties for remote files. Instead of using sftpClient.Walk, we use
// sftpClient.ReadDir to get file properties for all files in the remote directory. This is because sftpClient.Walk
// doesn't support excluding files/directories, and we can speed up the process by excluding files/directories that
// are not needed.
func (ex *Remote) getRemoteFilesProperties(ctx context.Context, dir string, excl []string) (map[string]fileProperties, error) {
	sftpClient, e := sftp.NewClient(ex.client)
	if e != nil {
		return nil, fmt.Errorf("failed to create sftp client: %v", e)
	}
	defer sftpClient.Close()

	fileProps := make(map[string]fileProperties)

	// recursive function to walk remote directory and get file properties
	var processEntry func(ctx context.Context, client *sftp.Client, root string, excl []string, dir string) error

	processEntry = func(ctx context.Context, client *sftp.Client, root string, excl []string, dir string) error {
		log.Printf("[DEBUG] processing remote directory %s", dir)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		entries, err := client.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}

		for _, entry := range entries {
			fullPath := filepath.Join(dir, entry.Name())
			relPath, err := filepath.Rel(root, fullPath)
			if err != nil {
				log.Printf("[WARN] failed to get relative path for %s: %v", fullPath, err)
				continue
			}

			if isExcluded(relPath, excl) {
				continue
			}

			if entry.IsDir() {
				err := processEntry(ctx, client, root, excl, fullPath)
				if err != nil && err.Error() != "context canceled" {
					log.Printf("[WARN] failed to process directory %s: %v", fullPath, err)
				}
				continue
			}

			fileProps[relPath] = fileProperties{Size: entry.Size(), Time: entry.ModTime(), FileName: fullPath, IsDir: entry.IsDir()}
		}
		return nil
	}

	if err := processEntry(ctx, sftpClient, dir, excl, dir); err != nil {
		return nil, fmt.Errorf("failed to get remote files properties for %s: %w", dir, err)
	}

	return fileProps, nil
}

func (ex *Remote) findUnmatchedFiles(local, remote map[string]fileProperties, excl []string) (updatedFiles, deletedFiles []string) {
	updatedFiles = []string{}
	deletedFiles = []string{}

	for localPath, localProps := range local {
		if localProps.IsDir {
			continue // don't put directories to unmatched files, no need to upload them
		}
		if isExcluded(localPath, excl) {
			continue // don't put excluded files to unmatched files, no need to upload them
		}
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

func (ex *Remote) findMatchedFiles(remote string, excl []string) ([]string, error) {
	sftpClient, err := sftp.NewClient(ex.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create sftp client: %v", err)
	}
	defer sftpClient.Close()

	matches, err := sftpClient.Glob(remote)
	if err != nil {
		return nil, fmt.Errorf("failed to list remote files: %v", err)
	}

	files := make([]string, 0, len(matches))
	for _, match := range matches {
		relPath, e := filepath.Rel(filepath.Dir(remote), match)
		if e != nil {
			return nil, fmt.Errorf("failed to build relative path for %s: %w", match, err)
		}
		if isExcluded(relPath, excl) {
			continue
		}

		files = append(files, match)
	}

	return files, nil
}
