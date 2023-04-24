package runner

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/umputun/simplotask/app/config"
	"github.com/umputun/simplotask/app/remote"
)

func TestProcess_Run(t *testing.T) {
	ctx := context.Background()
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := remote.NewConnector("test", "testdata/test_ssh_key")
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
	}
	_, err = p.Run(ctx, "task1", hostAndPort)
	require.NoError(t, err)
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
	require.NoError(t, err)

	_, err = container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "2222")
	require.NoError(t, err)
	return fmt.Sprintf("localhost:%s", port.Port()), func() { container.Terminate(ctx) }
}
