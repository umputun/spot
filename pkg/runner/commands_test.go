package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
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
			name: "all variables, hostAddr without port",
			inp: "${SPOT_REMOTE_HOST} ${SPOT_REMOTE_USER} ${SPOT_COMMAND} {SPOT_REMOTE_NAME} " +
				"{SPOT_REMOTE_ADDR} {SPOT_REMOTE_PORT}",
			tmpl: templater{
				hostAddr: "example.com",
				hostName: "example",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "example.com user ls example example.com 22",
		},
		{
			name: "all variables, hostAddr with port",
			inp: "${SPOT_REMOTE_HOST} ${SPOT_REMOTE_USER} ${SPOT_COMMAND} {SPOT_REMOTE_NAME} " +
				"{SPOT_REMOTE_ADDR} {SPOT_REMOTE_PORT}",
			tmpl: templater{
				hostAddr: "example.com:22022",
				hostName: "example",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "example.com:22022 user ls example example.com 22022",
		},
		{
			name: "all variables, hostAddr ipv6 with port",
			inp: "${SPOT_REMOTE_HOST} ${SPOT_REMOTE_USER} ${SPOT_COMMAND} {SPOT_REMOTE_NAME} " +
				"{SPOT_REMOTE_ADDR} {SPOT_REMOTE_PORT}",
			tmpl: templater{
				hostAddr: "[2001:db8::1]:22022",
				hostName: "example",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
			},
			expected: "[2001:db8::1]:22022 user ls example 2001:db8::1 22022",
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
			name: "single quoted env variable with dollar signs",
			inp:  "Password: ${BCRYPT_PASSWORD}",
			tmpl: templater{
				hostAddr: "example.com",
				command:  "echo",
				task:     &config.Task{Name: "task1", User: "user"},
				env:      map[string]string{"BCRYPT_PASSWORD": "__SQ__:$2a$14$G.j2F3fm9wluTougUU52sOzePOvvpujjRrCoVp5qWVZ6qRJh58ISC"},
			},
			expected: "Password: \\$2a\\$14\\$G.j2F3fm9wluTougUU52sOzePOvvpujjRrCoVp5qWVZ6qRJh58ISC",
		},
		{
			name: "mixed env variables with and without SQ prefix",
			inp:  "${NORMAL} and ${QUOTED}",
			tmpl: templater{
				hostAddr: "example.com",
				command:  "echo",
				task:     &config.Task{Name: "task1", User: "user"},
				env:      map[string]string{"NORMAL": "normal value", "QUOTED": "__SQ__:$special$value"},
			},
			expected: "normal value and \\$special\\$value",
		},
		{
			name: "with error msg",
			inp:  "$SPOT_REMOTE_HOST:$SPOT_REMOTE_USER:$SPOT_COMMAND ${SPOT_ERROR} and $SPOT_ERROR",
			tmpl: templater{
				hostAddr: "example.com",
				command:  "ls",
				task:     &config.Task{Name: "task1", User: "user"},
				err:      fmt.Errorf("some error"),
			},
			expected: "example.com:user:ls some error and some error",
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
	logs := executor.MakeLogs(false, false, nil)
	ctx := context.Background()
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, errSess)

	t.Run("copy a single file", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy: config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt", Direction: "push"}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt}", resp.details)
	})

	t.Run("download a single file", func(t *testing.T) {
		// first upload a file to download
		_, err := sess.Run(ctx, "echo 'test content' > /tmp/download_test.txt", nil)
		require.NoError(t, err)

		tmpFile := "/tmp/spot_test_download_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".txt"
		defer os.Remove(tmpFile)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy: config.CopyInternal{Source: "/tmp/download_test.txt", Dest: tmpFile, Direction: "pull"}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "direction: pull")

		// verify file was downloaded
		content, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "test content")
	})

	t.Run("download with sudo", func(t *testing.T) {
		// create a file in /srv that needs sudo to read (like other working sudo tests)
		_, err := sess.Run(ctx, "sudo sh -c 'echo sudo-content > /srv/sudo_test.txt && chmod 600 /srv/sudo_test.txt && chown root:root /srv/sudo_test.txt'", nil)
		require.NoError(t, err)
		defer sess.Run(ctx, "sudo rm -f /srv/sudo_test.txt", nil)

		tmpFile := "/tmp/spot_test_sudo_download_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".txt"
		defer os.Remove(tmpFile)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy:    config.CopyInternal{Source: "/srv/sudo_test.txt", Dest: tmpFile, Direction: "pull"},
			Options: config.CmdOptions{Sudo: true}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "sudo: true")
		assert.Contains(t, resp.details, "direction: pull")

		// verify file was downloaded
		content, err := os.ReadFile(tmpFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "sudo-content")
	})

	t.Run("download with glob pattern and sudo", func(t *testing.T) {
		// create multiple test files that need sudo
		_, err := sess.Run(ctx, "sudo sh -c 'echo test1 > /srv/test1.log && echo test2 > /srv/test2.log && chmod 600 /srv/*.log && chown root:root /srv/*.log'", nil)
		require.NoError(t, err)
		defer sess.Run(ctx, "sudo rm -f /srv/*.log", nil)

		tmpDir := "/tmp/spot_test_glob_" + fmt.Sprintf("%d", time.Now().UnixNano())
		defer os.RemoveAll(tmpDir)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy:    config.CopyInternal{Source: "/srv/*.log", Dest: tmpDir, Direction: "pull", Mkdir: true},
			Options: config.CmdOptions{Sudo: true}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "sudo: true")
		assert.Contains(t, resp.details, "direction: pull")

		// verify both files were downloaded
		files, err := os.ReadDir(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, 2, len(files), "should have downloaded 2 files")
	})

	t.Run("copy with invalid direction", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy: config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt", Direction: "invalid"}}}
		_, err := ec.Copy(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid copy direction")
	})

	t.Run("copy pull with local execution", func(t *testing.T) {
		localExec := executor.NewLocal(logs)
		ec := execCmd{exec: localExec, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy:    config.CopyInternal{Source: "/tmp/test.txt", Dest: "./test.txt", Direction: "pull"},
			Options: config.CmdOptions{Local: true}}}
		_, err := ec.Copy(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot use direction=pull with local execution")
	})

	t.Run("mcopy with mixed directions", func(t *testing.T) {
		// create remote files to download
		_, err := sess.Run(ctx, "echo 'content1' > /tmp/mcopy_test1.txt", nil)
		require.NoError(t, err)
		_, err = sess.Run(ctx, "echo 'content2' > /tmp/mcopy_test2.txt", nil)
		require.NoError(t, err)

		// create local file to upload
		tmpUpload := "/tmp/mcopy_upload_" + strconv.Itoa(rand.Int())
		err = os.WriteFile(tmpUpload, []byte("upload content"), 0o644)
		require.NoError(t, err)
		defer os.Remove(tmpUpload)

		tmpDownload1 := "/tmp/mcopy_download1_" + strconv.Itoa(rand.Int())
		tmpDownload2 := "/tmp/mcopy_download2_" + strconv.Itoa(rand.Int())
		tmpRemoteUpload := "/tmp/mcopy_uploaded_" + strconv.Itoa(rand.Int())
		defer os.Remove(tmpDownload1)
		defer os.Remove(tmpDownload2)

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			MCopy: []config.CopyInternal{
				{Source: "/tmp/mcopy_test1.txt", Dest: tmpDownload1, Direction: "pull"},
				{Source: tmpUpload, Dest: tmpRemoteUpload, Direction: "push"},
				{Source: "/tmp/mcopy_test2.txt", Dest: tmpDownload2, Direction: "pull"},
			}}}
		resp, err := ec.Mcopy(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "<-") // pull indicator
		assert.Contains(t, resp.details, "->") // push indicator

		// verify downloads worked
		content1, err := os.ReadFile(tmpDownload1)
		require.NoError(t, err)
		assert.Contains(t, string(content1), "content1")

		content2, err := os.ReadFile(tmpDownload2)
		require.NoError(t, err)
		assert.Contains(t, string(content2), "content2")

		// verify upload worked
		out, err := sess.Run(ctx, "cat "+tmpRemoteUpload, nil)
		require.NoError(t, err)
		assert.Contains(t, out[0], "upload content")
	})

	t.Run("wait done", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", nil)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /tmp/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100}}}
		resp, err := ec.Wait(ctx)
		require.NoError(t, err)
		t.Log(resp.details)
	})

	t.Run("wait multiline done", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "touch /tmp/wait.done", nil)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "echo this is wait\ncat /tmp/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100}}}
		resp, err := ec.Wait(ctx)
		require.NoError(t, err)
		t.Log(resp.details)
	})

	t.Run("wait done with sudo", func(t *testing.T) {
		time.AfterFunc(time.Second, func() {
			_, _ = sess.Run(ctx, "sudo touch /srv/wait.done", nil)
		})
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /srv/wait.done", Timeout: 2 * time.Second, CheckDuration: time.Millisecond * 100},
			Options: config.CmdOptions{Sudo: true}}}
		resp, err := ec.Wait(ctx)
		require.NoError(t, err)
		t.Log(resp.details)
	})

	t.Run("wait failed", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /tmp/wait.never-done", Timeout: 1 * time.Second, CheckDuration: time.Millisecond * 100}}}
		_, err := ec.Wait(ctx)
		require.EqualError(t, err, "timeout exceeded")
	})

	t.Run("wait failed with sudo", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Wait: config.WaitInternal{
			Command: "cat /srv/wait.never-done", Timeout: 1 * time.Second, CheckDuration: time.Millisecond * 100},
			Options: config.CmdOptions{Sudo: true}}}
		_, err := ec.Wait(ctx)
		require.EqualError(t, err, "timeout exceeded")
	})

	t.Run("delete a single file", func(t *testing.T) {
		_, err := sess.Run(ctx, "touch /tmp/delete.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/tmp/delete.me"}}}
		_, err = ec.Delete(ctx)
		require.NoError(t, err)
	})

	t.Run("delete a multi-files", func(t *testing.T) {
		_, err := sess.Run(ctx, "touch /tmp/delete1.me /tmp/delete2.me ", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		_, err = sess.Run(ctx, "ls /tmp/delete1.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{MDelete: []config.DeleteInternal{
			{Location: "/tmp/delete1.me"}, {Location: "/tmp/delete2.me"}}}}
		_, err = ec.MDelete(ctx)
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

		_, err = ec.Delete(ctx)
		require.NoError(t, err)

		_, err = sess.Run(ctx, "ls /tmp/delete-recursive", &executor.RunOpts{Verbose: true})
		require.Error(t, err, "should not exist")
	})

	t.Run("delete file with sudo", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/delete.me", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete.me"}, Options: config.CmdOptions{Sudo: false}}}

		_, err = ec.Delete(ctx)
		require.Error(t, err, "should fail because of missing sudo")

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Delete: config.DeleteInternal{
			Location: "/srv/delete.me"}, Options: config.CmdOptions{Sudo: true}}}

		_, err = ec.Delete(ctx)
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

		_, err = ec.Delete(ctx)
		require.NoError(t, err)

		_, err = sess.Run(ctx, "ls /srv/delete-recursive", &executor.RunOpts{Verbose: true})
		require.Error(t, err, "should not exist")
	})

	t.Run("condition false", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "ls /srv/test.condition",
			Script: "echo 'condition false'", Name: "test"}}
		resp, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", resp.details)
	})

	t.Run("condition true", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/test.condition", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "ls -la /srv/test.condition",
			Script: "echo condition true", Name: "test"}}
		resp, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {script: /bin/sh -c 'echo condition true'}", resp.details)
	})

	t.Run("condition true, sudo only", func(t *testing.T) {
		// check without sudo condition on root-only file
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "cat /etc/shadow",
			Script: "echo condition true", Name: "test"}}
		resp, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", resp.details)

		// check with sudo condition on root-only file
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "cat /etc/shadow",
			Script: "echo condition true", Name: "test", Options: config.CmdOptions{Sudo: true}}}
		resp, err = ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {script: /bin/sh -c 'echo condition true', sudo: true}", resp.details)
	})

	t.Run("condition true inverted", func(t *testing.T) {
		_, err := sess.Run(ctx, "sudo touch /srv/test.condition", &executor.RunOpts{Verbose: true})
		require.NoError(t, err)
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Condition: "! ls -la /srv/test.condition",
			Script: "echo condition true", Name: "test"}}
		resp, err := ec.Script(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", resp.details)
	})

	t.Run("sync command", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Sync: config.SyncInternal{
			Source: "testdata", Dest: "/tmp/sync.testdata", Exclude: []string{"conf2.yml"}}, Name: "test"}}
		resp, err := ec.Sync(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {sync: testdata -> /tmp/sync.testdata}", resp.details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$(ls -la /tmp/sync.testdata)", Name: "test"}}
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "conf.yml")
		assert.NotContains(t, resp.details, "conf2.yml")
	})

	t.Run("msync command", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{MSync: []config.SyncInternal{
			{Source: "testdata", Dest: "/tmp/sync.testdata_m1", Exclude: []string{"conf2.yml"}},
			{Source: "testdata", Dest: "/tmp/sync.testdata_m2"},
		}, Name: "test"}}
		resp, err := ec.Msync(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {sync: testdata -> /tmp/sync.testdata_m1, testdata -> /tmp/sync.testdata_m2}", resp.details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$(ls -la /tmp/sync.testdata_m1)",
			Name: "test"}}
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "conf.yml")
		assert.NotContains(t, resp.details, "conf2.yml")

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$(ls -la /tmp/sync.testdata_m2)",
			Name: "test"}}
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "conf.yml")
		assert.Contains(t, resp.details, "conf2.yml")
	})

	t.Run("dbl-copy non-forced", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy: config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt"}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt}", resp.details)

		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))
		// copy again, should be skipped
		resp, err = ec.Copy(ctx)
		require.NoError(t, err)
		assert.Contains(t, wr.String(), "remote file /tmp/inventory.txt identical to local file testdata/inventory.yml, skipping upload")
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt}", resp.details)
	})

	t.Run("dbl-copy forced", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Copy: config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt", Force: true}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt}", resp.details)

		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))
		// copy again, should not be skipped
		resp, err = ec.Copy(ctx)
		require.NoError(t, err)
		assert.NotContains(t, wr.String(), "remote file /tmp/inventory.txt identical to local file testdata/inventory.yml, skipping upload")
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt}", resp.details)
	})

	t.Run("script temp files cleanup", func(t *testing.T) {
		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))

		// run a multi-line script that will create temp files
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Script: "echo 'line1'\necho 'line2'\necho 'line3'",
		}}
		resp, err := ec.Script(ctx)
		require.NoError(t, err)
		t.Logf("resp: %+v", resp)

		// check that the temp file was cleaned up
		_, err = sess.Run(ctx, "ls -la /tmp/spot*", nil)
		require.Error(t, err, "temp script file should be cleaned up")
		_, err = sess.Run(ctx, "ls -la /tmp/.spot-*", nil)
		require.Error(t, err, "temp dir should be cleaned up")

		// also test cleanup on failure
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Script: "echo 'line1'\nexit 1\necho 'line3'",
		}}
		_, err = ec.Script(ctx)
		require.Error(t, err)

		// check cleanup after failure
		_, err = sess.Run(ctx, "ls -la /tmp/spot*", nil)
		require.Error(t, err, "temp script file should be cleaned up even after failure")
		_, err = sess.Run(ctx, "ls -la /tmp/.spot-*", nil)
		require.Error(t, err, "temp dir should be cleaned up even after failure")
	})
}

