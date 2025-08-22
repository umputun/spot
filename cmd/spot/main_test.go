package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/umputun/spot/pkg/config"
)

func Test_main(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	t.Run("with system shell set", func(*testing.T) {
		args := []string{"spot", "--dbg", "--playbook=testdata/conf-local.yml", "--user=test",
			"--key=testdata/test_ssh_key", "--target=" + hostAndPort, "-vv"}
		os.Args = args
		main()
	})

	t.Run("with system shell not set", func(t *testing.T) {
		args := []string{"spot", "--dbg", "--playbook=testdata/conf-local.yml", "--user=test",
			"--key=testdata/test_ssh_key", "--target=" + hostAndPort, "--verbose"}
		os.Args = args
		err := os.Setenv("SHELL", "")
		require.NoError(t, err)
		main()
	})

	t.Run("with system shell set, without verbose and debug", func(*testing.T) {
		args := []string{"spot", "--playbook=testdata/conf-local.yml", "--user=test",
			"--key=testdata/test_ssh_key", "--target=" + hostAndPort}
		os.Args = args
		main()
	})

}

func Test_runCompleted(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	t.Run("normal run", func(t *testing.T) {
		opts := options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{hostAndPort},
			Only:         []string{"wait"},
			SecretsProvider: SecretsProvider{
				Provider: "spot",
				Conn:     "testdata/test-secrets.db",
				Key:      "1234567890",
			},
			Dbg: true,
		}
		st := time.Now()
		logOut := captureStdout(t, func() {
			err := run(opts)
			require.NoError(t, err)
		})
		t.Log("out\n", logOut)
		assert.True(t, time.Since(st) >= 1*time.Second)
	})

	t.Run("normal run with secrets", func(t *testing.T) {
		opts := options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{hostAndPort},
			Only:         []string{"copy configuration", "some command"},
			SecretsProvider: SecretsProvider{
				Provider: "spot",
				Conn:     "testdata/test-secrets.db",
				Key:      "1234567890",
			},
			Dbg: true,
		}
		logOut := captureStdout(t, func() {
			err := run(opts)
			require.NoError(t, err)
		})
		t.Log("out\n", logOut)
		assert.Contains(t, logOut, "> secrets: **** ****")
		assert.Contains(t, logOut, "> secrets md5: a7ae287dce96d9dad168f42fb87518b2")
		assert.NotContains(t, logOut, "secval")
	})

	t.Run("dry run", func(t *testing.T) {
		opts := options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{hostAndPort},
			Only:         []string{"wait"},
			Dry:          true,
			SecretsProvider: SecretsProvider{
				Provider: "spot",
				Conn:     "testdata/test-secrets.db",
				Key:      "1234567890",
			},
			Dbg: true,
		}
		st := time.Now()
		logOut := captureStdout(t, func() {
			err := run(opts)
			require.NoError(t, err)
		})
		t.Log("out\n", logOut)
		assert.True(t, time.Since(st) < 1*time.Second)
		assert.NotContains(t, logOut, "secval")
	})

	t.Run("run with dynamic targets", func(t *testing.T) {
		opts := options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf-dynamic.yml",
			SecretsProvider: SecretsProvider{
				Provider: "spot",
				Conn:     "testdata/test-secrets.db",
				Key:      "1234567890",
			},
			Env: map[string]string{
				"hostAndPort": hostAndPort,
			},
			Dbg: true,
		}
		logOut := captureStdout(t, func() {
			err := run(opts)
			require.NoError(t, err)
		})
		t.Log("out\n", logOut)
	})

	t.Run("run with registered variables", func(t *testing.T) {
		opts := options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf.yml",
			TaskNames:    []string{"set_register_var", "use_register_var"},
			Targets:      []string{hostAndPort},
			SecretsProvider: SecretsProvider{
				Provider: "spot",
				Conn:     "testdata/test-secrets.db",
				Key:      "1234567890",
			},
			Dbg:     true,
			Verbose: []bool{true},
		}
		logOut := captureStdout(t, func() {
			err := run(opts)
			require.NoError(t, err)
		})
		t.Log("out\n", logOut)
		assert.Contains(t, logOut, " > setvar len=13")
		assert.Contains(t, logOut, " > len: 13")
	})
}

