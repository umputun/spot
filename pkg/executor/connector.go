package executor

import (
	"bytes"
	"context"
	"errors"
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
	preferredKey          ssh.PublicKey // agent key offered first, resolved once from privateKey in WithAgent
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

// WithAgent enables ssh agent for authentication. If the connector has a private key, the agent key
// matching it is offered first, so a host accepting that key is not pushed past MaxAuthTries by the
// other agent keys. The rest of the agent keys are still offered, in agent order.
func (c *Connector) WithAgent() *Connector {
	log.Printf("[DEBUG] use ssh agent")
	c.enableAgent = true
	c.preferredKey = c.resolvePreferredKey()
	return c
}

// resolvePreferredKey returns the public key of the connector's private key if the agent holds it.
// it runs once, at setup, so the warnings are not repeated for every host and task.
func (c *Connector) resolvePreferredKey() ssh.PublicKey {
	if c.privateKey == "" {
		return nil
	}
	pub, err := c.publicKey()
	if err != nil {
		log.Printf("[WARN] can't get public key for %q: %v, agent keys will be offered in agent order", c.privateKey, err)
		return nil
	}
	aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return pub // no agent to check against, connecting will report it
	}
	defer aconn.Close()
	if c.timeout > 0 { // bound a stalled agent, zero means no deadline as in sshClient
		_ = aconn.SetDeadline(time.Now().Add(c.timeout))
	}
	keys, err := agent.NewClient(aconn).List()
	if err != nil {
		log.Printf("[WARN] can't list ssh agent keys: %v", err)
		return pub
	}
	for _, k := range keys {
		if bytes.Equal(k.Marshal(), pub.Marshal()) {
			log.Printf("[DEBUG] agent key %s matches %q, offered first", ssh.FingerprintSHA256(pub), c.privateKey)
			return pub
		}
	}
	log.Printf("[WARN] key %q is not in the ssh agent, agent keys will be offered in agent order", c.privateKey)
	return nil
}

// publicKey returns the public key for the connector's private key, from <key>.pub if present, otherwise
// from the private key itself. for a passphrase-protected OpenSSH key the public key is readable without
// the passphrase, PEM-encrypted and hardware (sk) keys need the .pub file.
func (c *Connector) publicKey() (ssh.PublicKey, error) {
	keyPath := c.privateKey
	if data, err := os.ReadFile(keyPath + ".pub"); err == nil { // nolint
		pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			return nil, fmt.Errorf("can't parse %s.pub: %w", keyPath, err)
		}
		return pub, nil
	}
	data, err := os.ReadFile(keyPath) // nolint
	if err != nil {
		return nil, fmt.Errorf("can't read private key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer.PublicKey(), nil
	}
	var pmErr *ssh.PassphraseMissingError
	if errors.As(err, &pmErr) && pmErr.PublicKey != nil {
		return pmErr.PublicKey, nil
	}
	return nil, fmt.Errorf("can't parse private key and no %s.pub (needed for PEM-encrypted and sk keys): %w", keyPath, err)
}

// preferKey returns signers with the one matching the preferred key moved to the front, others keep
// their order. signers is not modified.
func (c *Connector) preferKey(signers []ssh.Signer) []ssh.Signer {
	if c.preferredKey == nil {
		return signers
	}
	want := c.preferredKey.Marshal()
	for i, s := range signers {
		if !bytes.Equal(s.PublicKey().Marshal(), want) {
			continue
		}
		if i == 0 {
			return signers
		}
		res := make([]ssh.Signer, 0, len(signers))
		res = append(res, s)
		res = append(res, signers[:i]...)
		return append(res, signers[i+1:]...)
	}
	return signers
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
			agentSigners := agent.NewClient(aconn).Signers
			auth = append(auth, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
				signers, err := agentSigners()
				if err != nil {
					return nil, err
				}
				return c.preferKey(signers), nil // reorder only, signing stays in the agent
			}))
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
	// cap the slice so it does not run past the end for short or empty (agent-only) keys
	key := c.privateKey[:min(len(c.privateKey), 8)]
	return fmt.Sprintf("ssh connector with private key %s.., timeout %v, agent %v", key, c.timeout, c.enableAgent)
}
