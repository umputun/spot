package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/go-pkgz/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestExecuter_UploadAndDownload(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)

	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	err = sess.Upload(ctx, "testdata/data1.txt", "/tmp/blah/data1.txt", true)
	require.NoError(t, err)

	tmpFile, err := fileutils.TempFileName("", "data1.txt")
	require.NoError(t, err)
	defer os.RemoveAll(tmpFile)
	err = sess.Download(ctx, "/tmp/blah/data1.txt", tmpFile, true)
	require.NoError(t, err)
	assert.FileExists(t, tmpFile)
	exp, err := os.ReadFile("testdata/data1.txt")
	require.NoError(t, err)
	act, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, string(exp), string(act))
}

func TestExecuter_UploadGlobAndDownload(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)

	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	err = sess.Upload(ctx, "testdata/data*.txt", "/tmp/blah", true)
	require.NoError(t, err)

	{
		tmpFile, err := fileutils.TempFileName("", "data1.txt")
		require.NoError(t, err)
		defer os.RemoveAll(tmpFile)
		err = sess.Download(ctx, "/tmp/blah/data1.txt", tmpFile, true)
		require.NoError(t, err)
		assert.FileExists(t, tmpFile)
		exp, err := os.ReadFile("testdata/data1.txt")
		require.NoError(t, err)
		act, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Equal(t, string(exp), string(act))
	}
	{
		tmpFile, err := fileutils.TempFileName("", "data2.txt")
		require.NoError(t, err)
		defer os.RemoveAll(tmpFile)
		err = sess.Download(ctx, "/tmp/blah/data2.txt", tmpFile, true)
		require.NoError(t, err)
		assert.FileExists(t, tmpFile)
		exp, err := os.ReadFile("testdata/data2.txt")
		require.NoError(t, err)
		act, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Equal(t, string(exp), string(act))
	}
}

func TestExecuter_Upload_FailedSourceNotFound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	err = sess.Upload(ctx, "testdata/data-not-found.txt", "/tmp/blah/data.txt", true)
	require.EqualError(t, err, "source file \"testdata/data-not-found.txt\" not found")
}

func TestExecuter_Upload_FailedNoRemoteDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	err = sess.Upload(ctx, "testdata/data1.txt", "/tmp/blah/data1.txt", false)
	require.EqualError(t, err, "failed to create remote file: file does not exist")
}

func TestExecuter_Upload_CantMakeRemoteDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	err = sess.Upload(ctx, "testdata/data1.txt", "/dev/blah/data1.txt", true)
	require.EqualError(t, err, "failed to create remote directory: permission denied")
}

func TestExecuter_Upload_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	cancel()
	err = sess.Upload(ctx, "testdata/data1.txt", "/tmp/blah/data1.txt", true)
	require.EqualError(t, err, "failed to copy file: context canceled")
}

func TestExecuter_UploadCanceledWithoutMkdir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	cancel()

	err = sess.Upload(ctx, "testdata/data1.txt", "/tmp/data1.txt", false)
	require.EqualError(t, err, "failed to copy file: context canceled")
}

func TestExecuter_ConnectCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	_, err = c.Connect(ctx, hostAndPort, "h1", "test")
	assert.ErrorContains(t, err, "failed to dial: dial tcp: lookup localhost: i/o timeout")
}

func TestExecuter_Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	t.Run("single line out", func(t *testing.T) {
		out, e := sess.Run(ctx, "sh -c 'echo hello world'", false)
		require.NoError(t, e)
		assert.Equal(t, []string{"hello world"}, out)
	})

	t.Run("multi line out", func(t *testing.T) {
		err = sess.Upload(ctx, "testdata/data1.txt", "/tmp/st/data1.txt", true)
		assert.NoError(t, err)
		err = sess.Upload(ctx, "testdata/data2.txt", "/tmp/st/data2.txt", true)
		assert.NoError(t, err)

		out, err := sess.Run(ctx, "ls -1 /tmp/st", false)
		require.NoError(t, err)
		t.Logf("out: %v", out)
		assert.Equal(t, 2, len(out))
		assert.Equal(t, "data1.txt", out[0])
		assert.Equal(t, "data2.txt", out[1])
	})

	t.Run("find out", func(t *testing.T) {
		cmd := fmt.Sprintf("find %s -type f -exec stat -c '%%n:%%s' {} \\;", "/tmp/")
		out, e := sess.Run(ctx, cmd, true)
		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"/tmp/st/data1.txt:13", "/tmp/st/data2.txt:13"}, out)
	})

	t.Run("with secrets", func(t *testing.T) {
		originalStdout := os.Stdout
		reader, writer, _ := os.Pipe()
		os.Stdout = writer

		sess.SetSecrets([]string{"data2"})
		defer sess.SetSecrets(nil)
		cmd := fmt.Sprintf("find %s -type f -exec stat -c '%%n:%%s' {} \\;", "/tmp/")
		out, e := sess.Run(ctx, cmd, true)
		writer.Close()
		os.Stdout = originalStdout

		capturedStdout, err := io.ReadAll(reader)
		require.NoError(t, err)

		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"/tmp/st/data1.txt:13", "/tmp/st/data2.txt:13"}, out)
		t.Logf("capturedStdout: %s", capturedStdout)
		assert.NotContains(t, string(capturedStdout), "data2", "captured stdout should not contain secrets")
		assert.Contains(t, string(capturedStdout), "****", "captured stdout should contain masked secrets")
	})

	t.Run("command failed", func(t *testing.T) {
		_, err := sess.Run(ctx, "sh -c 'exit 1'", false)
		assert.ErrorContains(t, err, "failed to run command on remote server")
	})

	t.Run("ctx canceled", func(t *testing.T) {
		ctxc, cancel := context.WithCancel(ctx)
		cancel()
		_, err := sess.Run(ctxc, "sh -c 'echo hello world'", false)
		assert.ErrorContains(t, err, "context canceled")
	})

}