func Test_runCompletedSimplePlaybook(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf-simple.yml",
		Targets:      []string{hostAndPort},
		Only:         []string{"wait"},
		Dbg:          true,
	}
	st := time.Now()
	logOut := captureStdout(t, func() {
		err := run(opts)
		require.NoError(t, err)
	})
	t.Log("out\n", logOut)
	assert.True(t, time.Since(st) >= 1*time.Second)
}

func Test_runAdhoc(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser: "test",
		SSHKey:  "testdata/test_ssh_key",
		Targets: []string{hostAndPort},
		Dbg:     true,
	}
	opts.PositionalArgs.AdHocCmd = "echo hello"
	logOut := captureStdout(t, func() {
		err := run(opts)
		require.NoError(t, err)
	})
	t.Log("out\n", logOut)
}

func Test_runCompletedSeveralTasks(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf3.yml",
		TaskNames:    []string{"task1", "task2"},
		Targets:      []string{hostAndPort},
		Dbg:          true,
	}

	st := time.Now()
	logOut := captureStdout(t, func() {
		err := run(opts)
		require.NoError(t, err)
	})
	t.Log("out: ", logOut)
	assert.True(t, time.Since(st) >= 1*time.Second)
	assert.Contains(t, logOut, "task 1 command 1")
	assert.Contains(t, logOut, "task 2 command 1")
	assert.NotContains(t, logOut, "task 3 command 1")
}

func Test_runCompletedAllTasks(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf2.yml",
		Targets:      []string{hostAndPort},
		Dbg:          true,
	}

	st := time.Now()
	logOut := captureStdout(t, func() {
		err := run(opts)
		require.NoError(t, err)
	})
	t.Log("out: ", logOut)

	assert.True(t, time.Since(st) >= 1*time.Second)
	assert.Contains(t, logOut, "task1")
	assert.Contains(t, logOut, "task2")
	assert.Contains(t, logOut, "all good, 123")
	assert.Contains(t, logOut, "good command 2")
	assert.Contains(t, logOut, "all good, 123 - foo-val bar-val")

}

func Test_runCanceled(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf.yml",
		TaskNames:    []string{"task1"},
		Targets:      []string{hostAndPort},
		Only:         []string{"wait"},
		SecretsProvider: SecretsProvider{
			Provider: "spot",
			Conn:     "testdata/test-secrets.db",
			Key:      "1234567890",
		},
		Dbg: true,
	}
	setupLog(true)

	errCh := make(chan error, 1)
	go func() {
		err := run(opts)
		errCh <- err
	}()

	// give the command time to start executing
	time.Sleep(700 * time.Millisecond)

	// send interrupt signal
	p, _ := os.FindProcess(os.Getpid())
	p.Signal(os.Interrupt)

	// wait for the error with timeout
	select {
	case err := <-errCh:
		// accept cancellation-related errors
		if err != nil && (strings.Contains(err.Error(), "remote command exited") ||
			strings.Contains(err.Error(), "can't connect to") ||
			strings.Contains(err.Error(), "ssh: handshake failed") ||
			strings.Contains(err.Error(), "context canceled") ||
			strings.Contains(err.Error(), "canceled:")) {
			// test passes with any of these errors
			return
		}
		t.Errorf("unexpected error: %v", err)
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for cancellation")
	}
}

func Test_runFailed(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf-local-failed.yml",
		TaskNames:    []string{"default"},
		Targets:      []string{hostAndPort},
	}
	setupLog(true)
	err := run(opts)
	assert.ErrorContains(t, err, `failed command "show content"`)
}

