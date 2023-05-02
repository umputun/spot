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
}

// NewDry creates new executor for dry run
func NewDry(hostAddr, hostName string) *Dry {
	return &Dry{hostAddr: hostAddr, hostName: hostName}
}

// Run shows the command content, doesn't execute it
func (ex *Dry) Run(_ context.Context, cmd string, verbose bool) (out []string, err error) {
	log.Printf("[DEBUG] run %s", cmd)
	outLog, _ := MakeOutAndErrWriters(ex.hostAddr, ex.hostName, verbose)
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
func (ex *Dry) Upload(_ context.Context, local, remote string, mkdir bool) (err error) {
	log.Printf("[DEBUG] upload %s to %s, mkdir: %v", local, remote, mkdir)
	if strings.Contains(remote, "spot-script") {
		outLog, outErr := MakeOutAndErrWriters(ex.hostAddr, ex.hostName, true)
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
func (ex *Dry) Download(_ context.Context, remote, local string, mkdir bool) (err error) {
	log.Printf("[DEBUG] download %s to %s, mkdir: %v", local, remote, mkdir)
	return nil
}

// Sync doesn't sync anything, just prints the command
func (ex *Dry) Sync(_ context.Context, localDir, remoteDir string, del bool) ([]string, error) {
	log.Printf("[DEBUG] sync %s to %s, delite: %v", localDir, remoteDir, del)
	return nil, nil
}

// Delete doesn't delete anything, just prints the command
func (ex *Dry) Delete(_ context.Context, remoteFile string, recursive bool) (err error) {
	log.Printf("[DEBUG] delete %s, recursive: %v", remoteFile, recursive)
	return nil
}

// Close doesn't do anything
func (ex *Dry) Close() error {
	return nil
}
