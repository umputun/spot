package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func Test_runCompletedWithProxy(t *testing.T) {

	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	t.Run("normal run", func(t *testing.T) {
		opts := options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf_proxy.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{targetHostAndPort},
			ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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
			PlaybookFile: "testdata/conf_proxy.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{targetHostAndPort},
			ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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
			PlaybookFile: "testdata/conf_proxy.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{targetHostAndPort},
			ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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

			// This env variable does not control spot directly like cli arguments, instead it is processed and injected
			// into playbook, check conf-dynamic.yml. Because value of `targetHostAndPort` does not match
			// name/group in playbook/inventory file, it will be treated as direct hostname and Playbook.TargetHosts() method will
			// inject AdhocProxyCommand
			Env: map[string]string{
				"hostAndPort": targetHostAndPort,
			},
			ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
			Dbg:          true,
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
			PlaybookFile: "testdata/conf_proxy.yml",
			TaskNames:    []string{"set_register_var", "use_register_var"},
			Targets:      []string{targetHostAndPort},
			ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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

func Test_runCompletedSimplePlaybookWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf-simple.yml",
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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

func Test_runAdhocWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
		Dbg:          true,
	}
	opts.PositionalArgs.AdHocCmd = "echo hello"
	logOut := captureStdout(t, func() {
		err := run(opts)
		require.NoError(t, err)
	})
	t.Log("out\n", logOut)
}

func Test_runCompletedSeveralTasksWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf3.yml",
		TaskNames:    []string{"task1", "task2"},
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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

func Test_runCompletedAllTasksWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf2.yml",
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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

func Test_runCanceledWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf_proxy.yml",
		TaskNames:    []string{"task1"},
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
		Only:         []string{"wait"},
		SecretsProvider: SecretsProvider{
			Provider: "spot",
			Conn:     "testdata/test-secrets.db",
			Key:      "1234567890",
		},
		Dbg: true,
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

func Test_runFailedWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "test",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf-local-failed.yml",
		TaskNames:    []string{"default"},
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
	}
	setupLog(true)
	err := run(opts)
	assert.ErrorContains(t, err, `failed command "show content"`)
}

// REVIEW TAG not running, tested in main_test.go
// func Test_runNoConfigWithProxy(t *testing.T) {
//	opts := options{
//		SSHUser:      "test",
//		SSHKey:       "testdata/test_ssh_key",
//		PlaybookFile: "testdata/conf-not-found.yml",
//		TaskNames:    []string{"task1"},
//		Targets:      []string{"localhost"},
//		Only:         []string{"wait"},
//	}
//	setupLog(true)
//	err := run(opts)
//	require.ErrorContains(t, err, "can't get playbook \"testdata/conf-not-found.yml\"")
//}