func Test_runNoConfig(t *testing.T) {
	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf-not-found.yml",
		TaskNames:    []string{"task1"},
		Targets:      []string{"localhost"},
		Only:         []string{"wait"},
	}
	setupLog(true)
	err := run(opts)
	require.ErrorContains(t, err, "can't get playbook \"testdata/conf-not-found.yml\"")
}

func Test_runGen_goTmplFile(t *testing.T) {
	outputFilename := filepath.Join(os.TempDir(), "test_gen_output.data")
	testCases := []struct {
		name string
		opts options
	}{{
		name: "generate output for a task",
		opts: options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{"dev"},
			SecretsProvider: SecretsProvider{
				Provider: "spot",
				Conn:     "testdata/test-secrets.db",
				Key:      "1234567890",
			},
			Inventory:   "testdata/inventory.yml",
			GenEnable:   true,
			GenOutput:   outputFilename,
			GenTemplate: "testdata/gen.tmpl",
		}},
		{
			name: "generate output for multiple tasks",
			opts: options{
				SSHUser:      "test",
				SSHKey:       "testdata/test_ssh_key",
				PlaybookFile: "testdata/conf.yml",
				TaskNames:    []string{"task1", "failed_task"},
				Targets:      []string{"dev"},
				SecretsProvider: SecretsProvider{
					Provider: "spot",
					Conn:     "testdata/test-secrets.db",
					Key:      "1234567890",
				},
				Inventory:   "testdata/inventory.yml",
				GenEnable:   true,
				GenOutput:   outputFilename,
				GenTemplate: "testdata/gen.tmpl",
			},
		},
	}

	defer os.Remove(outputFilename)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(tc.opts.GenOutput)

			setupLog(true)
			err := run(tc.opts)
			require.NoError(t, err)

			res, err := os.ReadFile(tc.opts.GenOutput)
			require.NoError(t, err)
			exp := "\n" + `"Name": "dev1", "Host": "dev1.umputun.dev", "Port": 22, "User": "test","Tags": []` + "\n" +
				`"Name": "dev2", "Host": "dev2.umputun.dev", "Port": 22, "User": "test","Tags": []`
			assert.Equal(t, exp, string(res), "expected output")
		})
	}
}

func TestMakeRunnerTempDir(t *testing.T) {
	tests := []struct {
		name       string
		opts       options
		playbook   *config.PlayBook
		wantTmpDir string
	}{
		{
			name: "command line temp dir",
			opts: options{
				SSHTempDir: "/tmp/custom/cmd",
				SSHKey:     "testdata/test_ssh_key", // add test SSH key
			},
			playbook:   &config.PlayBook{},
			wantTmpDir: "/tmp/custom/cmd",
		},
		{
			name: "playbook temp dir",
			opts: options{
				SSHKey: "testdata/test_ssh_key", // add test SSH key
			},
			playbook: &config.PlayBook{
				SSHTempDir: "/tmp/custom/playbook",
			},
			wantTmpDir: "/tmp/custom/playbook",
		},
		{
			name: "no temp dir",
			opts: options{
				SSHKey: "testdata/test_ssh_key", // add test SSH key
			},
			playbook:   &config.PlayBook{},
			wantTmpDir: "",
		},
		{
			name: "command line overrides playbook",
			opts: options{
				SSHTempDir: "/tmp/custom/cmd",
				SSHKey:     "testdata/test_ssh_key", // add test SSH key
			},
			playbook: &config.PlayBook{
				SSHTempDir: "/tmp/custom/playbook",
			},
			wantTmpDir: "/tmp/custom/cmd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.opts.SSHTempDir == "" && tt.playbook.SSHTempDir != "" {
				tt.opts.SSHTempDir = tt.playbook.SSHTempDir
			}
			r, err := makeRunner(tt.opts, tt.playbook)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTmpDir, r.SSHTempDir)
		})
	}
}

