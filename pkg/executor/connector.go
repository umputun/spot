package executor

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Connector provides factory methods to create Remote executor. Each executor is connected to a single SSH hostAddr.
type Connector struct {
	privateKey  string
	timeout     time.Duration
	enableAgent bool
}

// NewConnector creates a new Connector for a given user and private key.
func NewConnector(privateKey string, timeout time.Duration) (res *Connector, err error) {
	res = &Connector{privateKey: privateKey, timeout: timeout}
	if privateKey == "" {
		res.enableAgent = true
		log.Printf("[DEBUG] no private key provided, use ssh agent only")
		return res, nil
	}
	log.Printf("[DEBUG] use private key %q", privateKey)
	if _, err := os.Stat(privateKey); os.IsNotExist(err) {
		return nil, fmt.Errorf("private key file %q does not exist", privateKey)
	}
	return res, nil
}

// WithAgent enables ssh agent for authentication.
func (c *Connector) WithAgent() *Connector {
	log.Printf("[DEBUG] use ssh agent")
	c.enableAgent = true
	return c
}

// Connect connects to a remote hostAddr and returns a remote executer, caller must close.
func (c *Connector) Connect(ctx context.Context, hostAddr, hostName, user string) (*Remote, error) {
	log.Printf("[DEBUG] connect to %q (%s), user %q", hostAddr, hostName, user)
	client, err := c.sshClient(ctx, hostAddr, user)
	if err != nil {
		return nil, err
	}
	return &Remote{client: client, hostAddr: hostAddr, hostName: hostName}, nil
}

// sshClient creates ssh client connected to remote server. Caller must close session.
func (c *Connector) sshClient(ctx context.Context, host, user string) (session *ssh.Client, err error) {
	log.Printf("[DEBUG] create ssh session to %s, user %s", host, user)
	if !strings.Contains(host, ":") {
		host += ":22"
	}

	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	conf, err := c.sshConfig(user, c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh config: %w", err)
	}
	ncc, chans, reqs, err := ssh.NewClientConn(conn, host, conf)
	if err != nil {
		return nil, fmt.Errorf("failed to create client connection to %s: %v", host, err)
	}
	client := ssh.NewClient(ncc, chans, reqs)
	log.Printf("[DEBUG] ssh session created to %s", host)
	return client, nil
}

func (c *Connector) sshConfig(user, privateKeyPath string) (*ssh.ClientConfig, error) {

	// getAuth returns a list of ssh.AuthMethod to be used for authentication.
	// if ssh agent is enabled, it will be used, otherwise private key will be used.
	getAuth := func() (auth []ssh.AuthMethod, err error) {
		if privateKeyPath == "" || c.enableAgent {
			if aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
				auth = append(auth, ssh.PublicKeysCallback(agent.NewClient(aconn).Signers))
				log.Printf("[DEBUG] ssh agent found at %s", os.Getenv("SSH_AUTH_SOCK"))
			} else {
				return nil, fmt.Errorf("unable to connect to ssh agent: %w", err)
			}
			return auth, nil
		}

		key, err := os.ReadFile(privateKeyPath) // nolint
		if err != nil {
			return nil, fmt.Errorf("unable to read private key: %vw", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("unable to parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
		return auth, nil
	}

	auth, err := getAuth()
	if err != nil {
		return nil, fmt.Errorf("failed to get ssh auth: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // nolint
	}

	return sshConfig, nil
}