// REVIEW TAG
// To restrict side effects of ProxyCommand option code was added to the PlayBook.TargetHosts()
// but runGen() does not use it, so not sure if runGen() require changes or this test can be skipped
func Test_runGen_goTmplFileWithProxy(t *testing.T) {
	outputFilename := filepath.Join(os.TempDir(), "test_gen_output.data")
	testCases := []struct {
		name string
		opts options
	}{{
		name: "generate output for a task",
		opts: options{
			SSHUser:      "test",
			SSHKey:       "testdata/test_ssh_key",
			PlaybookFile: "testdata/conf_proxy.yml",
			TaskNames:    []string{"task1"},
			Targets:      []string{"dev"},
			ProxyCommand: "ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no",
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
				PlaybookFile: "testdata/conf_proxy.yml",
				TaskNames:    []string{"task1", "failed_task"},
				Targets:      []string{"dev"},
				ProxyCommand: "ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no",
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

func Test_connectFailedWithProxy(t *testing.T) {
	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:      "bad_user",
		SSHKey:       "testdata/test_ssh_key",
		PlaybookFile: "testdata/conf_proxy.yml",
		TaskNames:    []string{"task1"},
		Targets:      []string{targetHostAndPort},
		ProxyCommand: fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
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

func Test_sshAgentForwardingWithProxy(t *testing.T) {
	stop := runSSHAgent(t, "testdata/test_ssh_key")
	defer stop()

	_bastionHostAndPort, _, teardown := startTestContainerAndProxy(t)
	// TestContainers always return random ports, but we configured private Docker network to simulate proxy case and in that network
	// targetHostAndPort is always = target-host:2222, not using taget host info

	targetHostAndPort := "target-host:2222"
	bastionHostAndPort := strings.Split(_bastionHostAndPort, ":")

	log.Printf("[INFO] bastion: %v, target %v", bastionHostAndPort, targetHostAndPort)
	defer teardown()

	opts := options{
		SSHUser:         "test",
		SSHKey:          "testdata/test_ssh_key",
		ForwardSSHAgent: true,
		Targets:         []string{targetHostAndPort},
		ProxyCommand:    fmt.Sprintf("ssh -W %%h:%%p test@%s -p %s -i testdata/test_ssh_key -o StrictHostKeyChecking=no", bastionHostAndPort[0], bastionHostAndPort[1]),
		Dbg:             true,
	}

	cmd := fmt.Sprintf("ssh-add -l | awk \"{ print \\$2 }\" > f1; echo %q > f2; diff f1 f2",
		getKeyFingerprint(t, "testdata/test_ssh_key"))

	opts.PositionalArgs.AdHocCmd = cmd

	setupLog(true)
	err := run(opts)
	require.NoError(t, err)
}

func startTestContainerAndProxy(t *testing.T) (hostAndPort1, hostAndPort2 string, teardown func()) {
	t.Helper()
	ctx := context.Background()
	pubKey, err := os.ReadFile("testdata/test_ssh_key.pub")
	require.NoError(t, err)

	// Create a custom network
	networkName := "test-network"

	networkRequest := testcontainers.NetworkRequest{
		Name:           networkName,
		CheckDuplicate: true,
	}
	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: networkRequest,
	})
	require.NoError(t, err)

	// Define the container request
	containerRequest := func(name string) testcontainers.ContainerRequest {
		return testcontainers.ContainerRequest{
			AlwaysPullImage: true,
			Image:           "lscr.io/linuxserver/openssh-server:latest",
			ExposedPorts:    []string{"2222/tcp"},
			WaitingFor:      wait.NewLogStrategy("done.").WithStartupTimeout(time.Second * 60),
			Networks:        []string{networkName},
			NetworkAliases:  map[string][]string{networkName: {name}},
			Hostname:        name,
			Files: []testcontainers.ContainerFile{
				{HostFilePath: "testdata/test_ssh_key.pub", ContainerFilePath: "/authorized_key"},
			},
			Env: map[string]string{
				"PUBLIC_KEY":  string(pubKey),
				"USER_NAME":   "test",
				"TZ":          "Etc/UTC",
				"DOCKER_MODS": "linuxserver/mods:openssh-server-ssh-tunnel",
			},
		}
	}

	// Start the bastion container
	container1, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest("bastion-host"),
		Started:          true,
	})
	require.NoError(t, err)

	// Start the container with final ssh connection
	container2, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest("target-host"),
		Started:          true,
	})
	require.NoError(t, err)

	// Get the host and port for both containers
	host1, err := container1.Host(ctx)
	require.NoError(t, err)
	port1, err := container1.MappedPort(ctx, "2222")
	require.NoError(t, err)

	host2, err := container2.Host(ctx)
	require.NoError(t, err)
	port2, err := container2.MappedPort(ctx, "2222")
	require.NoError(t, err)

	teardown = func() {
		container1.Terminate(ctx)
		container2.Terminate(ctx)
		network.Remove(ctx)
	}

	return fmt.Sprintf("%s:%s", host1, port1.Port()), fmt.Sprintf("%s:%s", host2, port2.Port()), teardown
}
