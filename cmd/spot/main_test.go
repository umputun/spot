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

	"github.com/umputun/spot/pkg/config"
)

func Test_main(t *testing.T) {
	hostAndPort, teardown := startTestContainer(t)
	defer teardown()

	args := []string{"simplotask", "--dbg", "--playbook=testdata/conf-local.yml", "--user=test", "--key=testdata/test_ssh_key", "--target=" + hostAndPort}
	os.Args = args
	main()
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
		}
		setupLog(true)
		st := time.Now()
		err := run(opts)
		require.NoError(t, err)
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
		}
		setupLog(true)
		outWriter := &bytes.Buffer{}
		log.SetOutput(outWriter)
		err := run(opts)
		require.NoError(t, err)
		t.Log("out\n", outWriter.String())
		assert.Contains(t, outWriter.String(), "> secrets: **** ****")
		assert.Contains(t, outWriter.String(), "> secrets md5: a7ae287dce96d9dad168f42fb87518b2")
		assert.NotContains(t, outWriter.String(), "secval")
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
		}
		setupLog(true)
		st := time.Now()
		err := run(opts)
		require.NoError(t, err)
		assert.True(t, time.Since(st) < 1*time.Second)
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
		}
		setupLog(true)
		err := run(opts)
		require.NoError(t, err)
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
		SSHKey:  "testdata/test_ssh_key",
		Targets: []string{hostAndPort},
	}
	opts.PositionalArgs.AdHocCmd = "echo hello"
	setupLog(true)
	err := run(opts)
	require.NoError(t, err)
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
	setupLog(true)

	wr := &bytes.Buffer{}
	log.SetOutput(wr)

	st := time.Now()
	err := run(opts)
	t.Log("dbg: ", wr.String())
	require.NoError(t, err)
	assert.True(t, time.Since(st) >= 1*time.Second)
	assert.Contains(t, wr.String(), "task 1 command 1")
	assert.Contains(t, wr.String(), "task 2 command 1")
	assert.NotContains(t, wr.String(), "task 3 command 1")
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
			key, err := sshKey(tc.opts.SSHKey, &tc.conf)
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
`,
			expectedVars: map[string]string{
				"key1": "cliValue1",
				"key2": "cliValue2",
				"key3": "fileValue3",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			envFile := "/tmp/env-not-exist.yaml"
			if tt.envFileData != "" {
				// create a temp file
				file, err := os.CreateTemp("", "*.yaml")
				if err != nil {
					t.Fatalf("could not create temp file: %v", err)
				}
				defer os.Remove(file.Name())

				// write data to temp file
				if _, err = file.WriteString(tt.envFileData); err != nil {
					t.Fatalf("could not write to temp file: %v", err)
				}

				// close file
				if err = file.Close(); err != nil {
					t.Fatalf("could not close temp file: %v", err)
				}
				envFile = file.Name()
			}
			// get environment variables
			actualVars, err := envVars(tt.cliVars, envFile)
			assert.Equal(t, tt.expectedError, err != nil)
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
