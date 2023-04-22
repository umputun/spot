package remote

import (
	"context"
	"fmt"
	"os"
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

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)

	err = svc.Connect(ctx, hostAndPort)
	require.NoError(t, err)
	defer svc.Close()

	err = svc.Upload(ctx, "testdata/data.txt", "/tmp/blah/data.txt", true)
	require.NoError(t, err)

	tmpFile, err := fileutils.TempFileName("", "data.txt")
	require.NoError(t, err)
	defer os.RemoveAll(tmpFile)
	err = svc.Download(ctx, "/tmp/blah/data.txt", tmpFile, true)
	require.NoError(t, err)
	assert.FileExists(t, tmpFile)
	exp, err := os.ReadFile("testdata/data.txt")
	require.NoError(t, err)
	act, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, string(exp), string(act))
}

func TestExecuter_Upload_FailedNoRemoteDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)

	err = svc.Connect(ctx, hostAndPort)
	require.NoError(t, err)
	defer svc.Close()

	err = svc.Upload(ctx, "testdata/data.txt", "/tmp/blah/data.txt", false)
	require.EqualError(t, err, "failed to copy file: scp: /tmp/blah/data.txt: No such file or directory\n")
}

func TestExecuter_Upload_CantMakeRemoteDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)

	err = svc.Connect(ctx, hostAndPort)
	require.NoError(t, err)
	defer svc.Close()

	err = svc.Upload(ctx, "testdata/data.txt", "/dev/blah/data.txt", true)
	require.EqualError(t, err, "failed to create remote directory: failed to run command on remote server: Process exited with status 1")
}

func TestExecuter_Upload_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)

	err = svc.Connect(ctx, hostAndPort)
	require.NoError(t, err)
	defer svc.Close()

	cancel()
	err = svc.Upload(ctx, "testdata/data.txt", "/tmp/blah/data.txt", true)
	require.EqualError(t, err, "failed to create remote directory: canceled: context canceled")
}

func TestExecuter_UploadCanceledWithoutMkdir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)

	err = svc.Connect(ctx, hostAndPort)
	require.NoError(t, err)
	defer svc.Close()

	cancel()
	err = svc.Upload(ctx, "testdata/data.txt", "/tmp/blah/data.txt", false)
	require.EqualError(t, err, "failed to copy file: context canceled")
}

func TestExecuter_ConnectCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)

	err = svc.Connect(ctx, hostAndPort)
	require.EqualError(t, err, "failed to dial: dial tcp: lookup localhost: i/o timeout")
}

func TestExecuter_Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)
	require.NoError(t, svc.Connect(ctx, hostAndPort))

	t.Run("single line out", func(t *testing.T) {
		out, e := svc.Run(ctx, "sh -c 'echo hello world'")
		require.NoError(t, e)
		assert.Equal(t, []string{"hello world"}, out)
	})

	t.Run("multi line out", func(t *testing.T) {
		err = svc.Upload(ctx, "testdata/data.txt", "/tmp/st/data1.txt", true)
		assert.NoError(t, err)
		err = svc.Upload(ctx, "testdata/data.txt", "/tmp/st/data2.txt", true)
		assert.NoError(t, err)

		out, err := svc.Run(ctx, "ls -1 /tmp/st")
		require.NoError(t, err)
		t.Logf("out: %v", out)
		assert.Equal(t, 2, len(out))
		assert.Equal(t, "data1.txt", out[0])
		assert.Equal(t, "data2.txt", out[1])
	})

	t.Run("find out", func(t *testing.T) {
		cmd := fmt.Sprintf("find %s -type f -exec stat -c '%%n:%%s:%%Y' {} \\;", "/tmp/")
		out, e := svc.Run(ctx, cmd)
		require.NoError(t, e)
		assert.Equal(t, []string{"/tmp/st/data1.txt:68:1682028151", "/tmp/st/data2.txt:68:1682028151"}, out)
	})

}

func TestExecuter_Sync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	svc, err := NewExecuter("test", "testdata/test_ssh_key")
	require.NoError(t, err)
	require.NoError(t, svc.Connect(ctx, hostAndPort))

	t.Run("sync", func(t *testing.T) {
		err = svc.Sync(ctx, "testdata/sync", "/tmp/sync.dest")
		require.NoError(t, err)
		out, e := svc.Run(ctx, "find /tmp/sync.dest -type f -exec stat -c '%s %n' {} \\;")
		require.NoError(t, e)
		assert.Equal(t, []string{"185 /tmp/sync.dest/file1.txt", "61 /tmp/sync.dest/file2.txt", "17 /tmp/sync.dest/d1/file11.txt"}, out)
	})
}

func startTestContainer(t *testing.T) (hostAndPort string, teardown func()) {
	t.Helper()
	ctx := context.Background()
	pubKey, err := os.ReadFile("testdata/test_ssh_key.pub")
	require.NoError(t, err)

	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		WaitingFor:   wait.NewLogStrategy("done.").WithStartupTimeout(time.Second * 30),
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

	_, err = container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "2222")
	require.NoError(t, err)
	return fmt.Sprintf("localhost:%s", port.Port()), func() { container.Terminate(ctx) }
}