func Test_execEcho(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()
	logs := executor.MakeLogs(false, false, nil)
	ctx := context.Background()
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, errSess)

	t.Run("echo command", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "welcome back", Name: "test"}}
		resp, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: welcome back}", resp.details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "echo welcome back", Name: "test"}}
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: welcome back}", resp.details)

		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{Echo: "$var1 welcome back", Name: "test", Environment: map[string]string{"var1": "foo"}}}
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: foo welcome back}", resp.details)
	})

	t.Run("echo command with condition true", func(t *testing.T) {
		defer os.Remove("/tmp/test.condition")
		_, err := sess.Run(ctx, "touch /tmp/test.condition", nil)
		require.NoError(t, err)
		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Echo:      "welcome back",
				Name:      "test",
				Condition: "test -f /tmp/test.condition",
			},
		}
		resp, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: welcome back}", resp.details)
	})

	t.Run("echo command with condition false", func(t *testing.T) {
		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Echo:      "welcome back",
				Name:      "test",
				Condition: "test -f /tmp/nonexistent.condition",
			},
		}
		resp, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", resp.details)
	})

	t.Run("echo command with condition true inverted", func(t *testing.T) {
		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Echo:      "welcome back",
				Name:      "test",
				Condition: "! test -f /tmp/nonexistent.condition",
			},
		}
		resp, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: welcome back}", resp.details)
	})

	t.Run("echo command with condition false inverted", func(t *testing.T) {
		defer os.Remove("/tmp/test2.condition")
		_, err := sess.Run(ctx, "touch /tmp/test2.condition", nil)
		require.NoError(t, err)
		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Echo:      "welcome back",
				Name:      "test",
				Condition: "! test -f /tmp/test2.condition",
			},
		}
		resp, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test}", resp.details)
	})

	t.Run("echo command with sudo", func(t *testing.T) {
		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Echo: "hello from sudo",
				Name: "test",
				Options: config.CmdOptions{
					Sudo: true,
				},
			},
		}
		resp, err := ec.Echo(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {echo: hello from sudo}", resp.details)

		ec = execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Echo: "$(id)",
				Name: "test",
			},
		}
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "uid=911(test)")

		ec.cmd.Options.Sudo = true
		resp, err = ec.Echo(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "uid=0(root)")
	})
}

