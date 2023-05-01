package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/umputun/simplotask/app/config"
)

func Test_main(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	args := []string{"simplotask", "--dbg", "--file=runner/testdata/conf-local.yml", "--user=test", "--key=runner/testdata/test_ssh_key", "--target=" + hostAndPort}
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
	assert.True(t, time.Since(st) >= 5*time.Second)
}

func Test_runAdhoc(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	opts := options{
		SSHUser:  "test",
		SSHKey:   "runner/testdata/test_ssh_key",
		Targets:  []string{hostAndPort},
		AdHocCmd: "echo hello",
	}
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

	time.Sleep(3 * time.Second)
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
	assert.ErrorContains(t, err, `can't run command "show content"`)
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
	testCases := []struct {
		name         string
		opts         options
		conf         config.PlayBook
		expectedUser string
		expectedKey  string
	}{
		{
			name: "All defaults",
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
			name: "Task config overrides user",
			opts: options{
				TaskName: "test_task",
			},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "default_key",
				Tasks: []config.Task{
					{Name: "test_task", User: "task_user"},
				},
			},
			expectedUser: "task_user",
			expectedKey:  "default_key",
		},
		{
			name: "Command line overrides all",
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
			name: "Tilde expansion in key path",
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
			key := sshKey(tc.opts, &tc.conf)
			assert.Equal(t, tc.expectedKey, key, "key should match expected key")
		})
	}
}

type mockUserInfoProvider struct {
	user *user.User
}

func (p *mockUserInfoProvider) Current() (*user.User, error) {
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

		// Call adHocConf with custom SSH user and key options and mock provider.
		opts := options{
			SSHUser: "customuser",
			SSHKey:  "/tmp/custom-key",
		}
		conf := &config.PlayBook{
			User:   "customuser",
			SSHKey: "/tmp/custom-key",
		}
		err := adHocConf(opts, conf, mockProvider)

		// Check if the function correctly sets the custom user and the SSH key.
		require.NoError(t, err)
		assert.Equal(t, opts.SSHUser, conf.User)
		assert.Equal(t, opts.SSHKey, conf.SSHKey)
	})
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
