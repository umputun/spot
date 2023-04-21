package remote

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Executer executes commands on remote server, via ssh. Not thread-safe.
type Executer struct {
	user       string
	privateKey string

	conf   *ssh.ClientConfig
	client *ssh.Client
	host   string
}

// NewExecuter creates new Executer instance. It uses user and private key to authenticate.
func NewExecuter(user, privateKey string) (res *Executer, err error) {
	res = &Executer{
		user:       user,
		privateKey: privateKey,
	}

	res.conf, err = res.sshConfig(user, privateKey)
	return res, err
}

// Connect to remote server using ssh.
func (ex *Executer) Connect(ctx context.Context, host string) (err error) {
	log.Printf("[DEBUG] connect to %s", host)
	ex.client, err = ex.sshClient(ctx, host)
	ex.host = host
	return err
}

// Close connection to remote server.
func (ex *Executer) Close() error {
	if ex.client != nil {
		return ex.client.Close()
	}
	return nil
}

// Upload file to remote server with scp
func (ex *Executer) Upload(ctx context.Context, local, remote string, mkdir bool) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] upload %s to %s", local, remote)

	host, port, err := net.SplitHostPort(ex.host)
	if err != nil {
		return fmt.Errorf("failed to split host and port: %w", err)
	}

	req := scpReq{
		client:     ex.client,
		localFile:  local,
		remoteFile: remote,
		mkdir:      mkdir,
		remoteHost: host,
		remotePort: port,
	}
	return ex.scpUpload(ctx, req)
}

// Download file from remote server with scp
func (ex *Executer) Download(ctx context.Context, remote, local string, mkdir bool) (err error) {
	if ex.client == nil {
		return fmt.Errorf("client is not connected")
	}
	log.Printf("[DEBUG] upload %s to %s", local, remote)

	host, port, err := net.SplitHostPort(ex.host)
	if err != nil {
		return fmt.Errorf("failed to split host and port: %w", err)
	}

	req := scpReq{
		client:     ex.client,
		localFile:  local,
		remoteFile: remote,
		mkdir:      mkdir,
		remoteHost: host,
		remotePort: port,
	}
	return ex.scpDownload(ctx, req)
}

// sshClient creates ssh client connected to remote server. Caller must close session.
func (ex *Executer) sshClient(ctx context.Context, host string) (session *ssh.Client, err error) {
	log.Printf("[DEBUG] create ssh session to %s", host)
	if !strings.Contains(host, ":") {
		host += ":22"
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, host, ex.conf)
	if err != nil {
		return nil, fmt.Errorf("failed to create client connection to %s: %v", host, err)
	}
	client := ssh.NewClient(ncc, chans, reqs)
	log.Printf("[DEBUG] ssh session created to %s", host)
	return client, nil
}

// sshRun executes command on remote server. context close sends interrupt signal to remote process.
func (ex *Executer) sshRun(ctx context.Context, client *ssh.Client, command string) (err error) {
	log.Printf("[DEBUG] run ssh command %q on %s", command, client.RemoteAddr().String())
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()
	session.Stdout, session.Stderr = os.Stdout, os.Stderr

	// return session.Run(command)

	done := make(chan error)
	go func() {
		done <- session.Run(command)
	}()

	select {
	case err = <-done:
		if err != nil {
			return fmt.Errorf("failed to run command on remote server: %w", err)
		}
	case <-ctx.Done():
		err = session.Signal(ssh.SIGINT)
		if err != nil {
			return fmt.Errorf("failed to send interrupt signal to remote process: %w", err)
		}
		return fmt.Errorf("canceled: %w", ctx.Err())
	}

	return nil
}

type scpReq struct {
	localFile  string
	remoteHost string
	remotePort string
	remoteFile string
	mkdir      bool
	overwrite  bool
	client     *ssh.Client
}

// scpUpload uploads local file to remote host. Creates remote directory if mkdir is true.
func (ex *Executer) scpUpload(ctx context.Context, req scpReq) error {
	log.Printf("[INFO] upload %s to %s:%s", req.localFile, req.remoteHost, req.remoteFile)
	defer func(st time.Time) { log.Printf("[DEBUG] upload done for %q in %s", req.localFile, time.Since(st)) }(time.Now())

	if req.mkdir {
		if err := ex.sshRun(ctx, req.client, fmt.Sprintf("mkdir -p %s", filepath.Dir(req.remoteFile))); err != nil {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
	}

	remotePath := fmt.Sprintf("%s@%s:%s", ex.user, req.remoteHost, req.remoteFile)
	cmd := exec.Command("scp", "-i", ex.privateKey, "-P", req.remotePort, "-o", "StrictHostKeyChecking=no",
		req.localFile, remotePath) //nolint:gosec
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run SCP command: %v", err)
	}
	return nil
}

// scpDownload downloads remote file to local path. Creates remote directory if mkdir is true.
func (ex *Executer) scpDownload(ctx context.Context, req scpReq) error {
	log.Printf("[INFO] upload %s to %s:%s", req.localFile, req.remoteHost, req.remoteFile)
	defer func(st time.Time) { log.Printf("[DEBUG] download done for %q in %s", req.localFile, time.Since(st)) }(time.Now())

	if req.mkdir {
		if err := os.MkdirAll(filepath.Dir(req.localFile), 0750); err != nil {
			return fmt.Errorf("failed to create local directory: %w", err)
		}
	}

	remotePath := fmt.Sprintf("%s@%s:%s", ex.user, req.remoteHost, req.remoteFile)
	cmd := exec.Command("scp", "-i", ex.privateKey, "-P", req.remotePort, "-o", "StrictHostKeyChecking=no",
		remotePath, req.localFile) //nolint:gosec
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run SCP command: %v", err)
	}
	return nil
}

func (ex *Executer) sshConfig(user, privateKeyPath string) (*ssh.ClientConfig, error) {
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %vw", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %w", err)
	}
	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	} // nolint

	return sshConfig, nil
}