func TestRunWithCustomTempDir(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		SSHTempDir:   "/tmp/custom/spot", // updated path
		PlaybookFile: "testdata/conf2.yml",
		TaskNames:    []string{"task1"},
		Targets:      []string{hostAndPort},
		Only:         []string{"some command"},
		Dbg:          true,
	}

	logOut := captureStdout(t, func() {
		err := run(opts)
		require.NoError(t, err)
	})
	t.Log("out\n", logOut)

	assert.Contains(t, logOut, "/tmp/custom/spot/.spot-")
	assert.Contains(t, logOut, "deleted recursively /tmp/custom/spot/.spot-")
}

func Test_connectFailed(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "bad_user",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf.yml",
		TaskNames:    []string{"task1"},
		Targets:      []string{hostAndPort},
		SecretsProvider: SecretsProvider{
			Provider: "spot",
			Conn:     "testdata/test-secrets.db",
			Key:      "1234567890",
		},
	}
	setupLog(true)
	err := run(opts)
	assert.ErrorContains(t, err, `ssh: unable to authenticate`)
}

func runSSHAgent(t *testing.T, keyPath string) (stop func()) {
	t.Helper()

	path := fmt.Sprintf("/tmp/%s.sock", uuid.New())
	sock, err := net.Listen("unix", path)
	require.NoError(t, err)

	a := agent.NewKeyring()

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, e := sock.Accept()
			if e != nil {
				return
			}

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				<-stopCh
				c.Close()
			}(c)

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				agent.ServeAgent(a, c)
			}(c)
		}
	}()

	prevAgent, resetAgent := os.LookupEnv("SSH_AUTH_SOCK")
	os.Setenv("SSH_AUTH_SOCK", sock.Addr().String())

	keyBytes, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	signer, err := ssh.ParseRawPrivateKey(keyBytes)
	require.NoError(t, err)

	err = a.Add(agent.AddedKey{PrivateKey: signer})
	require.NoError(t, err)

	return func() {
		if resetAgent {
			os.Setenv("SSH_AUTH_SOCK", prevAgent)
		} else {
			os.Unsetenv("SSH_AUTH_SOCK")
		}

		sock.Close()
		close(stopCh)
		wg.Wait()
	}
}

func getKeyFingerprint(t *testing.T, keyPath string) string {
	t.Helper()

	keyBytes, err := os.ReadFile(keyPath)
	require.NoError(t, err)

	k, err := ssh.ParsePrivateKey(keyBytes)
	require.NoError(t, err)

	return ssh.FingerprintSHA256(k.PublicKey())
}

func Test_sshAgentForwarding(t *testing.T) {
	stop := runSSHAgent(t, "testdata/test_ssh_key")
	defer stop()

	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:         "test",
		SSHKey:          "testdata/test_ssh_key",
		ForwardSSHAgent: true,
		Targets:         []string{hostAndPort},
		Dbg:             true,
	}

	cmd := fmt.Sprintf("ssh-add -l | awk \"{ print \\$2 }\" > f1; echo %q > f2; diff f1 f2",
		getKeyFingerprint(t, "testdata/test_ssh_key"))

	opts.PositionalArgs.AdHocCmd = cmd

	setupLog(true)
	err := run(opts)
	require.NoError(t, err)
}