func Test_execLine(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()
	logs := executor.MakeLogs(false, false, nil)
	ctx := context.Background()
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, errSess)

	t.Run("line command delete", func(t *testing.T) {
		// create a test file with multiple lines
		testFile := "/tmp/test_line_delete.txt"
		_, err := sess.Run(ctx, fmt.Sprintf("echo -e 'line1\\nline2\\nline3' > %s", testFile), nil)
		require.NoError(t, err)
		defer sess.Run(ctx, fmt.Sprintf("rm -f %s", testFile), nil)

		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Line: config.LineInternal{
					File:   testFile,
					Match:  "line2",
					Delete: true,
				},
				Name: "test line delete",
			},
		}
		resp, err := ec.Line(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {line: /tmp/test_line_delete.txt, delete: line2}", resp.details)

		// verify line was deleted
		out, err := sess.Run(ctx, fmt.Sprintf("cat %s", testFile), nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "line3"}, out)
	})

	t.Run("line command replace", func(t *testing.T) {
		// create a test file
		testFile := "/tmp/test_line_replace.txt"
		_, err := sess.Run(ctx, fmt.Sprintf("echo -e 'line1\\nline2\\nline3' > %s", testFile), nil)
		require.NoError(t, err)
		defer sess.Run(ctx, fmt.Sprintf("rm -f %s", testFile), nil)

		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Line: config.LineInternal{
					File:    testFile,
					Match:   "line2",
					Replace: "replaced line",
				},
				Name: "test line replace",
			},
		}
		resp, err := ec.Line(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {line: /tmp/test_line_replace.txt, replace: line2}", resp.details)

		// verify line was replaced
		out, err := sess.Run(ctx, fmt.Sprintf("cat %s", testFile), nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "replaced line", "line3"}, out)
	})

	t.Run("line command append", func(t *testing.T) {
		// create a test file without the pattern
		testFile := "/tmp/test_line_append.txt"
		_, err := sess.Run(ctx, fmt.Sprintf("echo -e 'line1\\nline2' > %s", testFile), nil)
		require.NoError(t, err)
		defer sess.Run(ctx, fmt.Sprintf("rm -f %s", testFile), nil)

		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Line: config.LineInternal{
					File:   testFile,
					Match:  "line3",
					Append: "line3",
				},
				Name: "test line append",
			},
		}
		resp, err := ec.Line(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {line: /tmp/test_line_append.txt, append: line3}", resp.details)

		// verify line was appended
		out, err := sess.Run(ctx, fmt.Sprintf("cat %s", testFile), nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "line2", "line3"}, out)
	})

	t.Run("line command append skip if exists", func(t *testing.T) {
		// create a test file with the pattern already present
		testFile := "/tmp/test_line_append_skip.txt"
		_, err := sess.Run(ctx, fmt.Sprintf("echo -e 'line1\\nline2\\nline3' > %s", testFile), nil)
		require.NoError(t, err)
		defer sess.Run(ctx, fmt.Sprintf("rm -f %s", testFile), nil)

		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Line: config.LineInternal{
					File:   testFile,
					Match:  "line2",
					Append: "should not be added",
				},
				Name: "test line append skip",
			},
		}
		resp, err := ec.Line(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {line: /tmp/test_line_append_skip.txt, match: line2, skip: pattern found}", resp.details)

		// verify file unchanged
		out, err := sess.Run(ctx, fmt.Sprintf("cat %s", testFile), nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "line2", "line3"}, out)
	})

	t.Run("line command with condition", func(t *testing.T) {
		testFile := "/tmp/test_line_condition.txt"
		_, err := sess.Run(ctx, fmt.Sprintf("echo 'test' > %s", testFile), nil)
		require.NoError(t, err)
		defer sess.Run(ctx, fmt.Sprintf("rm -f %s", testFile), nil)

		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Line: config.LineInternal{
					File:   testFile,
					Match:  "test",
					Delete: true,
				},
				Name:      "test line with condition",
				Condition: "test -f /tmp/nonexistent.condition",
			},
		}
		resp, err := ec.Line(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: test line with condition}", resp.details)

		// verify file unchanged
		out, err := sess.Run(ctx, fmt.Sprintf("cat %s", testFile), nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"test"}, out)
	})

	t.Run("line command with sudo", func(t *testing.T) {
		// create a test file in a protected directory
		testDir := "/srv/test_line_sudo"
		testFile := "/srv/test_line_sudo/test.txt"
		_, err := sess.Run(ctx, fmt.Sprintf("sudo mkdir -p %s && echo -e 'line1\\nline2\\nline3' | sudo tee %s > /dev/null", testDir, testFile), nil)
		require.NoError(t, err)
		defer sess.Run(ctx, fmt.Sprintf("sudo rm -rf %s", testDir), nil)

		ec := execCmd{
			exec: sess,
			tsk:  &config.Task{Name: "test"},
			cmd: config.Cmd{
				Line: config.LineInternal{
					File:   testFile,
					Match:  "line2",
					Delete: true,
				},
				Name: "test line delete with sudo",
				Options: config.CmdOptions{
					Sudo: true,
				},
			},
		}
		resp, err := ec.Line(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {line: /srv/test_line_sudo/test.txt, delete: line2}", resp.details)

		// verify line was deleted
		out, err := sess.Run(ctx, fmt.Sprintf("sudo cat %s", testFile), nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "line3"}, out)
	})
}

