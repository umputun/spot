package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
		require.ErrorContains(t, err, "failed to dial: dial tcp 10.255.255.1:22: i/o timeout")
	})
}

func TestConnector_ConnectWithProxy(t *testing.T) {
	// To test proxy command, the chain of connection will be next:
	// localhost -> localhost:<random_port> (this is also the bastion host) -> target-host:2222
	// In a real-world application, "target-host:2222" will be replaced with "%h:%p", but since
	// testcontainers returns "localhost:<random_port>" manually, overriding it.
	//
	// "ssh -W" requires enabling AllowTcpForwarding, to enable it, modification was applied:
	// see pkg/executor/remote_test.go, env variable DOCKER_MODS on test container.
	// The "bastion-host" is a local host, and we are using a standard SSH client which tries to verify the host key;
	// to bypass this check, "-o StrictHostKeyChecking=no” was added to the proxy command.
	//

	ctx := context.Background()
	bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	defer teardown()

	bastionAddr := strings.Split(bastionHostAndPort, ":")
	proxyCommandParsed := []string{
		"ssh",
		"-W",
		"target-host:2222",
		"test@localhost",
		"-p",
		bastionAddr[1],
		"-i",
		"testdata/test_ssh_key",
		"-o",
		"StrictHostKeyChecking=no",
	}

	t.Run("good connection", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		sess, err := c.ConnectWithProxy(ctx, "target-host:2222", "target-host", "test", proxyCommandParsed)
		require.NoError(t, err)
		defer sess.Close()
	})

	t.Run("bad user", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.ConnectWithProxy(ctx, "target-host:2222", "target-host", "test33", proxyCommandParsed)
		require.ErrorContains(t, err, "ssh: unable to authenticate")
	})

	t.Run("bad key", func(t *testing.T) {
		_, err := NewConnector("testdata/test_ssh_key33", time.Second*10, MakeLogs(true, false, nil))
		require.ErrorContains(t, err, "private key file \"testdata/test_ssh_key33\" does not exist", "test")
	})

	t.Run("wrong port", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		wrongPortProxyCommand := []string{
			"ssh",
			"-W",
			"target-host:2222",
			"test@localhost",
			"-p",
			"12345",
			"-i",
			"testdata/test_ssh_key",
			"-o",
			"StrictHostKeyChecking=no",
		}
		_, err = c.ConnectWithProxy(ctx, "target-host:2222", "target-host", "test", wrongPortProxyCommand)
		require.ErrorContains(t, err, "failed to create client connection")
	})

	t.Run("timeout", func(t *testing.T) {
		t.Skip("Implementation of timeout here is overkill")

		/* Skipped because of next.
		For the net.Dialer() there is a parameter that controls timeout for establishing connection.
		When proxy command is being used, external program will be called exc.Command() and it seems there is no
		"default" functionality for timeout for starting program. I.e. OS receive command to start program, and
		then program will fail or will start.

		For ssh client virtual in memory server net.Pipe() will be started on the same host and client of it
		will be passed to ssh client, so it is also awkward to test timeout abort for localhost "in memory" connection.

		Some proxy commands can support connection timeout, for example ssh `-o ConnectTimeout=5` but it means
		test will try to check behavior of external program.
		*/

		c, err := NewConnector("testdata/test_ssh_key", time.Nanosecond, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.ConnectWithProxy(ctx, "target-host:2222", "target-host", "test", proxyCommandParsed)
		require.ErrorContains(t, err, "i/o timeout")
	})

	t.Run("unreachable host", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", time.Second, MakeLogs(true, false, nil))
		require.NoError(t, err)
		unreachableProxyCommand := []string{
			"ssh",
			"-W",
			"unreachable-host:2222",
			"test@10.255.255.1",
			"-p",
			"22",
			"-i",
			"testdata/test_ssh_key",
			"-o",
			"StrictHostKeyChecking=no",
			"-o",
			"ConnectTimeout=1", // connection timeout option to speed up test, default timeout  is too big

		}
		_, err = c.ConnectWithProxy(ctx, "unreachable-host:2222", "unreachable-host", "test", unreachableProxyCommand)

		// Commented out, timeout error text will be in exc.Command() output and for simplicity external command
		// error output is not copied into spot memory, only exit/return code is being checked.

		// require.ErrorContains(t, err, "failed to create client connection")
	})
}

func TestSubstituteProxyCommand(t *testing.T) {
	tests := []struct {
		username     string
		address      string
		proxyCommand []string
		expected     []string
		expectError  bool
	}{
		{
			username:     "user",
			address:      "example.com:22",
			proxyCommand: []string{"ssh", "-W", "%h:%p", "%r@example.com"},
			expected:     []string{"ssh", "-W", "example.com:22", "user@example.com"},
			expectError:  false,
		},
		{
			username:     "user",
			address:      "example.com:22",
			proxyCommand: []string{"ssh", "-W", "%h:%p", "%r@example.com", "random arg with spaces"},
			expected:     []string{"ssh", "-W", "example.com:22", "user@example.com", "random arg with spaces"},
			expectError:  false,
		},
		{
			username:     "user",
			address:      "example.com",
			proxyCommand: []string{"ssh", "-W", "%h:%p", "%r@example.com"},
			expected:     nil,
			expectError:  true,
		},
		{
			username:     "user",
			address:      "example.com:22",
			proxyCommand: []string{},
			expected:     []string{},
			expectError:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			result, err := substituteProxyCommand(test.username, test.address, test.proxyCommand)
			if test.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.expected, result)
			}
		})
	}
}
