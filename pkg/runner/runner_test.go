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
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
		}
		res, err := p.Run(ctx, "default", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 7, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("with runtime vars", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		// make target with name "the host" and host/poty from testingHostAndPort
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
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
}

func TestProcess_RunWithSudo(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	t.Run("single line", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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

	t.Run("multi line", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Config:      conf,
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
			ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
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
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)

	_, err = p.Run(ctx, "with_wait", testingHostAndPort)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "wait done")
}

func TestProcess_wait(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	ctx := context.Background()
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, err)

	t.Run("wait done", func(t *testing.T) {
		p := Process{Connector: connector}
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", false)
		})
		err = p.wait(ctx, sess, config.WaitInternal{Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100,
			Command: "cat /tmp/wait.done"})
		require.NoError(t, err)
	})

	t.Run("wait failed", func(t *testing.T) {
		p := Process{Connector: connector}
		err = p.wait(ctx, sess, config.WaitInternal{Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100,
			Command: "cat /tmp/wait.never-done"})
		require.EqualError(t, err, "timeout exceeded")
	})
}

func TestProcess_applyTemplates(t *testing.T) {
	tests := []struct {
		name     string
		inp      string
		user     string
		tdata    templateData
		expected string
	}{
		{
			name: "all_variables",
			inp:  "${SPOT_REMOTE_HOST}:${SPOT_REMOTE_USER}:${SPOT_COMMAND}:{SPOT_REMOTE_NAME}",
			tdata: templateData{
				hostAddr: "example.com",
				hostName: "example",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "example.com:user:ls:example",
		},
		{
			name: "no_variables",
			inp:  "no_variables_here",
			tdata: templateData{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1"},
			},
			expected: "no_variables_here",
		},
		{
			name: "single_dollar_variable",
			inp:  "$SPOT_REMOTE_HOST:$SPOT_REMOTE_USER:$SPOT_COMMAND:$SPOT_REMOTE_NAME",
			tdata: templateData{
				hostAddr: "example.com",
				hostName: "example",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "example.com:user:ls:example",
		},
		{
			name: "mixed_variables",
			inp:  "{SPOT_REMOTE_HOST}:$SPOT_REMOTE_USER:${SPOT_COMMAND}:{SPOT_TASK}",
			tdata: templateData{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user2"},
			},
			expected: "example.com:user2:ls:task1",
		},
		{
			name: "escaped_variables",
			inp:  "\\${SPOT_REMOTE_HOST}:\\$SPOT_REMOTE_USER:\\${SPOT_COMMAND}",
			tdata: templateData{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "\\example.com:\\user:\\ls",
		},
		{
			name: "variables with normal text",
			inp:  "${SPOT_REMOTE_HOST} blah ${SPOT_TASK} ${SPOT_REMOTE_USER}:${SPOT_COMMAND}",
			tdata: templateData{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user2"},
			},
			expected: "example.com blah task1 user2:ls",
		},
		{
			name: "with error msg",
			inp:  "$SPOT_REMOTE_HOST:$SPOT_REMOTE_USER:$SPOT_COMMAND ${SPOT_ERROR}",
			tdata: templateData{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
				err:      fmt.Errorf("some error"),
			},
			expected: "example.com:user:ls some error",
		},
		{
			name: "with error msg but no error",
			inp:  "$SPOT_REMOTE_HOST:$SPOT_REMOTE_USER:$SPOT_COMMAND ${SPOT_ERROR}",
			tdata: templateData{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
				err:      nil,
			},
			expected: "example.com:user:ls ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Process{}
			actual := p.applyTemplates(tt.inp, tt.tdata)
			require.Equal(t, tt.expected, actual)
		})
	}
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
