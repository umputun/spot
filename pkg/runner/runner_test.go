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
	"github.com/umputun/spot/pkg/runner/mocks"
	"github.com/umputun/spot/pkg/secrets"
)

func TestProcess_Run(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)

	t.Run("full playbook", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
		}
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 8, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Equal(t, map[string]string{"bar": "9", "bar2": "10", "baz": "zzzzz", "foo": "6", "foo2": "7"}, res.Vars)
	})

	t.Run("simple playbook", func(t *testing.T) {
		conf, err := config.New("testdata/conf-simple.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
		}
		res, err := p.Run(ctx, "default", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 7, res.Commands)
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("simple playbook with only_on skip", func(t *testing.T) {
		conf, err := config.New("testdata/conf-simple.yml", nil, nil)
		require.NoError(t, err)
		conf.Tasks[0].Commands[0].Options.OnlyOn = []string{"not-existing-host"}
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
		}
		res, err := p.Run(ctx, "default", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 6, res.Commands, "should skip one command")
		assert.Equal(t, 1, res.Hosts)
	})

	t.Run("simple playbook with only_on include", func(t *testing.T) {
		conf, err := config.New("testdata/conf-simple.yml", nil, nil)
		require.NoError(t, err)
		conf.Tasks[0].Commands[0].Options.OnlyOn = []string{testingHostAndPort}
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
		}
		res, err := p.Run(ctx, "default", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 7, res.Commands, "should include the only_on command")
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
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"runtime variables"},
		}
		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))
		res, err := p.Run(ctx, "task1", "default")
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), `name:"the host", cmd:"runtime variables", user:"test", task:"task1"`)
		assert.Contains(t, outWriter.String(), fmt.Sprintf(`host:"%s:`, adr))
	})

	t.Run("copy multiple files", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
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
			Playbook:    conf,
			Logs:        logs,
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
		assert.Equal(t, map[string]string{"bar": "9", "bar2": "10", "baz": "zzzzz", "foo": "6", "foo2": "7"}, res.Vars)
		assert.Equal(t, map[string]string{"bar2": "10", "foo2": "7"}, res.Registered)
	})

	t.Run("with secrets", func(t *testing.T) {
		sp := secrets.NewMemoryProvider(map[string]string{"FOO": "FOO_SECRET", "BAR": "BAR_SECRET"})
		conf, err := config.New("testdata/conf.yml", nil, sp)
		require.NoError(t, err)

		lgs := executor.MakeLogs(false, false, conf.AllSecretValues())
		conn, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, lgs)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   conn,
			Playbook:    conf,
			Logs:        lgs,
			Only:        []string{"secrets"},
		}
		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		t.Log("out: ", outWriter.String())
		assert.Equal(t, 1, res.Commands)
		assert.Contains(t, outWriter.String(), `FOO=****`)
		assert.Contains(t, outWriter.String(), `BAR=****`)
		assert.Contains(t, outWriter.String(), `secrets: ****,****`)
		assert.NotContains(t, outWriter.String(), "FOO_SECRET")
		assert.NotContains(t, outWriter.String(), "BAR_SECRET")
	})

	t.Run("set variables for copy command", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"set filename for copy to env", "copy filename from env"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Commands)
		assert.Contains(t, outWriter.String(), ` > setvar filename=testdata/conf.yml`)
		assert.Equal(t, map[string]string{"filename": "testdata/conf.yml"}, res.Vars)
	})

	t.Run("env variables for copy command", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		cmd := conf.Tasks[0].Commands[19]
		assert.Equal(t, "copy filename from env", cmd.Name)
		cmd.Environment = map[string]string{"filename": "testdata/conf.yml"}
		conf.Tasks[0].Commands[19] = cmd

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"copy filename from env"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		adr := strings.Split(testingHostAndPort, ":")[0]
		// msg like "uploaded testdata/conf.yml to localhost:/tmp/.spot-1101281563531463808/conf.yml in"
		// adr is localhost or container ip if DOCKER_HOST is set
		assert.Contains(t, outWriter.String(), fmt.Sprintf(`uploaded testdata/conf.yml to %s:/tmp/.spot-`, adr))
		assert.Contains(t, outWriter.String(), `/conf.yml in`)
	})

	t.Run("script with echo, with variables", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		outWriter := &bytes.Buffer{}
		wr := io.MultiWriter(outWriter, os.Stderr)
		lgs := executor.MakeLogs(false, false, conf.AllSecretValues())
		lgs.Info = lgs.Info.WithWriter(wr)

		conn, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, lgs)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   conn,
			Playbook:    conf,
			Logs:        lgs,
			Only:        []string{"copy configuration", "some command", "echo things"},
		}
		log.SetOutput(io.Discard)
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 3, res.Commands)
		t.Log("out:\n", outWriter.String())
		assert.Contains(t, outWriter.String(), `completed command "echo things" {echo: vars - 6, 9, zzzzz}`)
	})

	t.Run("script with echo and with variables, verbose", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		outWriter := &bytes.Buffer{}
		wr := io.MultiWriter(outWriter, os.Stderr)
		lgs := executor.MakeLogs(true, false, conf.AllSecretValues())
		lgs.Info = lgs.Info.WithWriter(wr)

		conn, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, lgs)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   conn,
			Playbook:    conf,
			Logs:        lgs,
			Only:        []string{"copy configuration", "some command", "echo things"},
			Verbose:     true,
		}
		log.SetOutput(io.Discard)
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 3, res.Commands)
		t.Log("out:\n", outWriter.String())
		assert.Contains(t, outWriter.String(), `completed command "echo things" {echo: vars - 6, 9, zzzzz}`)
	})

	t.Run("echo with variables and sudo, verbose", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		outWriter := &bytes.Buffer{}
		wr := io.MultiWriter(outWriter, os.Stderr)
		lgs := executor.MakeLogs(true, false, conf.AllSecretValues())
		lgs.Info = lgs.Info.WithWriter(wr)

		conn, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, lgs)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   conn,
			Playbook:    conf,
			Logs:        lgs,
			Only:        []string{"copy configuration", "some command", "echo things sudo"},
			Verbose:     true,
		}
		log.SetOutput(io.Discard)
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 3, res.Commands)
		t.Log("out:\n", outWriter.String())
		assert.Contains(t, outWriter.String(), `completed command "echo things sudo" {echo: vars - 6, 9, zzzzz`)
		assert.Contains(t, outWriter.String(), `uid=0(root) gid=0(root)`)
	})

	t.Run("delete multiple files", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"prep multiple files for delete", "delete multiple files"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), `deleted /tmp/deleteme.1`)
		assert.Contains(t, outWriter.String(), `deleted /tmp/deleteme.2`)
	})

	t.Run("multi-line failed script", func(t *testing.T) {
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		_, err = p.Run(ctx, "multiline_failed", testingHostAndPort)
		require.ErrorContains(t, err, "failed to run command on remote server: Process exited with status 2")
		assert.Contains(t, outWriter.String(), ` > good command 1`)
		assert.NotContains(t, outWriter.String(), ` > good command 2`)
		assert.NotContains(t, outWriter.String(), ` > good command 3`)
	})

	t.Run("script with task-level user", func(t *testing.T) {
		testingHostAndPort, teardown := startTestContainerWithCustomUser(t, "test2")
		defer teardown()
		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		_, err = p.Run(ctx, "with_task_user", testingHostAndPort)
		require.NoError(t, err)
		assert.Contains(t, outWriter.String(), ` > good command 1`)
		assert.Contains(t, outWriter.String(), `(test2)`)
		assert.Contains(t, outWriter.String(), ` > test2`)
	})

	t.Run("line command operations", func(t *testing.T) {
		conf, err := config.New("testdata/conf-line.yml", nil, nil)
		require.NoError(t, err)

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Verbose:     true,
		}

		// capture log output to verify test results
		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "test line operations", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 21, res.Commands) // all commands in the task including verification steps

		logContent := outWriter.String()

		// verify test ran successfully with all checks passing
		assert.Contains(t, logContent, "✓ Comment lines deleted successfully")
		assert.Contains(t, logContent, "✓ old_config line deleted successfully")
		assert.Contains(t, logContent, "✓ debug setting replaced successfully")
		assert.Contains(t, logContent, "✓ feature setting replaced successfully")
		assert.Contains(t, logContent, "✓ monitoring line appended successfully")
		assert.Contains(t, logContent, "✓ Existing server.port line not duplicated")
		assert.Contains(t, logContent, "✓ Protected file modified successfully with sudo")
		assert.Contains(t, logContent, "✓ Variable substituted correctly")
		assert.Contains(t, logContent, "✓ Anchored match replaced correctly")

		// verify variable substitution
		assert.Contains(t, logContent, "host=localhost") // SPOT_REMOTE_ADDR resolves to localhost in test
		assert.NotContains(t, logContent, "host={SPOT_REMOTE_HOST}")
	})
}