func Test_sshUserAndKey(t *testing.T) {

	osUser, err := user.Current()
	require.NoError(t, err)

	testCases := []struct {
		name         string
		opts         options
		conf         config.PlayBook
		expectedUser string
		expectedKey  string
	}{
		{
			name: "from playbook",
			opts: options{},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "default_key",
				Tasks:  []config.Task{},
			},
			expectedUser: "default_user",
			expectedKey:  "default_key",
		},
		{
			name: "command line overrides all",
			opts: options{
				TaskNames: []string{"test_task"},
				SSHUser:   "cmd_user",
				SSHKey:    "cmd_key",
			},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "default_key",
				Tasks: []config.Task{
					{Name: "test_task", User: "task_user"},
				},
			},
			expectedUser: "cmd_user",
			expectedKey:  "cmd_key",
		},
		{
			name: "no user or key in playbook and no in command line",
			opts: options{
				TaskNames: []string{"test_task"},
			},
			conf: config.PlayBook{
				Tasks: []config.Task{
					{Name: "test_task"},
				},
			},
			expectedUser: osUser.Username,
			expectedKey:  filepath.Join(osUser.HomeDir, ".ssh", "id_rsa"),
		},
		{
			name: "SSHAgent set no key in playbook and command line",
			opts: options{
				TaskNames: []string{"test_task"},
				SSHUser:   "cmd_user",
				SSHAgent:  true,
			},
			conf: config.PlayBook{
				Tasks: []config.Task{
					{Name: "test_task"},
				},
			},
			expectedUser: "cmd_user",
			expectedKey:  "",
		},
		{
			name: "SSHAgent should override playbook SSH key",
			opts: options{
				TaskNames: []string{"test_task"},
				SSHUser:   "cmd_user",
				SSHAgent:  true, // SSH agent is enabled
			},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "/path/to/playbook/key", // playbook has SSH key defined
				Tasks: []config.Task{
					{Name: "test_task"},
				},
			},
			expectedUser: "cmd_user",
			expectedKey:  "", // should be empty when using SSH agent, not playbook's key
		},
		{
			name: "SSHAgent with no playbook key should not set default key",
			opts: options{
				TaskNames: []string{"test_task"},
				SSHUser:   "root",
				SSHAgent:  true, // SSH agent is enabled
			},
			conf: config.PlayBook{
				// no SSHKey defined in playbook - this is the reported issue case
				Tasks: []config.Task{
					{Name: "test_task"},
				},
			},
			expectedUser: "root",
			expectedKey:  "", // should be empty, not /root/.ssh/id_rsa
		},
		{
			name: "tilde expansion in key path",
			opts: options{
				TaskNames: []string{"test_task"},
				SSHUser:   "cmd_user",
				SSHKey:    "~/cmd_key",
			},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "~/default_key",
				Tasks: []config.Task{
					{Name: "test_task", User: "task_user"},
				},
			},
			expectedUser: "cmd_user",
			expectedKey:  fmt.Sprintf("%s/cmd_key", os.Getenv("HOME")),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := sshKey(tc.opts.SSHAgent, tc.opts.SSHKey, &tc.conf)
			require.NoError(t, err, "sshKey should not return an error")
			assert.Equal(t, tc.expectedKey, key, "key should match expected key")
			sshUser, err := sshUser(tc.opts.SSHUser, &tc.conf)
			require.NoError(t, err, "sshUser should not return an error")
			assert.Equal(t, tc.expectedUser, sshUser, "sshUser should match expected user")
		})
	}
}

type mockUserInfoProvider struct {
	user *user.User
	err  error
}

func (p *mockUserInfoProvider) Current() (*user.User, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.user, nil
}

func TestAdHocConf(t *testing.T) {

	t.Run("default SSH user and key", func(t *testing.T) {
		mockUser := &user.User{
			Username: "testuser",
			HomeDir:  "/tmp/test-home",
		}
		userProvider = &mockUserInfoProvider{user: mockUser}
		defer func() { userProvider = &defaultUserInfoProvider{} }()

		// call adHocConf with empty options and mock provider.
		opts := options{}
		pbook := &config.PlayBook{}
		pbook, err := setAdHocSSH(opts, pbook)
		require.NoError(t, err)

		assert.Equal(t, mockUser.Username, pbook.User)
		assert.Equal(t, filepath.Join(mockUser.HomeDir, ".ssh", "id_rsa"), pbook.SSHKey)
	})

	t.Run("provided SSH user and key", func(t *testing.T) {
		mockUser := &user.User{
			Username: "testuser",
			HomeDir:  "/tmp/test-home",
		}
		userProvider = &mockUserInfoProvider{user: mockUser}
		defer func() { userProvider = &defaultUserInfoProvider{} }()

		opts := options{
			SSHUser: "customuser",
			SSHKey:  "/tmp/custom-key",
		}
		pbook := &config.PlayBook{
			User:   "customuser",
			SSHKey: "/tmp/custom-key",
		}
		pbook, err := setAdHocSSH(opts, pbook)
		require.NoError(t, err)
		assert.Equal(t, opts.SSHUser, pbook.User)
		assert.Equal(t, opts.SSHKey, pbook.SSHKey)
	})

	t.Run("error getting current user", func(t *testing.T) {
		userProvider = &mockUserInfoProvider{err: errors.New("user error")}
		defer func() { userProvider = &defaultUserInfoProvider{} }()
		opts := options{}
		pbook := &config.PlayBook{}
		_, err := setAdHocSSH(opts, pbook)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't get current user")
	})

	t.Run("error getting current user when SSH key is empty", func(t *testing.T) {
		mockUser := &user.User{
			Username: "testuser",
			HomeDir:  "/tmp/test-home",
		}
		userProvider = &mockUserInfoProvider{user: mockUser, err: errors.New("user error")}
		defer func() { userProvider = &defaultUserInfoProvider{} }()

		opts := options{
			SSHUser: "customuser",
		}
		conf := &config.PlayBook{}
		_, err := setAdHocSSH(opts, conf)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't get current user")
	})
}