func Test_execCmdWithTmp(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	ctx := context.Background()
	logs := executor.MakeLogs(false, false, nil)
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, errSess)

	extractTmpPath := func(log string) string {
		pattern := `upload\s+\S+\s+to\s+(\S+/tmp/\S+/)`
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(log)
		if len(match) > 1 {
			return strings.ReplaceAll(match[1], "localhost:", "")
		}
		return ""
	}

	t.Run("multi-line script", func(t *testing.T) {
		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Script:  "echo 'hello world'\n" + "echo 'hello world2'\n" + "echo 'hello world3'\n" + "echo 'hello world4'\n",
		}}
		resp, err := ec.Script(ctx)
		require.NoError(t, err)
		// {script: sh -c /tmp/.spot-8420993611669644288/spot-script1149755050, sudo: true}
		assert.Contains(t, resp.details, " {script: /bin/sh -c /tmp/.spot-")
		assert.Contains(t, resp.details, ", sudo: true}")

		// [INFO] deleted recursively /tmp/.spot-8279767396215533568
		assert.Contains(t, wr.String(), "deleted recursively /tmp/.spot-")

		assert.Contains(t, wr.String(), "> hello world")
		assert.Contains(t, wr.String(), "> hello world2")
		assert.Contains(t, wr.String(), "> hello world3")
		assert.Contains(t, wr.String(), "> hello world4")

		// check if tmp dir removed
		tmpPath := extractTmpPath(wr.String())
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Script:  "ls -la " + tmpPath},
		}
		resp, err = ec.Script(ctx)
		require.Error(t, err)
		assert.Contains(t, wr.String(), fmt.Sprintf("cannot access '%s'", tmpPath))
	})

	t.Run("copy a single file with sudo", func(t *testing.T) {
		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Copy:    config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt"}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt, sudo: true}", resp.details)
		tmpPath := extractTmpPath(wr.String())
		assert.NotEmpty(t, tmpPath)
		t.Logf("tmpPath: %s", tmpPath)

		// check if dest contains file
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Script: "ls -la /tmp/inventory.txt"},
		}
		resp, err = ec.Script(ctx)
		require.NoError(t, err)
		assert.Contains(t, wr.String(), "/tmp/inventory.txt")
		assert.Contains(t, wr.String(), "> -rw-r--r-- ")

		// check if tmp dir removed
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Script:  "ls -la " + tmpPath},
		}
		resp, err = ec.Script(ctx)
		require.Error(t, err)
		assert.Contains(t, wr.String(), fmt.Sprintf("cannot access '%s'", tmpPath))
	})

	t.Run("copy a single file with sudo and chmod+x", func(t *testing.T) {
		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Copy:    config.CopyInternal{Source: "testdata/inventory.yml", Dest: "/tmp/inventory.txt", ChmodX: true}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/inventory.yml -> /tmp/inventory.txt, sudo: true, chmod: +x}", resp.details)
		tmpPath := extractTmpPath(wr.String())
		assert.NotEmpty(t, tmpPath)
		t.Logf("tmpPath: %s", tmpPath)

		// check if dest contains file
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Script: "ls -la /tmp/inventory.txt"},
		}
		resp, err = ec.Script(ctx)
		require.NoError(t, err)
		assert.Contains(t, wr.String(), "/tmp/inventory.txt")
		assert.Contains(t, wr.String(), "> -rwxr-xr-x ", "file should be executable")

		// check if tmp dir removed
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Script:  "ls -la " + tmpPath},
		}
		resp, err = ec.Script(ctx)
		require.Error(t, err)
		assert.Contains(t, wr.String(), fmt.Sprintf("cannot access '%s'", tmpPath))
	})

	t.Run("copy multiple files with sudo", func(t *testing.T) {
		wr := bytes.NewBuffer(nil)
		log.SetOutput(io.MultiWriter(wr, os.Stdout))

		defer func() {
			_ = os.RemoveAll("/tmp/spot-test-dest")
		}()

		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Copy:    config.CopyInternal{Source: "testdata/*.yml", Dest: "/tmp/spot-test-dest", Mkdir: true}}}
		resp, err := ec.Copy(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {copy: testdata/*.yml -> /tmp/spot-test-dest, sudo: true}", resp.details)
		tmpPath := extractTmpPath(wr.String())
		assert.NotEmpty(t, tmpPath)
		t.Logf("tmpPath: %s", tmpPath)

		// check if dest contains files
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Script: "ls -la /tmp/spot-test-dest"},
		}
		resp, err = ec.Script(ctx)
		require.NoError(t, err)
		assert.Contains(t, wr.String(), "inventory.yml")
		assert.Contains(t, wr.String(), "conf.yml")

		// check if tmp dir removed
		wr.Reset()
		ec = execCmd{exec: sess, tsk: &config.Task{Name: "test"}, cmd: config.Cmd{
			Options: config.CmdOptions{Sudo: true},
			Script:  "ls -la " + tmpPath},
		}
		resp, err = ec.Script(ctx)
		require.Error(t, err)
		assert.Contains(t, wr.String(), fmt.Sprintf("cannot access '%s'", tmpPath))
	})
}

func Test_execCmd_prepScript(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()

	ctx := context.Background()
	logs := executor.MakeLogs(false, false, nil)
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-host", "test")
	require.NoError(t, errSess)

	t.Run("single line command, no temp files", func(t *testing.T) {
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}}
		cmd, script, teardown, err := ec.prepScript(ctx, "echo hello", nil)
		require.NoError(t, err)
		assert.Equal(t, "echo hello", cmd)
		assert.Empty(t, script)
		assert.Nil(t, teardown)
	})

	t.Run("multiline script with temp files", func(t *testing.T) {
		input := "#!/bin/sh\necho 'line 1'\necho 'line 2'\n"
		ec := execCmd{exec: sess, tsk: &config.Task{Name: "test"}}

		// capture log output
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stdout)

		cmd, _, teardown, err := ec.prepScript(ctx, "", bytes.NewBufferString(input))
		require.NoError(t, err)
		require.NotNil(t, teardown)
		defer teardown()

		// verify command format
		assert.Contains(t, cmd, "/bin/sh -c /tmp/.spot-")

		// verify script content matches input
		remotePath := strings.Split(cmd, " ")[2]
		out, err := sess.Run(ctx, fmt.Sprintf("cat %s", remotePath), nil)
		require.NoError(t, err)
		assert.Equal(t, input, strings.Join(out, "\n")+"\n")

		// verify local temp file was removed (check logs)
		assert.Contains(t, buf.String(), "[DEBUG] removed local temp script")
	})

	t.Run("failed upload cleanup", func(t *testing.T) {
		// create invalid executor that will fail on upload
		invalidSess := &executor.Remote{} // this will fail on upload

		// capture log output
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stdout)

		ec := execCmd{exec: invalidSess, tsk: &config.Task{Name: "test"}}
		_, _, _, err := ec.prepScript(ctx, "", bytes.NewBufferString("echo test"))
		require.Error(t, err)

		// verify local temp file was removed even on error (check logs)
		assert.Contains(t, buf.String(), "[DEBUG] removed local temp script")
	})
}

func Test_execCmd_uniqueTmp(t *testing.T) {
	t.Run("default tmp location", func(t *testing.T) {
		ec := &execCmd{}
		tmp1 := ec.uniqueTmp("/tmp/.spot-")
		tmp2 := ec.uniqueTmp("/tmp/.spot-")
		t.Logf("tmp1: %s, tmp2: %s", tmp1, tmp2)

		require.NotEqual(t, tmp1, tmp2, "uniqueTmp should generate unique temporary directory names")
		require.True(t, strings.HasPrefix(tmp1, "/tmp/.spot-"), "uniqueTmp should use the provided prefix")
		require.True(t, strings.HasPrefix(tmp2, "/tmp/.spot-"), "uniqueTmp should use the provided prefix")
	})

	t.Run("custom tmp location", func(t *testing.T) {
		ec := &execCmd{sshTmpDir: "/custom/tmp"}
		tmp := ec.uniqueTmp("/tmp/.spot-")
		t.Logf("tmp: %s", tmp)

		require.True(t, strings.HasPrefix(tmp, "/custom/tmp/.spot-"), "uniqueTmp should use the custom temporary directory")
	})
}

func Test_execCmd_wrapWithSudo(t *testing.T) {
	t.Run("no sudo", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{Options: config.CmdOptions{Sudo: false}},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "ls -la", result, "command should not be wrapped when sudo is false")
	})

	t.Run("sudo without password", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{Options: config.CmdOptions{Sudo: true}},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "sudo ls -la", result, "command should be wrapped with sudo when no password")
	})

	t.Run("sudo with password secret key but no secrets loaded", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{
				Options: config.CmdOptions{Sudo: true, SudoPassword: "my_sudo_key"},
			},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "sudo ls -la", result, "should fallback to passwordless sudo when secrets not loaded")
	})

	t.Run("sudo with password from secrets", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{
				Options: config.CmdOptions{Sudo: true, SudoPassword: "my_sudo_key"},
				Secrets: map[string]string{"my_sudo_key": "secret123"},
			},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "printf '%s\\n' 'secret123' | sudo -S ls -la", result,
			"command should be wrapped with sudo -S and password piping")
	})

	t.Run("sudo with empty password in secrets", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{
				Options: config.CmdOptions{Sudo: true, SudoPassword: "my_sudo_key"},
				Secrets: map[string]string{"my_sudo_key": ""},
			},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "sudo ls -la", result, "should fallback to passwordless sudo when password is empty")
	})

	t.Run("sudo with password containing single quotes", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{
				Options: config.CmdOptions{Sudo: true, SudoPassword: "my_sudo_key"},
				Secrets: map[string]string{"my_sudo_key": "pass'word"},
			},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "printf '%s\\n' 'pass'\\''word' | sudo -S ls -la", result,
			"password with single quotes should be properly escaped")
	})

	t.Run("sudo with password containing multiple special chars", func(t *testing.T) {
		ec := &execCmd{
			cmd: config.Cmd{
				Options: config.CmdOptions{Sudo: true, SudoPassword: "my_sudo_key"},
				Secrets: map[string]string{"my_sudo_key": "p'a's's'w'o'r'd'"},
			},
		}
		result := ec.wrapWithSudo("ls -la")
		assert.Equal(t, "printf '%s\\n' 'p'\\''a'\\''s'\\''s'\\''w'\\''o'\\''r'\\''d'\\''' | sudo -S ls -la", result,
			"password with multiple single quotes should be properly escaped")
	})
}

func TestProcessRegisterWithTemplateVars(t *testing.T) {
	task := &config.Task{Name: "test_task"}

	// test normal registration without templates
	t.Run("regular register", func(t *testing.T) {
		cmd := execCmd{
			cmd: config.Cmd{
				Script:   "export MY_VAR=test",
				Register: []string{"MY_VAR"},
			},
			hostAddr: "192.168.1.10:22",
			hostName: "hostA",
			tsk:      task,
		}

		out := []string{"setvar MY_VAR=test"}
		resp := execCmdResp{
			vars:       make(map[string]string),
			registered: make(map[string]string),
		}

		// process the setvar line like in Script method
		for _, line := range out {
			if !strings.HasPrefix(line, "setvar ") {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(line, "setvar"), "=", 2)
			if len(parts) != 2 {
				continue
			}
			key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			resp.vars[key] = val

			// process register values with templating
			for _, registerVar := range cmd.cmd.Register {
				// apply template substitution for register variables
				tmpl := templater{
					hostAddr: cmd.hostAddr,
					hostName: cmd.hostName,
					task:     cmd.tsk,
					command:  cmd.cmd.Name,
					env:      cmd.cmd.Environment,
				}

				processedRegister := tmpl.apply(registerVar)

				// if the key matches (either original or templated), register it
				if key == registerVar || key == processedRegister {
					resp.registered[key] = val
					break
				}
			}
		}

		assert.Equal(t, "test", resp.vars["MY_VAR"])
		assert.Equal(t, "test", resp.registered["MY_VAR"])
	})

	// test with template variable in register
	t.Run("register with template var", func(t *testing.T) {
		cmd := execCmd{
			cmd: config.Cmd{
				Script:   "export MY_VAR_192.168.1.10=test",
				Register: []string{"MY_VAR_{SPOT_REMOTE_ADDR}"},
			},
			hostAddr: "192.168.1.10:22",
			hostName: "hostA",
			tsk:      task,
		}

		out := []string{"setvar MY_VAR_192.168.1.10=test"}
		resp := execCmdResp{
			vars:       make(map[string]string),
			registered: make(map[string]string),
		}

		// process the setvar line like in Script method
		for _, line := range out {
			if !strings.HasPrefix(line, "setvar ") {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(line, "setvar"), "=", 2)
			if len(parts) != 2 {
				continue
			}
			key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			resp.vars[key] = val

			// process register values with templating
			for _, registerVar := range cmd.cmd.Register {
				// apply template substitution for register variables
				tmpl := templater{
					hostAddr: cmd.hostAddr,
					hostName: cmd.hostName,
					task:     cmd.tsk,
					command:  cmd.cmd.Name,
					env:      cmd.cmd.Environment,
				}

				processedRegister := tmpl.apply(registerVar)
				t.Logf("Original register var: %s, Processed: %s, Key: %s", registerVar, processedRegister, key)

				// if the key matches (either original or templated), register it
				if key == registerVar || key == processedRegister {
					resp.registered[key] = val
					break
				}
			}
		}

		assert.Equal(t, "test", resp.vars["MY_VAR_192.168.1.10"])
		assert.Equal(t, "test", resp.registered["MY_VAR_192.168.1.10"], "Should match processed register var")
	})

	// test with environment variable in register
	t.Run("register with env var", func(t *testing.T) {
		cmd := execCmd{
			cmd: config.Cmd{
				Script:      "export VAR_production=env-value",
				Register:    []string{"VAR_{ENV_TYPE}"},
				Environment: map[string]string{"ENV_TYPE": "production"},
			},
			hostAddr: "192.168.1.10:22",
			hostName: "hostA",
			tsk:      task,
		}

		out := []string{"setvar VAR_production=env-value"}
		resp := execCmdResp{
			vars:       make(map[string]string),
			registered: make(map[string]string),
		}

		// process the setvar line like in Script method
		for _, line := range out {
			if !strings.HasPrefix(line, "setvar ") {
				continue
			}
			parts := strings.SplitN(strings.TrimPrefix(line, "setvar"), "=", 2)
			if len(parts) != 2 {
				continue
			}
			key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			resp.vars[key] = val

			// process register values with templating
			for _, registerVar := range cmd.cmd.Register {
				// apply template substitution for register variables
				tmpl := templater{
					hostAddr: cmd.hostAddr,
					hostName: cmd.hostName,
					task:     cmd.tsk,
					command:  cmd.cmd.Name,
					env:      cmd.cmd.Environment,
				}

				processedRegister := tmpl.apply(registerVar)
				t.Logf("Original register var: %s, Processed: %s, Key: %s", registerVar, processedRegister, key)

				// if the key matches (either original or templated), register it
				if key == registerVar || key == processedRegister {
					resp.registered[key] = val
					break
				}
			}
		}

		assert.Equal(t, "env-value", resp.vars["VAR_production"])
		assert.Equal(t, "env-value", resp.registered["VAR_production"], "Should match processed register var with ENV substitution")
	})
}