func TestProcess_RunWithSudo(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()
	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	t.Run("single line script", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
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

	t.Run("single line script with var", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"root only single line with var"},
			SSHShell:    "/bin/sh",
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))
		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), " > sudo /bin/sh -c 'vvv=123 && echo var=$vvv'")
		assert.Contains(t, outWriter.String(), " > var=123")
	})

	t.Run("multi line script", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
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
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"root only copy single file"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		assert.Contains(t, outWriter.String(), "> sudo mv -f /tmp/.spot-")
		assert.Contains(t, outWriter.String(), "/conf.yml /srv/conf.yml")

		p.Only = []string{"root only stat /srv/conf.yml"}
		_, err = p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Contains(t, outWriter.String(), " File: /srv/conf.yml", "file was copied to /srv")
	})

	t.Run("copy glob files with sudo", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"root only copy multiple files"},
		}

		outWriter := &bytes.Buffer{}
		log.SetOutput(io.MultiWriter(outWriter, os.Stderr))

		res, err := p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Commands)
		assert.Equal(t, 1, res.Hosts)
		// check for "sudo mv -f /tmp/.spot-3004145016712714752/srv/* /srv", ignore the random tmp dir suffix
		assert.Contains(t, outWriter.String(), " > sudo mv -f /tmp/.spot-", "files were copied to /srv")
		assert.Contains(t, outWriter.String(), "/srv/* /srv", "files were copied to /srv")
		assert.Contains(t, outWriter.String(), "deleted recursively /tmp/.spot-", "tmp dir was removed")

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

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
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

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	t.Run("only, with auto", func(t *testing.T) {
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
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
			Playbook:    conf,
			Logs:        logs,
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
			Playbook:    conf,
			Logs:        logs,
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

	t.Run("verbose task", func(t *testing.T) {
		log.SetOutput(io.Discard)

		logs := executor.MakeLogs(true, false, nil)
		connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
		require.NoError(t, err)

		conf, err := config.New("testdata/conf.yml", nil, nil)
		require.NoError(t, err)
		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Verbose:     true,
			Skip:        []string{"wait"},
		}
		_, err = p.Run(ctx, "task1", testingHostAndPort)
		require.NoError(t, err)
	})

	t.Run("multi-line script with verbose (1)", func(t *testing.T) {
		log.SetOutput(io.Discard)
		stdout := captureStdOut(t, func() {
			logs := executor.MakeLogs(true, false, nil)
			connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
			require.NoError(t, err)

			conf, err := config.New("testdata/conf.yml", nil, nil)
			require.NoError(t, err)

			p := Process{
				Concurrency: 1,
				Connector:   connector,
				Playbook:    conf,
				Logs:        logs,
				Only:        []string{"copy configuration", "some command"},
				Verbose:     true,
				Verbose2:    false,
			}

			_, err = p.Run(ctx, "task1", testingHostAndPort)
			require.NoError(t, err)
		})

		t.Log(stdout)
		assert.NotContains(t, stdout, `+ #!/bin/sh`)
		assert.NotContains(t, stdout, `+ du -hcs /srv`)
		assert.Contains(t, stdout, ">       - name: wait\n")
	})

	t.Run("multi-line script with verbose (2)", func(t *testing.T) {
		log.SetOutput(io.Discard)
		stdout := captureStdOut(t, func() {
			logs := executor.MakeLogs(true, false, nil)
			connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
			require.NoError(t, err)

			conf, err := config.New("testdata/conf.yml", nil, nil)
			require.NoError(t, err)

			p := Process{
				Concurrency: 1,
				Connector:   connector,
				Playbook:    conf,
				Logs:        logs,
				Only:        []string{"copy configuration", "some command"},
				Verbose:     true,
				Verbose2:    true,
			}

			_, err = p.Run(ctx, "task1", testingHostAndPort)
			require.NoError(t, err)
		})

		t.Log(stdout)
		assert.Contains(t, stdout, `+ #!/bin/sh`)
		assert.Contains(t, stdout, `+ du -hcs /srv`)
		assert.Contains(t, stdout, ">       - name: wait\n")
	})
}