func TestExpandPath(t *testing.T) {
	testCases := []struct {
		name         string
		input        string
		expectedPath string
		expectedErr  error
	}{
		{"expand absolute path", "/home/testuser/myfile.txt", "/home/testuser/myfile.txt", nil},
		{"expand relative path", "testdata/myfile.txt", "testdata/myfile.txt", nil},
		{"expand tilde path", "~/myfile.txt", filepath.Join(os.Getenv("HOME"), "myfile.txt"), nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// replace the tilde with the home directory
			if strings.HasPrefix(tc.input, "~/") {
				tc.input = filepath.Join(os.Getenv("HOME"), tc.input[2:])
			}

			// call expandPath with the modified input
			p, err := expandPath(tc.input)
			require.Equal(t, tc.expectedErr, err)
			assert.Equal(t, tc.expectedPath, p)
		})
	}
}

func Test_formatErrorString(t *testing.T) {
	tbl := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:  "Two errors",
			input: `* can't run task "ad-hoc" for target "dev": 2 error(s) occurred: [0] {error 1}, [1] {error 2}`,
			output: `* can't run task "ad-hoc" for target "dev": 2 error(s) occurred:
   [0] error 1
   [1] error 2
`,
		},
		{
			name:   "Different string without errors",
			input:  `Different string without errors`,
			output: `Different string without errors`,
		},
		{
			name:  "No errors",
			input: `  * can't run task "ad-hoc" for target "dev": 0 error(s) occurred:`,
			output: `* can't run task "ad-hoc" for target "dev": 0 error(s) occurred:
`,
		},
		{
			name:  "One error",
			input: `* can't run task "ad-hoc" for target "dev": 1 error(s) occurred: [0] {error 1}`,
			output: `* can't run task "ad-hoc" for target "dev": 1 error(s) occurred:
   [0] error 1
`,
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.output, formatErrorString(tt.input))
		})
	}
}