func TestExecuter_Sync(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	t.Run("sync", func(t *testing.T) {
		res, e := sess.Sync(ctx, "testdata/sync", "/tmp/sync.dest", true)
		require.NoError(t, e)
		sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
		assert.Equal(t, []string{"d1/file11.txt", "file1.txt", "file2.txt"}, res)
		out, e := sess.Run(ctx, "find /tmp/sync.dest -type f -exec stat -c '%s %n' {} \\;", true)
		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"17 /tmp/sync.dest/d1/file11.txt", "185 /tmp/sync.dest/file1.txt", "61 /tmp/sync.dest/file2.txt"}, out)
	})

	t.Run("sync_again", func(t *testing.T) {
		res, e := sess.Sync(ctx, "testdata/sync", "/tmp/sync.dest", true)
		require.NoError(t, e)
		assert.Equal(t, 0, len(res), "no files should be synced", res)
	})

	t.Run("sync no src", func(t *testing.T) {
		_, err = sess.Sync(ctx, "/tmp/no-such-place", "/tmp/sync.dest", true)
		require.EqualError(t, err, "failed to get local files properties for /tmp/no-such-place: failed to walk local directory"+
			" /tmp/no-such-place: lstat /tmp/no-such-place: no such file or directory")
	})

	t.Run("sync with empty dir on remote to delete", func(t *testing.T) {
		_, e := sess.Run(ctx, "mkdir -p /tmp/sync.dest2/empty", true)
		require.NoError(t, e)
		res, e := sess.Sync(ctx, "testdata/sync", "/tmp/sync.dest2", true)
		require.NoError(t, e)
		sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
		assert.Equal(t, []string{"d1/file11.txt", "file1.txt", "file2.txt"}, res)
		out, e := sess.Run(ctx, "find /tmp/sync.dest2 -type f -exec stat -c '%s %n' {} \\;", true)
		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"17 /tmp/sync.dest2/d1/file11.txt", "185 /tmp/sync.dest2/file1.txt", "61 /tmp/sync.dest2/file2.txt"}, out)
	})

	t.Run("sync with non-empty dir on remote to delete", func(t *testing.T) {
		_, e := sess.Run(ctx, "mkdir -p /tmp/sync.dest3/empty", true)
		require.NoError(t, e)
		_, e = sess.Run(ctx, "touch /tmp/sync.dest3/empty/afile1.txt", true)
		require.NoError(t, e)
		res, e := sess.Sync(ctx, "testdata/sync", "/tmp/sync.dest3", true)
		require.NoError(t, e)
		sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
		assert.Equal(t, []string{"d1/file11.txt", "file1.txt", "file2.txt"}, res)
		out, e := sess.Run(ctx, "find /tmp/sync.dest3 -type f -exec stat -c '%s %n' {} \\;", true)
		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"17 /tmp/sync.dest3/d1/file11.txt", "185 /tmp/sync.dest3/file1.txt", "61 /tmp/sync.dest3/file2.txt"}, out)
	})

	t.Run("sync  with non-empty dir on remote to keep", func(t *testing.T) {
		_, e := sess.Run(ctx, "mkdir -p /tmp/sync.dest4/empty", true)
		require.NoError(t, e)
		_, e = sess.Run(ctx, "touch /tmp/sync.dest4/empty/afile1.txt", true)
		require.NoError(t, e)
		res, e := sess.Sync(ctx, "testdata/sync", "/tmp/sync.dest4", false)
		require.NoError(t, e)
		sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
		assert.Equal(t, []string{"d1/file11.txt", "file1.txt", "file2.txt"}, res)
		out, e := sess.Run(ctx, "find /tmp/sync.dest4 -type f -exec stat -c '%s %n' {} \\;", true)
		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"0 /tmp/sync.dest4/empty/afile1.txt", "17 /tmp/sync.dest4/d1/file11.txt",
			"185 /tmp/sync.dest4/file1.txt", "61 /tmp/sync.dest4/file2.txt"}, out)
	})
}