func TestProcess_RunLocal(t *testing.T) {
	ctx := context.Background()

	t.Run("command with local option", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		logs := executor.MakeLogs(false, false, nil)
		conf, err := config.New("testdata/conf-local.yml", nil, nil)
		require.NoError(t, err)
		p := Process{
			Concurrency: 1,
			Connector:   nil, // not needed for local execution
			Playbook:    conf,
			Logs:        logs,
			Verbose:     true,
		}
		res, err := p.Run(ctx, "default", "localhost")
		require.NoError(t, err)
		t.Log(buf.String())
		assert.Equal(t, 2, res.Commands)
		assert.Contains(t, buf.String(), "run command \"show content\"")
		assert.Contains(t, buf.String(), "run local command")
	})

	t.Run("process with Local flag forces all local", func(t *testing.T) {
		log.SetOutput(io.Discard)
		defer log.SetOutput(os.Stderr)

		// use simple test config
		conf, err := config.New("testdata/conf-local-test.yml", nil, nil)
		require.NoError(t, err)

		var res ProcResp
		stdout := captureStdOut(t, func() {
			logs := executor.MakeLogs(true, false, nil) // create logs with verbose=true inside capture
			p := Process{
				Concurrency: 1,
				Connector:   nil, // not needed for local execution
				Playbook:    conf,
				Logs:        logs,
				Verbose:     true,
				Local:       true, // force local mode
			}
			// run a simple task
			res, err = p.Run(ctx, "simple_task", "remark42")
		})

		require.NoError(t, err)
		t.Log(stdout)
		assert.Equal(t, 2, res.Commands) // 2 commands run locally once
		assert.Equal(t, 1, res.Hosts)    // only localhost, not the 2 remote hosts
		// should see local execution even for remote commands
		assert.Contains(t, stdout, "[localhost] run command")
		assert.Contains(t, stdout, "[localhost] completed task")
		// should NOT attempt SSH connection
		assert.NotContains(t, stdout, "can't connect")
	})
}

