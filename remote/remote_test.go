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

	// start a docker container running an SSH server
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
		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer container.Terminate(ctx)

	_, err = container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "2222")
	require.NoError(t, err)
	hostAndPort := fmt.Sprintf("localhost:%s", port.Port())

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
