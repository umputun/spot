package executor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnector_Connect(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	t.Run("good connection", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key")
		require.NoError(t, err)
		sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
		require.NoError(t, err)
		defer sess.Close()
	})

	t.Run("bad user", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key")
		require.NoError(t, err)
		_, err = c.Connect(ctx, hostAndPort, "h1", "test33")
		require.ErrorContains(t, err, "ssh: unable to authenticate")
	})

	t.Run("bad key", func(t *testing.T) {
		_, err := NewConnector("testdata/test_ssh_key33")
		require.ErrorContains(t, err, "private key file \"testdata/test_ssh_key33\" does not exist", "test")
	})

	t.Run("wrong port", func(t *testing.T) {
		c, err := NewConnector("testdata/test_ssh_key")
		require.NoError(t, err)
		_, err = c.Connect(ctx, "127.0.0.1:12345", "h1", "test")
		require.ErrorContains(t, err, "failed to dial: dial tcp 127.0.0.1:12345")
	})
}