func Test_targetsForTask(t *testing.T) {
	tests := []struct {
		name           string
		opts           options
		taskName       string
		conf           *config.PlayBook
		expectedResult []string
	}{
		{
			name: "non-default targets specified on command line",
			opts: options{
				Targets: []string{"target1", "target2"},
			},
			taskName:       "task1",
			conf:           &config.PlayBook{},
			expectedResult: []string{"target1", "target2"},
		},
		{
			name: "task with targets defined and default in command line",
			opts: options{
				Targets: []string{"default"},
			},
			taskName: "task1",
			conf: &config.PlayBook{
				Tasks: []config.Task{
					{
						Name:    "task1",
						Targets: []string{"target3", "target4"},
					},
				},
			},
			expectedResult: []string{"target3", "target4"},
		},
		{
			name: "task without targets defined",
			opts: options{
				Targets: []string{"default"},
			},
			taskName: "task2",
			conf: &config.PlayBook{
				Tasks: []config.Task{
					{
						Name:    "task1",
						Targets: []string{"target3", "target4"},
					},
					{
						Name: "task2",
					},
				},
			},
			expectedResult: []string{"default"},
		},
		{
			name: "default target with no task targets",
			opts: options{
				Targets: []string{"default"},
			},
			taskName:       "task3",
			conf:           &config.PlayBook{},
			expectedResult: []string{"default"},
		},
		{
			name: "non-existing task",
			opts: options{
				Targets: []string{"default"},
			},
			taskName: "task3",
			conf: &config.PlayBook{
				Tasks: []config.Task{
					{
						Name:    "task1",
						Targets: []string{"target3", "target4"},
					},
				},
			},
			expectedResult: []string{"default"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := targetsForTask(tc.opts.Targets, tc.taskName, tc.conf)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestEnvVars(t *testing.T) {
	os.Setenv("ENV_VAR", "envValue")
	defer os.Unsetenv("ENV_VAR")

	tests := []struct {
		name          string
		cliVars       map[string]string
		envFileData   string
		expectedVars  map[string]string
		expectedError bool
	}{
		{
			name: "override env file vars",
			cliVars: map[string]string{
				"key1": "cliValue1",
				"key2": "cliValue2",
			},
			envFileData: `vars:
  key1: fileValue1
  key2: fileValue2
  key3: fileValue3
  key4: "${ENV_VAR}"
  key5: "${ENV_VAR_NOT_FOUND}"
  key6: "$ENV_VAR"
`,
			expectedVars: map[string]string{
				"key1": "cliValue1",
				"key2": "cliValue2",
				"key3": "fileValue3",
				"key4": "envValue",
				"key5": "",
				"key6": "envValue",
			},
			expectedError: false,
		},
		{
			name: "no env file vars",
			cliVars: map[string]string{
				"key1": "cliValue1",
				"key2": "cliValue2",
			},
			envFileData: "",
			expectedVars: map[string]string{
				"key1": "cliValue1",
				"key2": "cliValue2",
			},
			expectedError: false,
		},
		{
			name: "system env var replacement",
			cliVars: map[string]string{
				"key1": "$ENV_VAR",
				"key2": "${ENV_VAR}",
				"key3": "${ENV_VAR_NOT_FOUND}",
			},
			envFileData: "",
			expectedVars: map[string]string{
				"key1": "envValue",
				"key2": "envValue",
				"key3": "",
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envFile := "/tmp/env-not-exist.yaml"
			if tt.envFileData != "" {
				file, err := os.CreateTemp("", "*.yaml")
				if err != nil {
					t.Fatalf("could not create temp file: %v", err)
				}
				defer os.Remove(file.Name()) // clean up

				if _, err = file.WriteString(tt.envFileData); err != nil {
					t.Fatalf("could not write to temp file: %v", err)
				}

				if err = file.Close(); err != nil {
					t.Fatalf("could not close temp file: %v", err)
				}
				envFile = file.Name()
			}

			actualVars, err := envVars(tt.cliVars, envFile)
			if err != nil && !tt.expectedError {
				t.Errorf("envVars() error = %v, expectedError %v", err, tt.expectedError)
				return
			}
			if err == nil && tt.expectedError {
				t.Errorf("envVars() expected error, got none")
				return
			}

			assert.Equal(t, tt.expectedVars, actualVars)
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
	require.NoError(t, err)

	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "2222")
	require.NoError(t, err)
	host, err := container.Host(ctx)
	require.NoError(t, err)
	return fmt.Sprintf("%s:%s", host, port.Port()), func() { container.Terminate(ctx) }
}

// captureStdout captures everything written to stdout within the function fn
func captureStdout(t *testing.T, fn func()) string {
	// keep backup of the real stdout
	old := os.Stdout
	defer func() { os.Stdout = old }()

	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()

	out, _ := io.ReadAll(r)
	return string(out)
}