func TestProcess_RunFailed(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
	}
	_, err = p.Run(ctx, "failed_task", testingHostAndPort)
	require.ErrorContains(t, err, `failed command "bad command" on host`)
}

func TestProcess_RunFailed_WithOnError(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf-onerror.yml",
		&config.Overrides{Environment: map[string]string{"var1": "value1", "var2": "value2"}},
		secrets.NewMemoryProvider(map[string]string{"FOO": "FOO_SECRET", "BAR": "BAR_SECRET"}))
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
	}

	t.Run("onerror called, single line", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "failed_task_with_onerror_single", testingHostAndPort)
		require.ErrorContains(t, err, `failed command "bad command" on host`)
		t.Log(buf.String())
		require.Contains(t, buf.String(), "> onerror called")
		require.Contains(t, buf.String(), "var: value1", "onerror script has access to env vars")
		require.Contains(t, buf.String(), "secret: BAR_SECRET", "onerror script has access to secrets")
		require.Contains(t, buf.String(), "task: failed_task_with_onerror_single,")
	})

	t.Run("onerror called, multiline", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "failed_task_with_onerror_multiline", testingHostAndPort)
		require.ErrorContains(t, err, `failed command "bad command" on host`)
		t.Log(buf.String())
		require.Contains(t, buf.String(), "> onerror called")
		require.Contains(t, buf.String(), "var: value1", "onerror script has access to env vars")
		require.Contains(t, buf.String(), "secret foo: FOO_SECRET", "onerror script has access to secrets")
		require.Contains(t, buf.String(), "task: failed_task_with_onerror_multiline,")
	})

	t.Run("onerror failed", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "failed_task_with_bad_onerror", testingHostAndPort)
		require.ErrorContains(t, err, `failed command "bad command" on host`)
		t.Log(buf.String())
		require.NotContains(t, buf.String(), "onerror called")
		assert.Contains(t, buf.String(), "[WARN]")
		assert.Contains(t, buf.String(), `can't run on-error command for "failed_task_with_bad_onerror", "exit 1"`)
	})
}

func TestProcess_RunFailedErrIgnored(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "failed_task", conf.Tasks[1].Name)
	conf.Tasks[1].Commands[1].Options.IgnoreErrors = true
	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
	}
	_, err = p.Run(ctx, "failed_task", testingHostAndPort)
	require.NoError(t, err, "error ignored")
}

