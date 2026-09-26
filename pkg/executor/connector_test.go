package executor

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestConnector_String(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantSubstr string
	}{
		{"agent-only empty key", "", "private key .., "},
		{"short key", "abc", "private key abc.., "},
		{"exactly eight", "12345678", "private key 12345678.., "},
		{"long key path", "/home/user/.ssh/id_rsa", "private key /home/us.., "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Connector{privateKey: tc.key, timeout: time.Second}
			var got string
			assert.NotPanics(t, func() { got = c.String() }, "String must not panic on short or empty keys")
			assert.Contains(t, got, tc.wantSubstr, "String must show at most the first 8 key chars")
		})
	}
}

func TestConnector_Connect(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	t.Run("good connection", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
		require.NoError(t, err)
		defer sess.Close()
	})

	t.Run("bad user", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, hostAndPort, "h1", "test33")
		require.ErrorContains(t, err, "ssh: unable to authenticate")
	})

	t.Run("bad key", func(t *testing.T) {
		_, err := NewConnector("testdata/test_ssh_key33", time.Second*10, MakeLogs(true, false, nil))
		require.ErrorContains(t, err, "private key file \"testdata/test_ssh_key33\" does not exist", "test")
	})

	t.Run("wrong port", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, "127.0.0.1:12345", "h1", "test")
		require.ErrorContains(t, err, "failed to dial: dial tcp 127.0.0.1:12345")
	})

	t.Run("timeout", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Nanosecond, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, hostAndPort, "h1", "test")
		require.ErrorContains(t, err, "i/o timeout")
	})

	t.Run("unreachable host", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, "10.255.255.1:22", "h1", "test")
		require.ErrorContains(t, err, "failed to dial: dial tcp 10.255.255.1:22")
	})
}

func TestConnector_WithAgent(t *testing.T) {
	inAgent, notInAgent := genEd25519(t), genEd25519(t)
	startTestAgent(t, genEd25519(t), inAgent)
	dir := t.TempDir()

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	t.Run("key in agent", func(t *testing.T) {
		logBuf.Reset()
		c := &Connector{privateKey: writeKey(t, dir, "in", inAgent, "", false)}
		c.WithAgent()
		require.NotNil(t, c.preferredKey)
		assert.Equal(t, pubOf(t, inAgent).Marshal(), c.preferredKey.Marshal())
		assert.NotContains(t, logBuf.String(), "[WARN]")
	})

	t.Run("key not in agent warns once and keeps agent order", func(t *testing.T) {
		logBuf.Reset()
		c := &Connector{privateKey: writeKey(t, dir, "out", notInAgent, "", false)}
		c.WithAgent()
		assert.Nil(t, c.preferredKey)
		assert.Equal(t, 1, bytes.Count(logBuf.Bytes(), []byte("is not in the ssh agent")))
	})

	t.Run("no key, agent only", func(t *testing.T) {
		c := &Connector{}
		c.WithAgent()
		assert.Nil(t, c.preferredKey)
		assert.True(t, c.enableAgent)
	})
}

func TestPublicKeyFor(t *testing.T) {
	dir := t.TempDir()
	key := genEd25519(t)
	want := pubOf(t, key).Marshal()

	t.Run("from .pub", func(t *testing.T) {
		path := writeKey(t, dir, "withpub", key, "", true)
		require.NoError(t, os.WriteFile(path, []byte("not a key the parser can read"), 0o600)) // like an sk key
		pub, err := publicKeyFor(path)
		require.NoError(t, err)
		assert.Equal(t, want, pub.Marshal())
	})

	t.Run("from unencrypted private key", func(t *testing.T) {
		pub, err := publicKeyFor(writeKey(t, dir, "plain", key, "", false))
		require.NoError(t, err)
		assert.Equal(t, want, pub.Marshal())
	})

	t.Run("from passphrase-protected private key", func(t *testing.T) {
		pub, err := publicKeyFor(writeKey(t, dir, "enc", key, "secret", false))
		require.NoError(t, err)
		assert.Equal(t, want, pub.Marshal())
	})

	t.Run("unparsable key without .pub", func(t *testing.T) {
		path := filepath.Join(dir, "sk_no_pub")
		require.NoError(t, os.WriteFile(path, []byte("not a key the parser can read"), 0o600))
		_, err := publicKeyFor(path)
		require.ErrorContains(t, err, "no "+path+".pub (needed for PEM-encrypted and sk keys)")
	})

	t.Run("bad .pub", func(t *testing.T) {
		path := filepath.Join(dir, "badpub")
		require.NoError(t, os.WriteFile(path+".pub", []byte("garbage"), 0o600))
		_, err := publicKeyFor(path)
		require.ErrorContains(t, err, "can't parse "+path+".pub")
	})
}

func TestPreferKey(t *testing.T) {
	signers := make([]ssh.Signer, 0, 4)
	for range 4 {
		s, err := ssh.NewSignerFromKey(genEd25519(t))
		require.NoError(t, err)
		signers = append(signers, s)
	}
	other, err := ssh.NewSignerFromKey(genEd25519(t))
	require.NoError(t, err)

	tests := []struct {
		name string
		key  ssh.PublicKey
		want []ssh.Signer
	}{
		{"nil key keeps order", nil, signers},
		{"first stays first", signers[0].PublicKey(), signers},
		{"middle moves to front", signers[2].PublicKey(), []ssh.Signer{signers[2], signers[0], signers[1], signers[3]}},
		{"last moves to front", signers[3].PublicKey(), []ssh.Signer{signers[3], signers[0], signers[1], signers[2]}},
		{"no match keeps order", other.PublicKey(), signers},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]ssh.Signer(nil), signers...)
			assert.Equal(t, tc.want, preferKey(in, tc.key))
			assert.Equal(t, signers, in, "input must not be modified")
		})
	}
}

// startTestAgent serves an in-memory agent holding keys, in the given order, and points SSH_AUTH_SOCK at it.
func startTestAgent(t *testing.T, keys ...ed25519.PrivateKey) {
	t.Helper()
	sock, err := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { sock.Close() })

	keyring := agent.NewKeyring()
	for _, k := range keys {
		require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: k}))
	}
	go func() {
		for {
			c, err := sock.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = agent.ServeAgent(keyring, c)
			}()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock.Addr().String())
}

func genEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

func pubOf(t *testing.T, key ed25519.PrivateKey) ssh.PublicKey {
	t.Helper()
	pub, err := ssh.NewPublicKey(key.Public())
	require.NoError(t, err)
	return pub
}

// writeKey writes key as an OpenSSH private key file, encrypted if passphrase is set, and
// optionally the matching .pub. Returns the private key path.
func writeKey(t *testing.T, dir, name string, key ed25519.PrivateKey, passphrase string, withPub bool) string {
	t.Helper()
	var block *pem.Block
	var err error
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(key, "")
	}
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	if withPub {
		require.NoError(t, os.WriteFile(path+".pub", ssh.MarshalAuthorizedKey(pubOf(t, key)), 0o600))
	}
	return path
}
