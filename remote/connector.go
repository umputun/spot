package remote

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Connector provides factory methods to create Executer. Each Executer is connected to a single SSH host.
type Connector struct {
	user       string
	privateKey string
	conf       *ssh.ClientConfig
}

// NewConnector creates a new Connector for a given user and private key.
func NewConnector(user, privateKey string) (res *Connector, err error) {
	res = &Connector{user: user, privateKey: privateKey}
	if res.conf, err = res.sshConfig(user, privateKey); err != nil {
		return nil, err
	}
	return res, nil
}

// Connect connects to a remote host and returns an Executer, caller must close the Executer.
func (c *Connector) Connect(ctx context.Context, host string) (*Executer, error) {
	log.Printf("[DEBUG] connect to %s", host)
	client, err := c.sshClient(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to create client connection to %s: %v", host, err)
	}
	return &Executer{client: client, host: host}, nil
}

// sshClient creates ssh client connected to remote server. Caller must close session.
func (c *Connector) sshClient(ctx context.Context, host string) (session *ssh.Client, err error) {
	log.Printf("[DEBUG] create ssh session to %s", host)
	if !strings.Contains(host, ":") {
		host += ":22"
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, host, c.conf)
	if err != nil {
		return nil, fmt.Errorf("failed to create client connection to %s: %v", host, err)
	}
	client := ssh.NewClient(ncc, chans, reqs)
	log.Printf("[DEBUG] ssh session created to %s", host)
	return client, nil
}

func (c *Connector) sshConfig(user, privateKeyPath string) (*ssh.ClientConfig, error) {
	key, err := os.ReadFile(privateKeyPath) //nolint
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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint
	}

	return sshConfig, nil
}