func TestProcess_RunWithOnExit(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
	}

	t.Run("on_exit called on script completion", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "with_onexit", testingHostAndPort)
		require.NoError(t, err)
		t.Log(buf.String())
		require.Contains(t, buf.String(), "> file content")
		require.Contains(t, buf.String(), "> on exit called. task: with_onexit")
		require.Contains(t, buf.String(), "> /bin/sh -c 'ls -la /tmp/file.txt'")
	})

	t.Run("on_exit called on script failed", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "with_onexit_failed", testingHostAndPort)
		require.Error(t, err)
		t.Log(buf.String())
		require.Contains(t, buf.String(), "> on exit called on failed. task: with_onexit_failed")
	})

	t.Run("on_exit called on copy completion", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)

		_, err = p.Run(ctx, "with_onexit_copy", testingHostAndPort)
		require.NoError(t, err)
		t.Log(buf.String())
		require.Contains(t, buf.String(), "> on exit called for copy. task: with_onexit_copy")
		require.Contains(t, buf.String(), "> removed '/tmp/conf-blah.yml'")
	})
}

func TestProcess_RunTaskWithWait(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)
	conf, err := config.New("testdata/conf.yml", nil, nil)
	require.NoError(t, err)

	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)

	_, err = p.Run(ctx, "with_wait", testingHostAndPort)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "wait done")
}

func Test_shouldRunCmd(t *testing.T) {
	testCases := []struct {
		name     string
		cmd      config.Cmd
		hostName string
		hostAddr string
		only     []string
		skip     []string
		expected bool
	}{
		{
			name:     "with no restrictions",
			cmd:      config.Cmd{Name: "echo"},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "with hostname restriction",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"host1"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "with ip address restriction",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"192.168.1.1"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "with excluded hostname restriction",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"!host1"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: false,
		},
		{
			name:     "with excluded ip address restriction",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"!192.168.1.1"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: false,
		},
		{
			name:     "in only list",
			cmd:      config.Cmd{Name: "echo"},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{"echo"},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "not in only list",
			cmd:      config.Cmd{Name: "echo"},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{"ls"},
			skip:     []string{},
			expected: false,
		},
		{
			name:     "in skip list",
			cmd:      config.Cmd{Name: "echo"},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{"echo"},
			expected: false,
		},
		{
			name:     "not in skip list",
			cmd:      config.Cmd{Name: "echo"},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{"ls"},
			expected: true,
		},
		{
			name:     "with noauto option and not in only list",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{NoAuto: true}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: false,
		},
		{
			name:     "with noauto option and in only list",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{NoAuto: true}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{"echo"},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "with multiple hostname restrictions",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"host1", "host2"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "with multiple ip address restrictions",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"192.168.1.1", "192.168.1.2"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: true,
		},
		{
			name:     "with excluded and included hostname restrictions",
			cmd:      config.Cmd{Name: "echo", Options: config.CmdOptions{OnlyOn: []string{"!host1", "host2"}}},
			hostName: "host1",
			hostAddr: "192.168.1.1",
			only:     []string{},
			skip:     []string{},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Process{Only: tc.only, Skip: tc.skip}
			assert.Equal(t, tc.expected, p.shouldRunCmd(tc.cmd, tc.hostName, tc.hostAddr))
		})
	}
}

func TestGen(t *testing.T) {
	mockPbook := &mocks.PlaybookMock{
		TargetHostsFunc: func(string) ([]config.Destination, error) {
			return []config.Destination{
				{Name: "test1", Host: "host1", Port: 8080, User: "user1", Tags: []string{"tag1", "tag2"}},
				{Name: "test2", Host: "host2", Port: 8081, User: "user2", Tags: []string{"tag3", "tag4"}},
			}, nil
		},
	}

	testCases := []struct {
		name      string
		target    string
		tmplInput string
		wantErr   bool
		want      string
	}{
		{
			name:      "single field",
			target:    "test",
			tmplInput: `{{range .}}{{.Name}}{{end}}`,
			wantErr:   false,
			want:      "test1test2",
		},
		{
			name:      "multiple fields",
			target:    "test",
			tmplInput: `{{range .}}{{.Name}}, {{.Host}}, {{.Port}}, {{.User}}{{end}}`,
			wantErr:   false,
			want:      "test1, host1, 8080, user1test2, host2, 8081, user2",
		},
		{
			name:      "invalid template",
			target:    "test",
			tmplInput: `{{range .}{.Name}}{{end}}`,
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Process{
				Playbook: mockPbook,
			}

			tmplRdr := bytes.NewBufferString(tc.tmplInput)
			respWr := &bytes.Buffer{}

			err := p.Gen([]string{tc.target}, tmplRdr, respWr)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, respWr.String())
			}
		})
	}
}

