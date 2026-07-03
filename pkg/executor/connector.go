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
	privateKey            string
	timeout               time.Duration
	enableAgent           bool
	enableAgentForwarding bool
	logs                  Logs
}

// NewConnector creates a new Connector for a given user and private key.
func NewConnector(privateKey string, timeout time.Duration, logs Logs) (res *Connector, err error) {
	res = &Connector{privateKey: privateKey, timeout: timeout, logs: logs}
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

// WithAgentForwarding enables ssh agent forwarding.
func (c *Connector) WithAgentForwarding() *Connector {
	log.Printf("[DEBUG] use ssh agent forwarding")
	c.enableAgentForwarding = true
	return c
}

// Connect connects to a remote hostAddr and returns a remote executer, caller must close.
func (c *Connector) Connect(ctx context.Context, hostAddr, hostName, user string) (*Remote, error) {
	log.Printf("[DEBUG] connect to %q (%s), user %q", hostAddr, hostName, user)
	client, err := c.sshClient(ctx, hostAddr, user)
	if err != nil {
		return nil, err
	}
	return &Remote{client: client, hostAddr: hostAddr, hostName: hostName, logs: c.logs.WithHost(hostAddr, hostName)}, nil
}

func (c *Connector) forwardAgent(client *ssh.Client) error {
	if !c.enableAgentForwarding {
		return nil
	}

	aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return fmt.Errorf("unable to connect to ssh agent: %w", err)
	}
	// agent.NewClient starts a background reader on the connection; close it when the ssh client
	// is done to release the socket and stop the reader
	go func() {
		_ = client.Wait()
		_ = aconn.Close()
	}()

	aclient := agent.NewClient(aconn)
	if err = agent.ForwardToAgent(client, aclient); err != nil {
		return fmt.Errorf("failed to forward agent: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create new session: %w", err)
	}
	defer session.Close()

	if err := agent.RequestAgentForwarding(session); err != nil {
		return fmt.Errorf("failed to requst agent forwarding: %w", err)
	}

	return nil
}

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

	conf, agentConn, err := c.sshConfig(user, c.privateKey)
	if err != nil {
		_ = conn.Close() // release the dialed connection, the handshake never started
		return nil, fmt.Errorf("failed to create ssh config: %w", err)
	}
	ncc, chans, reqs, err := ssh.NewClientConn(conn, host, conf)
	if agentConn != nil {
		// the agent connection is only needed for the handshake; close it to release
		// the socket and the background reader started by agent.NewClient
		_ = agentConn.Close()
	}
	if err != nil {
		// NewClientConn already closes conn on handshake failure, no need to close it here
		return nil, fmt.Errorf("failed to create client connection to %s: %v", host, err)
	}
	client := ssh.NewClient(ncc, chans, reqs)

	if err := c.forwardAgent(client); err != nil {
		_ = client.Close() // release the connection and the agent cleanup goroutine waiting on it
		return nil, fmt.Errorf("failed to forward agent to %s: %v", host, err)
	}

	log.Printf("[DEBUG] ssh session created to %s", host)
	return client, nil
}

// sshConfig makes ssh client config for the given user and private key. If the ssh agent is used for
// authentication, the returned connection to the agent should be closed by the caller after the handshake.
func (c *Connector) sshConfig(user, privateKeyPath string) (*ssh.ClientConfig, net.Conn, error) {

	// getAuth returns a list of ssh.AuthMethod to be used for authentication.
	// if ssh agent is enabled, it will be used, otherwise private key will be used.
	getAuth := func() (auth []ssh.AuthMethod, agentConn net.Conn, err error) {
		if privateKeyPath == "" || c.enableAgent {
			aconn, e := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
			if e != nil {
				return nil, nil, fmt.Errorf("unable to connect to ssh agent: %w", e)
			}
			auth = append(auth, ssh.PublicKeysCallback(agent.NewClient(aconn).Signers))
			log.Printf("[DEBUG] ssh agent found at %s", os.Getenv("SSH_AUTH_SOCK"))
			return auth, aconn, nil
		}

		key, err := os.ReadFile(privateKeyPath) // nolint
		if err != nil {
			return nil, nil, fmt.Errorf("unable to read private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
		return auth, nil, nil
	}

	auth, agentConn, err := getAuth()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get ssh auth: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // nolint
	}

	return sshConfig, agentConn, nil
}

func (c *Connector) String() string {
	return fmt.Sprintf("ssh connector with private key %s.., timeout %v, agent %v", c.privateKey[:8], c.timeout, c.enableAgent)
}
