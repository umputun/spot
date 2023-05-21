package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
)

func Test_templaterApply(t *testing.T) {
	tests := []struct {
		name     string
		inp      string
		user     string
		tmpl     templater
		expected string
	}{
		{
			name: "all_variables",
			inp:  "${SPOT_REMOTE_HOST}:${SPOT_REMOTE_USER}:${SPOT_COMMAND}:{SPOT_REMOTE_NAME}",
			tmpl: templater{
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
			tmpl: templater{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1"},
			},
			expected: "no_variables_here",
		},
		{
			name: "single_dollar_variable",
			inp:  "$SPOT_REMOTE_HOST:$SPOT_REMOTE_USER:$SPOT_COMMAND:$SPOT_REMOTE_NAME",
			tmpl: templater{
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
			tmpl: templater{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user2"},
			},
			expected: "example.com:user2:ls:task1",
		},
		{
			name: "escaped_variables",
			inp:  "\\${SPOT_REMOTE_HOST}:\\$SPOT_REMOTE_USER:\\${SPOT_COMMAND}",
			tmpl: templater{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "\\example.com:\\user:\\ls",
		},
		{
			name: "variables with normal text",
			inp:  "${SPOT_REMOTE_HOST} blah ${SPOT_TASK} ${SPOT_REMOTE_USER}:${SPOT_COMMAND}",
			tmpl: templater{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user2"},
			},
			expected: "example.com blah task1 user2:ls",
		},
		{
			name: "env variables",
			inp:  "${FOO} blah $BAR ${SPOT_REMOTE_USER}:${SPOT_COMMAND}",
			tmpl: templater{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user2"},
				env:      map[string]string{"FOO": "foo_val", "BAR": "bar_val"},
			},
			expected: "foo_val blah bar_val user2:ls",
		},
		{
			name: "with error msg",
			inp:  "$SPOT_REMOTE_HOST:$SPOT_REMOTE_USER:$SPOT_COMMAND ${SPOT_ERROR}",
			tmpl: templater{
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
			tmpl: templater{
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
			actual := tt.tmpl.apply(tt.inp)
			require.Equal(t, tt.expected, actual)
		})
	}
}

func Test_execCmd(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	ctx := context.Background()
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, errSess)

	t.Run("copy a single file", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy: config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt"}}}
		details, _, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt}", details)
	})

	t.Run("wait done", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", nil)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /tmp/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100}}}
		details, _, err := ec.Wait(ctx)
		require.NoError(t, err)
		t.Log(details)
	})

	t.Run("wait multiline done", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", nil)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "echo this is wait\ncat /tmp/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100}}}
		details, _, err := ec.Wait(ctx)
		require.NoError(t, err)
		t.Log(details)
	})

	t.Run("wait done with sudo", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "sudo touch /srv/wait.done", nil)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /srv/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100},
			Options: config.CmdOptions{Sudo: true}}}
		details, _, err := ec.Wait(ctx)
		require.NoError(t, err)
		t.Log(details)
	})

	t.Run("wait failed", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /tmp/wait.never-done", Timeout: 1 * time.Second, CheckDuration: time.Millisecond * 100}}}
		_, _, err := ec.Wait(ctx)
		require.EqualError(t, err, "timeout exceeded")
	})

	t.Run("wait failed with sudo", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /srv/wait.never-done", Timeout: 1 * time.Second, CheckDuration: time.Millisecond * 100},
			Options: config.CmdOptions{Sudo: true}}}
		_, _, err := ec.Wait(ctx)
		require.EqualError(t, err, "timeout exceeded")
	})

	t.Run("delete a single file", func(t *testing.T) {
		_, err := sess.Run(ctx, "touch /tmp/delete.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/tmp/delete.me"}}}
		_, _, err = ec.Delete(ctx)
		require.NoError(t, err)
	})

	t.Run("delete a multi-files", func(t *testing.T) {
		_, err := sess.Run(ctx, "touch /tmp/delete1.me /tmp/delete2.me ", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		_, err = sess.Run(ctx, "ls /tmp/delete1.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{MDelete: []config.DeleteInternal{
			{Location: "/tmp/delete1.me"}, {Location: "/tmp/delete2.me"}}}}
		_, _, err = ec.MDelete(ctx)
		require.NoError(t, err)
		_, err = sess.Run(ctx, "ls /tmp/delete1.me", &executor.RunOpts{Verbose: true})
		require.Error(t, err)
		_, err = sess.Run(ctx, "ls /tmp/delete2.me", &executor.RunOpts{Verbose: true})
		require.Error(t, err)
	})

	t.Run("delete files recursive", func(t *testing.T) {
		var err error
		_, err = sess.Run(ctx, "mkdir -p /tmp/delete-recursive", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		_, err = sess.Run(ctx, "touch /tmp/delete-recursive/delete1.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		_, err = sess.Run(ctx, "touch /tmp/delete-recursive/delete2.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/tmp/delete-recursive", Recursive: true}}}

		_, _, err = ec.Delete(ctx)
		require.NoError(t, err)

		_, err = sess.Run(ctx, "ls /tmp/delete-recursive", &executor.RunOpts{Verbose: true})
		require.Error(t, err, "should not exist")
	})

	t.Run("delete file with sudo", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/delete.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete.me"}, Options: config.CmdOptions{Sudo: false}}}

		_, _, err = ec.Delete(ctx)
		require.Error(t, err, "should fail because of missing sudo")

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete.me"}, Options: config.CmdOptions{Sudo: true}}}

		_, _, err = ec.Delete(ctx)
		require.NoError(t, err, "should fail pass with sudo")
	})

	t.Run("delete files recursive with sudo", func(t *testing.T) {
		var err error
		_, err = sess.Run(ctx, "sudo mkdir -p /srv/delete-recursive", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		_, err = sess.Run(ctx, "sudo touch /srv/delete-recursive/delete1.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		_, err = sess.Run(ctx, "sudo touch /srv/delete-recursive/delete2.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete-recursive", Recursive: true}, Options: config.CmdOptions{Sudo: true}}}

		_, _, err = ec.Delete(ctx)
		require.NoError(t, err)

		_, err = sess.Run(ctx, "ls /srv/delete-recursive", &executor.RunOpts{Verbose: true})
		require.Error(t, err, "should not exist")
	})

	t.Run("condition false", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "ls /srv/test.condition",
			Script: "echo 'condition false'", Name: "test"}}
		details, _, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", details)
	})

	t.Run("condition true", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/test.condition", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "ls -la /srv/test.condition",
			Script: "echo condition true", Name: "test"}}
		details, _, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {script: sh -c 'echo condition true'}", details)
	})

	t.Run("condition true inverted", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/test.condition", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "! ls -la /srv/test.condition",
			Script: "echo condition true", Name: "test"}}
		details, _, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", details)
	})

	t.Run("echo command", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "welcome back", Name: "test"}}
		details, _, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: welcome back}", details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "echo welcome back", Name: "test"}}
		details, _, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: welcome back}", details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$var1 welcome back", Name: "test", Environment: map[string]string{"var1": "foo"}}}
		details, _, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: foo welcome back}", details)
	})

	t.Run("sync command", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Sync: config.SyncInternal{
			Source: "testdata", Dest: "/tmp/sync.testdata", Exclude: []string{"conf2.yml"}}, Name: "test"}}
		details, _, err := ec.Sync(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {sync: testdata -> /tmp/sync.testdata}", details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$(ls -la /tmp/sync.testdata)", Name: "test"}}
		details, _, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, details, "conf.yml")
		assert.NotContains(t, details, "conf2.yml")
	})

	t.Run("msync command", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{MSync: []config.SyncInternal{
			{Source: "testdata", Dest: "/tmp/sync.testdata_m1", Exclude: []string{"conf2.yml"}},
			{Source: "testdata", Dest: "/tmp/sync.testdata_m2"},
		}, Name: "test"}}
		details, _, err := ec.Msync(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {sync: testdata -> /tmp/sync.testdata_m1, testdata -> /tmp/sync.testdata_m2}", details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$(ls -la /tmp/sync.testdata_m1)",
			Name: "test"}}
		details, _, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, details, "conf.yml")
		assert.NotContains(t, details, "conf2.yml")

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$(ls -la /tmp/sync.testdata_m2)",
			Name: "test"}}
		details, _, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, details, "conf.yml")
		assert.Contains(t, details, "conf2.yml")
	})
}