// TestRegisteredVarTemplateSubstitution tests the template substitution in register variables
func TestRegisteredVarTemplateSubstitution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	// capture log output to verify test results
	var buf bytes.Buffer
	log.SetOutput(io.MultiWriter(&buf, os.Stdout))
	defer log.SetOutput(os.Stdout)

	// initialize test components
	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)

	conf, err := config.New("testdata/register_templates.yml", nil, nil)
	require.NoError(t, err)

	// run the task with template variables in register
	p := Process{
		Concurrency: 1,
		Connector:   connector,
		Playbook:    conf,
		Logs:        logs,
		Verbose:     true,
	}

	// create a custom Playbook mock to verify UpdateRegisteredVars is called
	var calledWithVars map[string]string
	playMock := &mocks.PlaybookMock{
		AllTasksFunc: conf.AllTasks,
		TaskFunc: func(name string) (*config.Task, error) {
			return conf.Task(name)
		},
		TargetHostsFunc: func(name string) ([]config.Destination, error) {
			return conf.TargetHosts(name)
		},
		AllSecretValuesFunc:    conf.AllSecretValues,
		UpdateTasksTargetsFunc: conf.UpdateTasksTargets,
		UpdateRegisteredVarsFunc: func(vars map[string]string) {
			calledWithVars = vars
			conf.UpdateRegisteredVars(vars)
		},
	}

	// use testingHostAndPort as target to match docker test container
	p.Playbook = playMock
	res, err := p.Run(ctx, "register_with_template_vars", testingHostAndPort)
	require.NoError(t, err)

	// print out what's in the registered variables
	t.Logf("Registered variables: %+v", res.Registered)
	t.Logf("UpdateRegisteredVars called with: %+v", calledWithVars)

	// manually update registered variables on the playbook (mimicking what cmd/spot/main.go does)
	playMock.UpdateRegisteredVars(res.Registered)

	// verify the regular variable registration works
	assert.Contains(t, res.Registered, "STATIC_VAR")
	assert.Equal(t, "static-test", res.Registered["STATIC_VAR"])

	// check VAR_{SPOT_REMOTE_ADDR} -> VAR_[actual IP address]
	host := "localhost" // we know this is the hostname from our test config
	varHostKey := "VAR_" + host
	assert.Contains(t, res.Registered, varHostKey, "Host-specific variable not found")
	assert.Equal(t, "host-specific-value", res.Registered[varHostKey])

	// check second template substitution HOST_{SPOT_REMOTE_NAME} -> HOST_localhost
	assert.Contains(t, res.Registered, "HOST_localhost", "Host name template variable not found")
	assert.Equal(t, "hostname-specific-value", res.Registered["HOST_localhost"])

	// check template substitution with user CMD_{SPOT_REMOTE_USER} -> CMD_test
	assert.Contains(t, res.Registered, "CMD_test", "User template variable not found")
	assert.Equal(t, "command-template-value", res.Registered["CMD_test"])

	// check dynamic variable substitution DYNAMIC_{DYNAMIC_VAR} -> DYNAMIC_mydynamic
	assert.Contains(t, res.Registered, "DYNAMIC_mydynamic", "Dynamic variable not found")
	assert.Equal(t, "dynamic-test-value", res.Registered["DYNAMIC_mydynamic"])

	// verify we have logs showing successful execution
	logContent := buf.String()
	assert.Contains(t, logContent, "SUCCESS: All variables are correctly registered and accessible")

	// now run the second task to verify propagation of variables between tasks
	buf.Reset() // clear the log buffer

	// run the second task to verify variable propagation
	_, err = p.Run(ctx, "verify_variable_propagation", testingHostAndPort)
	require.NoError(t, err, "Second task should pass as variables are propagated")

	// verify the expected success message
	logContent = buf.String()
	assert.Contains(t, logContent, "SUCCESS: All variables are correctly propagated between tasks",
		"Variables should propagate between tasks")
}

