package executor

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"strings"
)

// Dry is an executor for dry run, just prints commands and files to be copied, synced, deleted.
// Useful for debugging and testing, doesn't actually execute anything.
type Dry struct {
	hostAddr string
	hostName string
	secrets  []string
}

// NewDry creates new executor for dry run
func NewDry(hostAddr, hostName string) *Dry {
	return &Dry{hostAddr: hostAddr, hostName: hostName}
}

// SetSecrets sets secrets for the executor
func (ex *Dry) SetSecrets(secrets []string) {
	ex.secrets = secrets
}

// Run shows the command content, doesn't execute it
func (ex *Dry) Run(_ context.Context, cmd string, opts *RunOpts) (out []string, err error) {
	log.Printf("[DEBUG] run %s", cmd)
	outLog, _ := MakeOutAndErrWriters(ex.hostAddr, ex.hostName, opts != nil && opts.Verbose, ex.secrets)
	var stdoutBuf bytes.Buffer
	mwr := io.MultiWriter(outLog, &stdoutBuf)
	mwr.Write([]byte(cmd)) //nolint
	for _, line := range strings.Split(stdoutBuf.String(), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// Upload doesn't actually upload, just prints the command
func (ex *Dry) Upload(_ context.Context, local, remote string, opts *UpDownOpts) (err error) {
	var mkdir bool
	var exclude []string

	if opts != nil {
		mkdir = opts.Mkdir
		exclude = opts.Exclude
	}

	log.Printf("[DEBUG] upload %s to %s, mkdir: %v, exclude: %v", local, remote, mkdir, exclude)
	if strings.Contains(remote, "spot-script") {
		// this is a temp script created by spot to perform script execution on remote host
		outLog, outErr := MakeOutAndErrWriters(ex.hostAddr, ex.hostName, true, ex.secrets)
		outErr.Write([]byte("command script " + remote)) //nolint
		// read local file and write it to outLog
		f, err := os.Open(local) //nolint
		if err != nil {
			return err
		}
		defer f.Close() //nolint ro file

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			outLog.Write([]byte(scanner.Text())) //nolint
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	}
	return nil
}

// Download file from remote server with scp
func (ex *Dry) Download(_ context.Context, remote, local string, opts *UpDownOpts) (err error) {
	var mkdir bool
	var exclude []string

	if opts != nil {
		mkdir = opts.Mkdir
		exclude = opts.Exclude
	}

	log.Printf("[DEBUG] download %s to %s, mkdir: %v, exclude: %v", local, remote, mkdir, exclude)
	return nil
}

// Sync doesn't sync anything, just prints the command
func (ex *Dry) Sync(_ context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error) {
	del := opts != nil && opts.Delete
	exclude := []string{}
	if opts != nil {
		exclude = opts.Exclude
	}
	log.Printf("[DEBUG] sync %s to %s, delete: %v, exlcude: %v", localDir, remoteDir, del, exclude) //nolint
	return nil, nil
}

// Delete doesn't delete anything, just prints the command
func (ex *Dry) Delete(_ context.Context, remoteFile string, opts *DeleteOpts) (err error) {
	var recursive bool
	var exclude []string

	if opts != nil {
		recursive = opts.Recursive
		exclude = opts.Exclude
	}
	log.Printf("[DEBUG] delete %s, recursive: %v, exclude: %v", remoteFile, recursive, exclude)
	return nil
}

// Close doesn't do anything
func (ex *Dry) Close() error {
	return nil
}
