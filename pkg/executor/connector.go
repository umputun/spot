package executor

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
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

// substituteProxyCommand updates variables with values associated with the target host.
// SSH ProxyCommand can use placeholders such as %h, %p, and %r (host, port, username), they have to be replaced with the actual values.
func substituteProxyCommand(username, address string, proxyCommand []string) ([]string, error) {
	if len(proxyCommand) == 0 {
		return []string{}, nil
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("failed to split hostAddr and port: %w", err)
	}

	cmdArgs := make([]string, len(proxyCommand))

	for i, arg := range proxyCommand {
		arg = strings.ReplaceAll(arg, "%h", host)
		if port != "" {
			arg = strings.ReplaceAll(arg, "%p", port)
		}
		arg = strings.ReplaceAll(arg, "%r", username)
		cmdArgs[i] = arg
	}
	return cmdArgs, nil
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
	client, _, err := c.sshClient(ctx, hostAddr, user, nil)
	if err != nil {
		return nil, err
	}
	return &Remote{client: client, hostAddr: hostAddr, hostName: hostName, logs: c.logs.WithHost(hostAddr, hostName)}, nil
}

// ConnectWithProxy connects to a remote host through a proxy command and returns a remote executer, caller must close.
func (c *Connector) ConnectWithProxy(ctx context.Context, hostAddr, hostName, user string, proxyCommandParsed []string) (*Remote, error) {
	log.Printf("[DEBUG] connect to %q (%s), user %q, proxy command: %s", hostAddr, hostName, user, proxyCommandParsed)

	client, stopProxyCommand, err := c.sshClient(ctx, hostAddr, user, proxyCommandParsed)
	if err != nil {
		return nil, err
	}

	return &Remote{client: client, hostAddr: hostAddr, hostName: hostName, logs: c.logs.WithHost(hostAddr, hostName), stopProxyCommand: stopProxyCommand}, nil
}

func (c *Connector) forwardAgent(client *ssh.Client) error {
	if !c.enableAgentForwarding {
		return nil
	}

	aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return fmt.Errorf("unable to connect to ssh agent: %w", err)
	}

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

func (c *Connector) dial(ctx context.Context, host string, conf *ssh.ClientConfig) (*ssh.Client, error) {
	var client *ssh.Client
	var conn net.Conn
	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, host, conf)
	if err != nil {
		return nil, fmt.Errorf("failed to create client connection to %s: %v", host, err)
	}
	client = ssh.NewClient(ncc, chans, reqs)
	return client, nil
}