func TestProcess_RunBcryptPassword(t *testing.T) {
	ctx := context.Background()
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	logs := executor.MakeLogs(false, false, nil)
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, err)

	conf, err := config.New("testdata/conf-bcrypt.yml", nil, nil)
	require.NoError(t, err)

	t.Run("auto export case", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(io.MultiWriter(&buf, os.Stdout))

		p := Process{
			Concurrency: 1,
			Connector:   connector,
			Playbook:    conf,
			Logs:        logs,
			Only:        []string{"export variable auto", "read variable auto"},
			Verbose:     true,
		}

		_, err := p.Run(ctx, "bcrypt_password_test", testingHostAndPort)
		logContent := buf.String()

		expectedBcrypt := "$2a$14$G.j2F3fm9wluTougUU52sOzePOvvpujjRrCoVp5qWVZ6qRJh58ISC"

		// check that export shows the correct value
		assert.Contains(t, logContent, "---- EXPORTED AUTO: "+expectedBcrypt, "Export should show correct bcrypt")

		// check that propagation preserves the value
		assert.Contains(t, logContent, "---- PROPAGATED AUTO: "+expectedBcrypt, "Propagation should preserve bcrypt")

		// the password should be preserved correctly
		require.NoError(t, err, "Command should succeed with preserved password")
		assert.Contains(t, logContent, "AUTO: Password preserved correctly", "Password should be preserved")
		assert.NotContains(t, logContent, "AUTO: Password corrupted!", "Password should not be corrupted")
	})
}

func TestProcess_pickCmdExecutor(t *testing.T) {
	logs := executor.MakeLogs(false, false, nil)

	t.Run("local mode forces local executor", func(t *testing.T) {
		p := Process{Local: true, Logs: logs}
		cmd := config.Cmd{Name: "test"}
		ec := execCmd{cmd: cmd, hostAddr: "remote.host", hostName: "remote"}

		result := p.pickCmdExecutor(cmd, ec, "remote.host", "remote")

		// verify executor is local even though host is remote
		assert.NotNil(t, result.exec)
		_, isLocal := result.exec.(*executor.Local)
		assert.True(t, isLocal, "should be local executor")
	})

	t.Run("local mode with dry run", func(t *testing.T) {
		p := Process{Local: true, Dry: true, Logs: logs}
		cmd := config.Cmd{Name: "test"}
		ec := execCmd{cmd: cmd, hostAddr: "remote.host", hostName: "remote"}

		result := p.pickCmdExecutor(cmd, ec, "remote.host", "remote")

		// verify executor is dry
		assert.NotNil(t, result.exec)
		_, isDry := result.exec.(*executor.Dry)
		assert.True(t, isDry, "should be dry executor")
	})

	t.Run("command local overrides even without global local", func(t *testing.T) {
		p := Process{Local: false, Logs: logs}
		cmd := config.Cmd{Name: "test", Options: config.CmdOptions{Local: true}}
		ec := execCmd{cmd: cmd, hostAddr: "remote.host", hostName: "remote"}

		result := p.pickCmdExecutor(cmd, ec, "remote.host", "remote")

		// verify executor is local
		assert.NotNil(t, result.exec)
		_, isLocal := result.exec.(*executor.Local)
		assert.True(t, isLocal, "should be local executor")
	})

	t.Run("no local flags returns original executor", func(t *testing.T) {
		p := Process{Local: false, Logs: logs}
		cmd := config.Cmd{Name: "test"}
		mockExec := &mocks.InterfaceMock{}
		ec := execCmd{cmd: cmd, hostAddr: "remote.host", hostName: "remote", exec: mockExec}

		result := p.pickCmdExecutor(cmd, ec, "remote.host", "remote")

		// verify executor unchanged
		assert.Equal(t, mockExec, result.exec)
	})
}

func startTestContainer(t *testing.T) (hostAndPort string, teardown func()) {
	return startTestContainerWithCustomUser(t, "test")
}

func startTestContainerWithCustomUser(t *testing.T, user string) (hostAndPort string, teardown func()) {
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
			"USER_NAME":   user,
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

// captureStdOut captures the output of a function that writes to stdout.
func captureStdOut(t *testing.T, f func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}
