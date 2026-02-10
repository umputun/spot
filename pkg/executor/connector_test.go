package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConnector_Connect(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	t.Run("good connection", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", "", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
		require.NoError(t, err)
		defer sess.Close()
	})

	t.Run("bad user", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", "", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, hostAndPort, "h1", "test33")
		require.ErrorContains(t, err, "ssh: unable to authenticate")
	})

	t.Run("bad key", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")
		_, err := NewConnector("testdata/test_ssh_key33", "", time.Second*10, MakeLogs(true, false, nil))
		require.ErrorContains(t, err, "private key file \"testdata/test_ssh_key33\" does not exist", "test")
	})

	t.Run("wrong port", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", "", time.Second*10, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, "127.0.0.1:12345", "h1", "test")
		require.ErrorContains(t, err, "failed to dial: dial tcp 127.0.0.1:12345")
	})

	t.Run("timeout", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", "", time.Nanosecond, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, hostAndPort, "h1", "test")
		require.ErrorContains(t, err, "i/o timeout")
	})

	t.Run("unreachable host", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key", "", time.Second, MakeLogs(true, false, nil))
		require.NoError(t, err)
		_, err = c.Connect(ctx, "10.255.255.1:22", "h1", "test")
		require.ErrorContains(t, err, "failed to dial: dial tcp 10.255.255.1:22: i/o timeout")
	})
}