func TestExecuter_Delete(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	c, err := NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := c.Connect(ctx, hostAndPort, "h1", "test")
	require.NoError(t, err)
	defer sess.Close()

	res, err := sess.Sync(ctx, "testdata/sync", "/tmp/sync.dest", true)
	require.NoError(t, err)
	sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
	assert.Equal(t, []string{"d1/file11.txt", "file1.txt", "file2.txt"}, res)

	t.Run("delete file", func(t *testing.T) {
		err = sess.Delete(ctx, "/tmp/sync.dest/file1.txt", false)
		assert.NoError(t, err)
		out, e := sess.Run(ctx, "ls -1 /tmp/sync.dest", false)
		require.NoError(t, e)
		assert.Equal(t, []string{"d1", "file2.txt"}, out)
	})

	t.Run("delete dir non-recursive", func(t *testing.T) {
		err = sess.Delete(ctx, "/tmp/sync.dest", false)
		require.Error(t, err)
	})

	t.Run("delete dir", func(t *testing.T) {
		err = sess.Delete(ctx, "/tmp/sync.dest", true)
		assert.NoError(t, err)
		out, e := sess.Run(ctx, "ls -1 /tmp/", true)
		require.NoError(t, e)
		assert.NotContains(t, out, "file2.txt", out)
	})

	t.Run("delete empty dir", func(t *testing.T) {
		_, err = sess.Run(ctx, "mkdir -p /tmp/sync.dest/empty", true)
		require.NoError(t, err)
		out, e := sess.Run(ctx, "ls -1 /tmp/sync.dest", true)
		require.NoError(t, e)
		assert.Contains(t, out, "empty", out)
		err = sess.Delete(ctx, "/tmp/sync.dest/empty", false)
		assert.NoError(t, err)
		out, e = sess.Run(ctx, "ls -1 /tmp/sync.dest", true)
		require.NoError(t, e)
		assert.NotContains(t, out, "empty", out)
	})

	t.Run("delete no-such-file", func(t *testing.T) {
		err = sess.Delete(ctx, "/tmp/sync.dest/no-such-file", false)
		assert.NoError(t, err)
	})

	t.Run("delete no-such-dir", func(t *testing.T) {
		err = sess.Delete(ctx, "/tmp/sync.dest/no-such-dir", true)
		assert.NoError(t, err)
	})
}

func TestRemote_CloseNoSession(t *testing.T) {
	sess := &Remote{}
	err := sess.Close()
	assert.NoError(t, err)
}

func TestExecuter_findUnmatchedFiles(t *testing.T) {
	tbl := []struct {
		name    string
		local   map[string]fileProperties
		remote  map[string]fileProperties
		updated []string
		deleted []string
	}{
		{
			name: "all files match",
			local: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(0, 0)},
			},
			remote: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(0, 0)},
			},
			updated: []string{},
			deleted: []string{},
		},
		{
			name: "some files match",
			local: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(0, 0)},
			},
			remote: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(1, 1)},
			},
			updated: []string{"file2"},
			deleted: []string{},
		},
		{
			name: "no files match",
			local: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(0, 0)},
			},
			remote: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(1, 1)},
				"file2": {Size: 200, Time: time.Unix(1, 1)},
			},
			updated: []string{"file1", "file2"},
			deleted: []string{},
		},
		{
			name: "some local files deleted",
			local: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(0, 0)},
			},
			remote: map[string]fileProperties{
				"file1": {Size: 100, Time: time.Unix(0, 0)},
				"file2": {Size: 200, Time: time.Unix(0, 0)},
				"file3": {Size: 300, Time: time.Unix(0, 0)},
			},
			updated: []string{},
			deleted: []string{"file3"},
		},
	}

	for _, tc := range tbl {
		t.Run(tc.name, func(t *testing.T) {
			ex := &Remote{}
			updated, deleted := ex.findUnmatchedFiles(tc.local, tc.remote)
			assert.Equal(t, tc.updated, updated)
			assert.Equal(t, tc.deleted, deleted)
		})
	}
}

func startTestContainer(t *testing.T) (hostAndPort string, teardown func()) {
	t.Helper()
	ctx := context.Background()
	pubKey, err := os.ReadFile("testdata/test_ssh_key.pub")
	require.NoError(t, err)

	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		WaitingFor:   wait.NewLogStrategy("done.").WithStartupTimeout(time.Second * 60),
		Files: []testcontainers.ContainerFile{
			{HostFilePath: "testdata/test_ssh_key.pub", ContainerFilePath: "/authorized_key"},
		},
		Env: map[string]string{
			"PUBLIC_KEY": string(pubKey),
			"USER_NAME":  "test",
			"TZ":         "Etc/UTC",
		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "2222")
	require.NoError(t, err)
	return fmt.Sprintf("%s:%s", host, port.Port()), func() { container.Terminate(ctx) }
}
