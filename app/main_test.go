package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/umputun/spot/app/config"
)

func Test_main(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	args := []string{"simplotask", "--dbg", "--playbook=runner/testdata/conf-local.yml", "--user=test", "--key=runner/testdata/test_ssh_key", "--target=" + hostAndPort}
	os.Args = args
	main()
}

func Test_runCompleted(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf.yml",
		TaskName:     "task1",
		Targets:      []string{hostAndPort},
		Only:         []string{"wait"},
	}
	setupLog(true)
	st := time.Now()
	err := run(opts)
	require.NoError(t, err)
	assert.True(t, time.Since(st) >= 1*time.Second)
}

func Test_runCompletedSimplePlaybook(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf-simple.yml",
		Targets:      []string{hostAndPort},
		Only:         []string{"wait"},
	}
	setupLog(true)
	st := time.Now()
	err := run(opts)
	require.NoError(t, err)
	assert.True(t, time.Since(st) >= 1*time.Second)
}

func Test_runAdhoc(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser: "test",
		SSHKey:  "runner/testdata/test_ssh_key",
		Targets: []string{hostAndPort},
	}
	opts.PositionalArgs.AdHocCmd = "echo hello"
	setupLog(true)
	err := run(opts)
	require.NoError(t, err)
}

func Test_runCompletedAllTasks(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf2.yml",
		Targets:      []string{hostAndPort},
		Dbg:          true,
	}
	setupLog(true)

	wr := &bytes.Buffer{}
	log.SetOutput(wr)

	st := time.Now()
	err := run(opts)
	t.Log("dbg: ", wr.String())
	require.NoError(t, err)
	assert.True(t, time.Since(st) >= 1*time.Second)
	assert.Contains(t, wr.String(), "task1")
	assert.Contains(t, wr.String(), "task2")
	assert.Contains(t, wr.String(), "all good, 123")
	assert.Contains(t, wr.String(), "good command 2")
	assert.Contains(t, wr.String(), "all good, 123 - foo-val bar-val")

}

func Test_runCanceled(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf.yml",
		TaskName:     "task1",
		Targets:      []string{hostAndPort},
		Only:         []string{"wait"},
	}
	setupLog(true)
	go func() {
		err := run(opts)
		assert.ErrorContains(t, err, "remote command exited")
	}()

	time.Sleep(500 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	signal.NotifyContext(ctx, os.Interrupt)
}

func Test_runFailed(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf-local-failed.yml",
		TaskName:     "default",
		Targets:      []string{hostAndPort},
	}
	setupLog(true)
	err := run(opts)
	assert.ErrorContains(t, err, `failed command "show content"`)
}

func Test_runNoConfig(t *testing.T) {
	opts := options{
		SSHUser:      "test",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf-not-found.yml",
		TaskName:     "task1",
		Targets:      []string{"localhost"},
		Only:         []string{"wait"},
	}
	setupLog(true)
	err := run(opts)
	require.ErrorContains(t, err, " can't read config runner/testdata/conf-not-found.yml")
}

func Test_connectFailed(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:      "bad_user",
		SSHKey:       "runner/testdata/test_ssh_key",
		PlaybookFile: "runner/testdata/conf.yml",
		TaskName:     "task1",
		Targets:      []string{hostAndPort},
	}
	setupLog(true)
	err := run(opts)
	assert.ErrorContains(t, err, `ssh: unable to authenticate`)
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
				TaskName: "test_task",
				SSHUser:  "cmd_user",
				SSHKey:   "cmd_key",
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
				TaskName: "test_task",
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
			name: "tilde expansion in key path",
			opts: options{
				TaskName: "test_task",
				SSHUser:  "cmd_user",
				SSHKey:   "~/cmd_key",
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
			key, err := sshKey(tc.opts, &tc.conf, &defaultUserInfoProvider{})
			require.NoError(t, err, "sshKey should not return an error")
			assert.Equal(t, tc.expectedKey, key, "key should match expected key")
			sshUser, err := sshUser(tc.opts, &tc.conf, &defaultUserInfoProvider{})
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
		mockProvider := &mockUserInfoProvider{user: mockUser}

		// call adHocConf with empty options and mock provider.
		opts := options{}
		conf := &config.PlayBook{}
		err := adHocConf(opts, conf, mockProvider)

		// check if the function correctly sets the user and the SSH key.
		require.NoError(t, err)
		assert.Equal(t, mockUser.Username, conf.User)
		assert.Equal(t, filepath.Join(mockUser.HomeDir, ".ssh", "id_rsa"), conf.SSHKey)
	})

	t.Run("provided SSH user and key", func(t *testing.T) {
		mockUser := &user.User{
			Username: "testuser",
			HomeDir:  "/tmp/test-home",
		}
		mockProvider := &mockUserInfoProvider{user: mockUser}

		// call adHocConf with custom SSH user and key options and mock provider.
		opts := options{
			SSHUser: "customuser",
			SSHKey:  "/tmp/custom-key",
		}
		conf := &config.PlayBook{
			User:   "customuser",
			SSHKey: "/tmp/custom-key",
		}
		err := adHocConf(opts, conf, mockProvider)

		// check if the function correctly sets the custom user and the SSH key.
		require.NoError(t, err)
		assert.Equal(t, opts.SSHUser, conf.User)
		assert.Equal(t, opts.SSHKey, conf.SSHKey)
	})

	t.Run("error getting current user", func(t *testing.T) {
		mockProvider := &mockUserInfoProvider{err: errors.New("user error")}

		// call adHocConf with empty options and mock provider that returns an error
		opts := options{}
		conf := &config.PlayBook{}
		err := adHocConf(opts, conf, mockProvider)

		// check if the function returns the expected error
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't get current user")
	})

	t.Run("error getting current user when SSH key is empty", func(t *testing.T) {
		mockUser := &user.User{
			Username: "testuser",
			HomeDir:  "/tmp/test-home",
		}
		mockProvider := &mockUserInfoProvider{user: mockUser, err: errors.New("user error")}

		// call adHocConf with custom SSH user and mock provider that returns an error
		opts := options{
			SSHUser: "customuser",
		}
		conf := &config.PlayBook{}
		err := adHocConf(opts, conf, mockProvider)

		// check if the function returns the expected error
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
			input: `* can't run task "ad-hoc" for target "dev": 0 error(s) occurred:`,
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

func startTestContainer(t *testing.T) (hostAndPort string, teardown func()) {
	t.Helper()
	ctx := context.Background()
	pubKey, err := os.ReadFile("runner/testdata/test_ssh_key.pub")
	require.NoError(t, err)

	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		WaitingFor:   wait.NewLogStrategy("done.").WithStartupTimeout(time.Second * 60),
		Files: []testcontainers.ContainerFile{
			{HostFilePath: "runner/testdata/test_ssh_key.pub", ContainerFilePath: "/authorized_key"},
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