func (c *Connector) dialWithProxy(ctx context.Context, host string, cmdArgs []string, conf *ssh.ClientConfig) (*ssh.Client, context.CancelFunc, error) {
	var sshClient *ssh.Client
	pipeClient, pipeServer := net.Pipe()

	log.Printf("[DEBUG] create ssh session with, ProxyCommand: %s", cmdArgs)

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Stderr = os.Stderr

	// If stdin, stdout is not standard OS files, cmd.Wait() will wait till files will be closed which for observers
	// looks like hangup. To automate management of closing files lines below create "standard" stdin/out
	// and there is code that copy data between them and pipe.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cmd.StdoutPipe(): %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cmd.StdinPipe(): %w", err)
	}

	err = cmd.Start()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start proxy command: %w", err)
	}

	errChan := make(chan error, 3)

	copyCtx, cancelCopy := context.WithCancel(ctx)

	// sends data to stdin of proxy command
	go func() {
		defer stdin.Close()
		copied, err := io.Copy(stdin, pipeServer)

		log.Printf("[DEBUG] io.Copy(stdin, pipeServer) returned error: %v bytes copied %d", err, copied)

		if err != nil && copyCtx.Err() == nil {
			log.Printf("[DEBUG] sending error to channel")
			errChan <- fmt.Errorf("failed to copy to proxy stdin: %w", err)
		}
	}()

	// reads data from stdout of proxy command
	go func() {
		copied, err := io.Copy(pipeServer, stdout)

		log.Printf("[DEBUG] io.Copy(pipeServer, stdout) returned error: %v, bytes copied %d", err, copied)
		if err != nil && copyCtx.Err() == nil {
			log.Printf("[DEBUG] sending error to channel")
			errChan <- fmt.Errorf("failed to copy from proxy stdout: %w", err)
		}
	}()

	go func() {
		// There is a catch proxy command, for example `gcloud compute start-iap-tunnel`, can stop/exit with error but still return 0 as
		// return code. Because of that we can't rely on `if err != nil`, instead treating cmd.Wait() as
		// blocking request and if that requested ended - proxy command exited/completed/failed -
		// sending Done() signal to channel.
		err := cmd.Wait()

		log.Printf("[DEBUG] cmd.Wait() returned: %v", err)

		if err != nil && copyCtx.Err() == nil {
			errChan <- fmt.Errorf("proxy command execution failed: %w", err)
		}

		if err == nil {
			log.Printf("[DEBUG] proxy command exited with returned code %v:", err)
			cancelCopy()
		}
	}()

	// monitoring for proxy command errors
	go func() {
		log.Printf("[DEBUG] staring proxy command monitoring ")
		select {
		case proxyErr := <-errChan:
			log.Printf("[WARN] proxy error after SSH connection established: %v ; closing pipeServer", proxyErr)

			if sshClient != nil {
				sshClient.Close()
			}

			pipeClient.Close()
			pipeServer.Close()
		case <-copyCtx.Done():
			log.Printf("[DEBUG] recevied Done() signal, closing pipeServer ")

			pipeClient.Close()
			pipeServer.Close()
		}
	}()

	ncc, chans, reqs, err := ssh.NewClientConn(pipeClient, host, conf)

	if err != nil {
		cancelCopy()
		pipeClient.Close()
		pipeServer.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}

		return nil, nil, fmt.Errorf("failed to create SSH pipeClient connection: %w", err)
	}

	sshClient = ssh.NewClient(ncc, chans, reqs)

	return sshClient, cancelCopy, nil
}

func (c *Connector) sshClient(ctx context.Context, host, user string, proxyCommandParsed []string) (*ssh.Client, context.CancelFunc, error) {
	var client *ssh.Client
	var stopProxyCommand context.CancelFunc

	log.Printf("[DEBUG] create ssh session to %s, user %s", host, user)
	if !strings.Contains(host, ":") {
		host += ":22"
	}

	conf, err := c.sshConfig(user, c.privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ssh config: %w", err)
	}

	if len(proxyCommandParsed) == 0 {
		client, err = c.dial(ctx, host, conf)
		if err != nil {
			return nil, nil, err
		}
	} else {
		cmdArgs, err := substituteProxyCommand(user, host, proxyCommandParsed)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to substitute proxy command with target host values: %w", err)
		}

		client, stopProxyCommand, err = c.dialWithProxy(ctx, host, cmdArgs, conf)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create client connection wtth proxy command %s, to %s: %v", cmdArgs, host, err)
		}
	}

	if err := c.forwardAgent(client); err != nil {
		return nil, nil, fmt.Errorf("failed to forward agent to %s: %v", host, err)
	}

	log.Printf("[DEBUG] ssh session created to %s", host)
	return client, stopProxyCommand, nil
}

func (c *Connector) sshConfig(user, privateKeyPath string) (*ssh.ClientConfig, error) {

	// getAuth returns a list of ssh.AuthMethod to be used for authentication.
	// if ssh agent is enabled, it will be used, otherwise private key will be used.
	getAuth := func() (auth []ssh.AuthMethod, err error) {
		if privateKeyPath == "" || c.enableAgent {
			if aconn, e := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); e == nil {
				auth = append(auth, ssh.PublicKeysCallback(agent.NewClient(aconn).Signers))
				log.Printf("[DEBUG] ssh agent found at %s", os.Getenv("SSH_AUTH_SOCK"))
			} else {
				return nil, fmt.Errorf("unable to connect to ssh agent: %w", e)
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

func (c *Connector) String() string {
	return fmt.Sprintf("ssh connector with private key %s.., timeout %v, agent %v", c.privateKey[:8], c.timeout, c.enableAgent)
}
