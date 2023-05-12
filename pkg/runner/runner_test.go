package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
	"github.com/umputun/spot/pkg/secrets"
)

func TestProcess_Run(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)

	t.Run("full playbook", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
		}
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 8, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("simple playbook", func(t *testing.T) {
		conf, err := config.New("testdata/conf-simple.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
		}
		res, err := p.Run(ctx, "default", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 7, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("with runtime vars", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		// make target with name "the host" and host/port from testingHostAndPort
		adr := strings.Split(testingHostAndPort, ":")[0]
		port, err := strconv.Atoi(strings.Split(testingHostAndPort, ":")[1])
		require.NoError(t, err)
		tg := conf.Targets["default"]
		tg.Hosts = []config.Destination{{Host: adr, Port: port, Name: "the host"}}
		conf.Targets["default"] = tg

		require.NoError(t, err)
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"runtime variables"},
		}
		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))
		res, err := p.Run(ctx, "task1", "default")
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), `name:"the host", cmd:"runtime variables", user:"test", task:"task1"`)
		assert.Contains(t, outWriter.String(), `host:"localhost:`)
	})

	t.Run("copy multiple files", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"copy multiple files"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), `upload testdata/conf2.yml to /tmp/conf2.yml`)
		assert.Contains(t, outWriter.String(), `upload testdata/conf-local.yml to /tmp/conf3.yml`)
	})

	t.Run("set variables", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"copy configuration", "some command", "user variables"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 3, res.Commands)
		assert.Contains(t, outWriter.String(), `> var foo: 6`)
		assert.Contains(t, outWriter.String(), `> var bar: 9`)
		assert.Contains(t, outWriter.String(), `> var baz: qux`, "was not overwritten")
	})

	t.Run("with secrets", func(t *testing.T) {
		sp := secrets.NewMemoryProvider(map[string]string{"FOO": "FOO_SECRET", "BAR": "BAR_SECRET"})
		conf, err := config.New("testdata/conf.yml", nil, sp)
		require.NoError(t, err)
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", conf.AllSecretValues()),
			Only:        []string{"secrets"},
		}
		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Contains(t, outWriter.String(), `FOO=****`)
		assert.Contains(t, outWriter.String(), `BAR=****`)
		assert.Contains(t, outWriter.String(), `secrets: ********`)
		assert.NotContains(t, outWriter.String(), "FOO_SECRET")
		assert.NotContains(t, outWriter.String(), "BAR_SECRET")
	})

	t.Run("set variables for copy command", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"set filename for copy to env", "copy filename from env"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Commands)
		assert.Contains(t, outWriter.String(), ` > setvar filename=testdata/conf.yml`)
	})

	t.Run("env variables for copy command", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		cmd := conf.Tasks[0].Commands[18]
		assert.Equal(t, "copy filename from env", cmd.Name)
		cmd.Environment = map[string]string{"filename": "testdata/conf.yml"}
		conf.Tasks[0].Commands[18] = cmd

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"copy filename from env"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Contains(t, outWriter.String(), `uploaded testdata/conf.yml to localhost:/tmp/.spot/conf.yml`)
	})

}

func TestProcess_RunWithSudo(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	t.Run("single line script", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"root only single line"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), "passwd")
	})

	t.Run("multi line script", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"root only multiline"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), "passwd")
	})

	t.Run("copy single file with sudo", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"root only copy single file"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), "> sudo mv -f /tmp/.spot/conf.yml /srv/conf.yml")

		p.Only = []string{"root only stat /srv/conf.yml"}
		_, err = p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Contains(t, outWriter.String(), " File: /srv/conf.yml", "file was copied to /srv")
	})

	t.Run("copy glob files with sudo", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"root only copy multiple files"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), " > sudo mv -f /tmp/.spot/srv/* /srv", "files were copied to /srv")
		assert.Contains(t, outWriter.String(), " > rm -rf /tmp/.spot/srv", "tmp dir was removed")

		p.Only = []string{"root only ls /srv"}
		_, err = p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Contains(t, outWriter.String(), "conf-simple.yml", "file was copied to /srv")

		p.Only = []string{"root only stat /srv/conf.yml"}
		_, err = p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Contains(t, outWriter.String(), " File: /srv/conf.yml", "file was copied to /srv")
	})
}

func TestProcess_RunDry(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
		Dry:         true,
	}
	res, err := p.Run(ctx, "task1", testingHostAndPort)
	require.NoError(t, err)
	assert.Equal(t, 8, res.Commands)
	assert.Equal(t, 1, res.Hosts)
}

func TestProcess_RunOnlyAndSkip(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	t.Run("only, with auto", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"show content"},
		}
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("only, no auto", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Only:        []string{"show content", "no auto cmd"},
		}
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("skip", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
			Skip:        []string{"wait", "show content"},
		}
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 6, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})
}

func TestProcess_RunVerbose(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	log.SetOutput(io.Discard)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)
	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
		Verbose:     true,
		Skip:        []string{"wait"},
	}
	_, err = p.Run(ctx, "task1", testingHostAndPort)
	require.NoError(t, err)
}

func TestProcess_RunLocal(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	var buf bytes.Buffer
	log.SetOutput(&buf)

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf-local.yml", nil, nil)
	require.NoError(t, err)
	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
		Verbose:     true,
	}
	res, err := p.Run(ctx, "default", testingHostAndPort)
	require.NoError(t, err)
	t.Log(buf.String())
	assert.Equal(t, 2, res.Commands)
	assert.Contains(t, buf.String(), "run command \"show content\"")
}

func TestProcess_RunFailed(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
	}
	_, err = p.Run(ctx, "failed_task", testingHostAndPort)
	require.ErrorContains(t, err, `failed command "bad command" on host`)
}

func TestProcess_RunFailed_WithOnError(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
	}

	t.Run("onerror called", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "failed_task_with_onerror", testingHostAndPort)
		require.ErrorContains(t, err, `failed command "bad command" on host`)
		t.Log(buf.String())
		require.Contains(t, buf.String(), "onerror called")
	})

	t.Run("onerror failed", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		tsk := p.Config.Tasks[2]
		require.Equal(t, "failed_task_with_onerror", tsk.Name)
		tsk.OnError = "bad command"
		p.Config.Tasks[2] = tsk
		_, err = p.Run(ctx, "failed_task_with_onerror", testingHostAndPort)
		require.ErrorContains(t, err, `failed command "bad command" on host`)
		t.Log(buf.String())
		require.NotContains(t, buf.String(), "onerror called")
		assert.Contains(t, buf.String(), "[WARN]")
		assert.Contains(t, buf.String(), "not found")
	})
}

func TestProcess_RunFailedErrIgnored(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "failed_task", conf.Tasks[1].Name)
	conf.Tasks[1].Commands[1].Options.IgnoreErrors = true
	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
	}
	_, err = p.Run(ctx, "failed_task", testingHostAndPort)
	require.NoError(t, err, "error ignored")
}

func TestProcess_RunTaskWithWait(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Config:      conf,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)

	_, err = p.Run(ctx, "with_wait", testingHostAndPort)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "wait done")
}

func startTestContainer(t *testing.T) (hostAndPort string, teardown func()) {
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
			"PUBLIC_KEY":  string(pubKey),
			"USER_NAME":   "test",
			"TZ":          "Etc/UTC",
			"SUDO_ACCESS": "true",
		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)

	port, err := container.MappedPort(ctx, "2222")
	require.NoError(t, err)

	return fmt.Sprintf("%s:%s", host, port.Port()), func() { container.Terminate(ctx) }
}
