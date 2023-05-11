package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

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
	connector, err := executor.NewConnector("testdata/test_ssh_key", time.Second*10)
	require.NoError(t, err)
	sess, err := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, err)

	t.Run("wait done", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", false)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /tmp/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100}}}
		details, _, err := ec.wait(ctx)
		require.NoError(t, err)
		t.Log(details)
	})

	t.Run("wait multiline done", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", false)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "echo this is wait\ncat /tmp/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100}}}
		details, _, err := ec.wait(ctx)
		require.NoError(t, err)
		t.Log(details)
	})

	t.Run("wait done with sudo", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "sudo touch /srv/wait.done", false)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /srv/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100},
			Options: config.CmdOptions{Sudo: true}}}
		details, _, err := ec.wait(ctx)
		require.NoError(t, err)
		t.Log(details)
	})

	t.Run("wait failed", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /tmp/wait.never-done", Timeout: 1 * time.Second, CheckDuration: time.Millisecond * 100}}}
		_, _, err := ec.wait(ctx)
		require.EqualError(t, err, "timeout exceeded")
	})

	t.Run("wait failed with sudo", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /srv/wait.never-done", Timeout: 1 * time.Second, CheckDuration: time.Millisecond * 100},
			Options: config.CmdOptions{Sudo: true}}}
		_, _, err := ec.wait(ctx)
		require.EqualError(t, err, "timeout exceeded")
	})

	t.Run("delete a single file", func(t *testing.T) {
		_, err := sess.Run(ctx, "touch /tmp/delete.me", true)
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/tmp/delete.me"}}}
		_, _, err = ec.delete(ctx)
		require.NoError(t, err)
	})

	t.Run("delete files recursive", func(t *testing.T) {
		var err error
		_, err = sess.Run(ctx, "mkdir -p /tmp/delete-recursive", true)
		require.NoError(t, err)
		_, err = sess.Run(ctx, "touch /tmp/delete-recursive/delete1.me", true)
		require.NoError(t, err)
		_, err = sess.Run(ctx, "touch /tmp/delete-recursive/delete2.me", true)
		require.NoError(t, err)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/tmp/delete-recursive", Recursive: true}}}

		_, _, err = ec.delete(ctx)
		require.NoError(t, err)

		_, err = sess.Run(ctx, "ls /tmp/delete-recursive", true)
		require.Error(t, err, "should not exist")
	})

	t.Run("delete file with sudo", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/delete.me", true)
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete.me"}, Options: config.CmdOptions{Sudo: false}}}

		_, _, err = ec.delete(ctx)
		require.Error(t, err, "should fail because of missing sudo")

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete.me"}, Options: config.CmdOptions{Sudo: true}}}

		_, _, err = ec.delete(ctx)
		require.NoError(t, err, "should fail pass with sudo")
	})

	t.Run("delete files recursive with sudo", func(t *testing.T) {
		var err error
		_, err = sess.Run(ctx, "sudo mkdir -p /srv/delete-recursive", true)
		require.NoError(t, err)
		_, err = sess.Run(ctx, "sudo touch /srv/delete-recursive/delete1.me", true)
		require.NoError(t, err)
		_, err = sess.Run(ctx, "sudo touch /srv/delete-recursive/delete2.me", true)
		require.NoError(t, err)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete-recursive", Recursive: true}, Options: config.CmdOptions{Sudo: true}}}

		_, _, err = ec.delete(ctx)
		require.NoError(t, err)

		_, err = sess.Run(ctx, "ls /srv/delete-recursive", true)
		require.Error(t, err, "should not exist")
	})
}
