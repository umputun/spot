package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/spot/pkg/config/mocks"
	"github.com/umputun/spot/pkg/secrets"
)

func TestPlaybook_New(t *testing.T) {

	t.Run("good file", func(t *testing.T) {
		c, err := New("testdata/f1.yml", nil, nil)
		require.NoError(t, err)
		t.Logf("%+v", c)
		assert.Equal(t, 1, len(c.Tasks), "single task")
		assert.Equal(t, "umputun", c.User, "user")
		assert.Equal(t, "/bin/sh", c.Tasks[0].Commands[0].SSHShell, "ssh shell")
		expShell := os.Getenv("SHELL")
		if expShell == "" {
			expShell = "/bin/sh"
		}
		assert.Equal(t, expShell, c.Tasks[0].Commands[0].LocalShell, "local shell")

		tsk := c.Tasks[0]
		assert.Equal(t, 5, len(tsk.Commands), "5 commands")
		assert.Equal(t, "deploy-remark42", tsk.Name, "task name")
	})

	t.Run("good toml file", func(t *testing.T) {
		c, err := New("testdata/f1.toml", nil, nil)
		require.NoError(t, err)
		t.Logf("%+v", c)
		assert.Equal(t, 1, len(c.Tasks), "single task")
		assert.Equal(t, "umputun", c.User, "user")

		tsk := c.Tasks[0]
		assert.Equal(t, 5, len(tsk.Commands), "5 commands")
		assert.Equal(t, "deploy-remark42", tsk.Name, "task name")
	})

	t.Run("inventory from env", func(t *testing.T) {
		err := os.Setenv("SPOT_INVENTORY", "testdata/hosts-with-groups.yml")
		require.NoError(t, err)
		defer os.Unsetenv("SPOT_INVENTORY")

		c, err := New("testdata/f1.yml", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, c.inventory)
		assert.Len(t, c.inventory.Groups["all"], 7, "7 hosts in inventory")
		assert.Len(t, c.inventory.Groups["gr2"], 3, "3 hosts in gr2 group")
		assert.Equal(t, Destination{Name: "h5", Host: "h5.example.com", Port: 2233, User: "umputun"}, c.inventory.Groups["gr2"][0])
	})

	t.Run("inventory from playbook", func(t *testing.T) {
		c, err := New("testdata/playbook-with-inventory.yml", nil, nil)
		require.NoError(t, err)
		require.NotNil(t, c.inventory)
		assert.Len(t, c.inventory.Groups["all"], 5, "5 hosts in inventory")
		assert.Equal(t, Destination{Name: "h2", Host: "h2.example.com", Port: 2233, User: "umputun"},
			c.inventory.Groups["all"][0])
	})

	t.Run("inventory from overrides", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{Inventory: "testdata/hosts-with-groups.yml"}, nil)
		require.NoError(t, err)
		require.NotNil(t, c.inventory)
		assert.Len(t, c.inventory.Groups["all"], 7, "7 hosts in inventory")
		assert.Len(t, c.inventory.Groups["gr2"], 3, "3 hosts in gr2 group")
		assert.Equal(t, Destination{Name: "h5", Host: "h5.example.com", Port: 2233, User: "umputun"}, c.inventory.Groups["gr2"][0])
	})

	t.Run("inventory from overrides with env and playbook", func(t *testing.T) {
		err := os.Setenv("SPOT_INVENTORY", "testdata/inventory_env.yml")
		require.NoError(t, err)
		defer os.Unsetenv("SPOT_INVENTORY")

		c, err := New("testdata/playbook-with-inventory.yml", &Overrides{Inventory: "testdata/hosts-without-groups.yml"}, nil)
		require.NoError(t, err)
		require.NotNil(t, c.inventory)
		assert.Len(t, c.inventory.Groups["all"], 5, "5 hosts in inventory")
	})

	t.Run("adhoc mode", func(t *testing.T) {
		c, err := New("no-such-thing", &Overrides{AdHocCommand: "echo 123", User: "umputun"}, nil)
		require.NoError(t, err)
		assert.Equal(t, 0, len(c.Tasks), "empty config, no task just overrides")
	})

	t.Run("incorrectly formatted file", func(t *testing.T) {
		_, err := New("testdata/bad-format.yml", nil, nil)
		assert.ErrorContains(t, err, " can't unmarshal yaml playbook (full mode) testdata/bad-format.yml")
	})

	t.Run("bad field in the full playbook", func(t *testing.T) {
		_, err := New("testdata/bad-field.yml", nil, nil)
		assert.ErrorContains(t, err, " field tragets not found")
	})

	t.Run("bad field in the simplified playbook", func(t *testing.T) {
		_, err := New("testdata/bad-field-simple.yml", nil, nil)
		assert.ErrorContains(t, err, "can't unmarshal yaml playbook (simple mode) testdata/bad-field-simple.yml: failed to decode field \"copy\"", err.Error())
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := New("testdata/bad.yml", nil, nil)
		assert.EqualError(t, err, "open testdata/bad.yml: no such file or directory")
	})

	t.Run("missing task name", func(t *testing.T) {
		_, err := New("testdata/no-task-name.yml", nil, nil)
		require.ErrorContains(t, err, "task name is required")
	})

	t.Run("duplicate task name", func(t *testing.T) {
		_, err := New("testdata/dup-task-name.yml", nil, nil)
		require.ErrorContains(t, err, `duplicate task name "deploy"`)
	})

	t.Run("simple playbook with inventory", func(t *testing.T) {
		c, err := New("testdata/simple-playbook.yml", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(c.Tasks), "1 task")
		assert.Equal(t, "default", c.Tasks[0].Name, "task name")
		assert.Equal(t, 5, len(c.Tasks[0].Commands), "5 commands")

		assert.Equal(t, 1, len(c.Targets))
		assert.Equal(t, []string{"name1", "name2"}, c.Targets["default"].Names)
		assert.Equal(t, []Destination{{Host: "127.0.0.1", Port: 2222}}, c.Targets["default"].Hosts)
	})

	t.Run("simple playbook without inventory", func(t *testing.T) {
		c, err := New("testdata/simple-playbook-no-inventory.yml", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(c.Tasks), "1 task")
		assert.Equal(t, "default", c.Tasks[0].Name, "task name")
		assert.Equal(t, 5, len(c.Tasks[0].Commands), "5 commands")

		assert.Equal(t, 1, len(c.Targets))
		assert.Equal(t, 0, len(c.Targets["default"].Names))
		assert.Equal(t, []Destination{{Host: "name1", Port: 22}, {Host: "192.168.1.1", Port: 22},
			{Host: "127.0.0.1", Port: 2222}}, c.Targets["default"].Hosts)
	})

	t.Run("simple playbook with a single target set", func(t *testing.T) {
		c, err := New("testdata/simple-playbook-single-target.yml", nil, nil)
		require.NoError(t, err)
		require.Equal(t, 1, len(c.Tasks), "1 task")
		assert.Equal(t, "default", c.Tasks[0].Name, "task name")
		assert.Equal(t, 5, len(c.Tasks[0].Commands), "5 commands")

		assert.Equal(t, 1, len(c.Targets))
		assert.Equal(t, 0, len(c.Targets["default"].Names))
		assert.Equal(t, []Destination{{Host: "127.0.0.1", Port: 2222}}, c.Targets["default"].Hosts)
	})

	t.Run("playbook with secrets", func(t *testing.T) {
		secProvider := &mocks.SecretsProviderMock{
			GetFunc: func(key string) (string, error) {
				switch key {
				case "SEC1":
					return "VAL1", nil
				case "SEC2":
					return "VAL2", nil
				case "SEC11":
					return "VAL11", nil
				case "SEC12":
					return "VAL12", nil
				default:
					return "", fmt.Errorf("unknown secret key %q", key)
				}
			},
		}

		p, err := New("testdata/playbook-with-secrets.yml", nil, secProvider)
		require.NoError(t, err)
		assert.Equal(t, 1, len(p.Tasks), "1 task")
		assert.Equal(t, "deploy-remark42", p.Tasks[0].Name, "task name")
		assert.Equal(t, 5, len(p.Tasks[0].Commands), "5 commands")

		assert.Equal(t, map[string]string{"SEC1": "VAL1", "SEC11": "VAL11", "SEC12": "VAL12", "SEC2": "VAL2"},
			p.secrets, "Secrets map for all Secrets")

		tsk, err := p.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
		assert.Equal(t, "docker", tsk.Commands[4].Name)
		assert.Equal(t, map[string]string{"SEC1": "VAL1", "SEC11": "VAL11", "SEC12": "VAL12", "SEC2": "VAL2"}, tsk.Commands[4].Secrets)
		assert.Equal(t, []string{"VAL1", "VAL11", "VAL12", "VAL2"}, p.AllSecretValues())
	})

	t.Run("playbook with options", func(t *testing.T) {
		secProvider := &mocks.SecretsProviderMock{
			GetFunc: func(key string) (string, error) {
				switch key {
				case "SEC1":
					return "VAL1", nil
				case "SEC2":
					return "VAL2", nil
				case "SEC11":
					return "VAL11", nil
				case "SEC12":
					return "VAL12", nil
				default:
					return "", fmt.Errorf("unknown secret key %q", key)
				}
			},
		}

		p, err := New("testdata/playbook-with-task-opts.yml", nil, secProvider)
		require.NoError(t, err)
		assert.Equal(t, 1, len(p.Tasks), "1 task")
		assert.Equal(t, "deploy-remark42", p.Tasks[0].Name, "task name")
		assert.Equal(t, 5, len(p.Tasks[0].Commands), "5 commands")

		assert.Equal(t, map[string]string{"SEC1": "VAL1", "SEC11": "VAL11", "SEC12": "VAL12", "SEC2": "VAL2"},
			p.secrets, "Secrets map for all Secrets")

		tsk, err := p.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
		assert.Equal(t, "docker", tsk.Commands[4].Name)
		assert.EqualValues(t, map[string]string{"SEC1": "VAL1", "SEC11": "VAL11", "SEC12": "VAL12", "SEC2": "VAL2"}, tsk.Commands[4].Secrets)
		assert.Equal(t, []string{"VAL1", "VAL11", "VAL12", "VAL2"}, p.AllSecretValues())

		assert.Equal(t, CmdOptions{IgnoreErrors: true, NoAuto: true, SudoPassword: "TASK_PASS",
			Secrets: []string{"SEC11", "SEC12"}}, p.Tasks[0].Commands[0].Options)
		assert.Equal(t, CmdOptions{IgnoreErrors: true, NoAuto: true, SudoPassword: "TASK_PASS",
			Secrets: []string{"SEC11", "SEC12"}}, p.Tasks[0].Commands[1].Options)
		assert.Equal(t, CmdOptions{IgnoreErrors: true, NoAuto: true, Local: true, SudoPassword: "TASK_PASS",
			Secrets: []string{"SEC11", "SEC12"}}, p.Tasks[0].Commands[2].Options)
		assert.Equal(t, CmdOptions{IgnoreErrors: true, NoAuto: true, Local: false, Sudo: true, SudoPassword: "TASK_PASS",
			Secrets: []string{"SEC11", "SEC12"}}, p.Tasks[0].Commands[3].Options)
		assert.Equal(t, CmdOptions{IgnoreErrors: true, NoAuto: true, Local: false, Sudo: false, SudoPassword: "CMD_PASS",
			Secrets: []string{"SEC1", "SEC2", "SEC11", "SEC12"}}, p.Tasks[0].Commands[4].Options)
	})

	t.Run("playbook prohibited all target", func(t *testing.T) {
		_, err := New("testdata/playbook-with-all-group.yml", nil, nil)
		require.ErrorContains(t, err, "config testdata/playbook-with-all-group.yml is invalid: target \"all\" is reserved for all hosts")
	})

	t.Run("playbook with custom ssh shell set", func(t *testing.T) {
		c, err := New("testdata/with-ssh-shell.yml", nil, nil)
		require.NoError(t, err)
		t.Logf("%+v", c)
		assert.Equal(t, 1, len(c.Tasks), "single task")
		assert.Equal(t, "umputun", c.User, "user")
		assert.Equal(t, "/bin/bash", c.Tasks[0].Commands[0].SSHShell, "remote ssh shell")
		assert.Equal(t, "/bin/xxx", c.Tasks[0].Commands[0].LocalShell, "local local shell")

		tsk := c.Tasks[0]
		assert.Equal(t, 6, len(tsk.Commands), "5 commands")
		assert.Equal(t, "deploy-remark42", tsk.Name, "task name")
	})

	t.Run("playbook with custom shell overrides", func(t *testing.T) {
		c, err := New("testdata/with-ssh-shell.yml", &Overrides{SSHShell: "/bin/zsh"}, nil)
		require.NoError(t, err)
		t.Logf("%+v", c)
		assert.Equal(t, 1, len(c.Tasks), "single task")
		assert.Equal(t, "umputun", c.User, "user")
		assert.Equal(t, "/bin/zsh", c.Tasks[0].Commands[0].SSHShell, "remote ssh shell")

		tsk := c.Tasks[0]
		assert.Equal(t, 6, len(tsk.Commands), "5 commands")
		assert.Equal(t, "deploy-remark42", tsk.Name, "task name")
	})
}

func TestPlayBook_Task(t *testing.T) {

	t.Run("not-found", func(t *testing.T) {
		c, err := New("testdata/f1.yml", nil, nil)
		require.NoError(t, err)
		_, err = c.Task("no-such-task")
		assert.EqualError(t, err, `task "no-such-task" not found`)
	})

	t.Run("found", func(t *testing.T) {
		c, err := New("testdata/f1.yml", nil, nil)
		require.NoError(t, err)
		tsk, err := c.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
		assert.Equal(t, "deploy-remark42", tsk.Name)
	})

	t.Run("adhoc", func(t *testing.T) {
		c, err := New("", &Overrides{AdHocCommand: "echo 123", User: "umputun"}, nil)
		require.NoError(t, err)
		tsk, err := c.Task("ad-hoc")
		require.NoError(t, err)
		assert.Equal(t, 1, len(tsk.Commands))
		assert.Equal(t, "ad-hoc", tsk.Name)
		assert.Equal(t, "echo 123", tsk.Commands[0].Script)
		assert.Equal(t, "/bin/sh", tsk.Commands[0].SSHShell)
	})

	t.Run("adhoc with custom shell", func(t *testing.T) {
		c, err := New("", &Overrides{AdHocCommand: "echo 123", User: "umputun", SSHShell: "/bin/zsh"}, nil)
		require.NoError(t, err)
		tsk, err := c.Task("ad-hoc")
		require.NoError(t, err)
		assert.Equal(t, 1, len(tsk.Commands))
		assert.Equal(t, "ad-hoc", tsk.Name)
		assert.Equal(t, "echo 123", tsk.Commands[0].Script)
		assert.Equal(t, "/bin/zsh", tsk.Commands[0].SSHShell)
	})
}

func TestPlayBook_TaskOverrideEnv(t *testing.T) {
	c, err := New("testdata/f1.yml", nil, nil)
	require.NoError(t, err)

	c.overrides = &Overrides{
		Environment: map[string]string{"k1": "v1", "k2": "v2"},
	}

	tsk, err := c.Task("deploy-remark42")
	require.NoError(t, err)
	assert.Equal(t, 5, len(tsk.Commands))
	assert.Equal(t, "deploy-remark42", tsk.Name)
	cmd := tsk.Commands[2]
	assert.Equal(t, "some local command", cmd.Name)
	assert.Equal(t, "v1", cmd.Environment["k1"])
	assert.Equal(t, "v2", cmd.Environment["k2"])
}

func TestTargetHosts(t *testing.T) {
	p := &PlayBook{
		User: "defaultuser",
		Targets: map[string]Target{
			"target1": {Name: "target1", Hosts: []Destination{{Host: "host1.example.com", Port: 22}}},
			"target2": {Name: "target2", Groups: []string{"group1"}},
			"target3": {Name: "target3", Groups: []string{"group1"},
				Hosts: []Destination{{Host: "host4.example.com", Port: 22, Name: "host4", Tags: []string{"tag4"}, User: "user4"}},
			},
			"target4":         {Name: "target4", Groups: []string{"group1"}, Names: []string{"host3"}},
			"target5":         {Name: "target5", Tags: []string{"tag1"}},
			"target-empty":    {Name: "target-empty", Groups: []string{"empty-group", "gpu-nodes"}},
			"targetwithproxy": {Name: "targetwithproxy", Hosts: []Destination{{Host: "host1.example.com", Port: 22, ProxyCommand: "ssh -W %h:%p gateway.example.com", ProxyCommandParsed: []string{"ssh", "-W", "%h:%p", "gateway.example.com"}}}, Tags: []string{"tagproxy"}},
		},
		inventory: &InventoryData{
			Groups: map[string][]Destination{
				"all": {
					{Host: "host1.example.com", Port: 22, User: "user1"},
					{Host: "host1.example.com", Port: 22, User: "user1"}, // intentionally duplicated
					{Host: "host2.example.com", Port: 22, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}},
					{Host: "host3.example.com", Port: 22, User: "defaultuser", Name: "host3", Tags: []string{"tag1", "tag2"}},
				},
				"group1": {
					{Host: "host2.example.com", Port: 2222, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}},
				},
				"empty-group": {}, // empty group
				"gpu-nodes":   {}, // empty group
			},
			Hosts: []Destination{
				{Host: "host3.example.com", Port: 22, Name: "host3", Tags: []string{"tag1", "tag2"}},
			},
		},
	}

	testCases := []struct {
		name        string
		targetName  string
		overrides   *Overrides
		expected    []Destination
		expectError bool
	}{
		{
			"target with hosts", "target1", nil,
			[]Destination{{Host: "host1.example.com", Port: 22, User: "defaultuser"}},
			false,
		},
		{
			"target with groups", "target2", nil,
			[]Destination{{Host: "host2.example.com", Port: 2222, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}}},
			false,
		},
		{
			"target with both hosts and group", "target3", nil,
			[]Destination{
				{Name: "host4", Host: "host4.example.com", Port: 22, User: "user4", Tags: []string{"tag4"}},
				{Host: "host2.example.com", Port: 2222, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}}},
			false,
		},
		{
			"target with both group and name", "target4", nil,
			[]Destination{
				{Name: "host3", Host: "host3.example.com", Port: 22, User: "defaultuser", Tags: []string{"tag1", "tag2"}},
				{Name: "host2", Host: "host2.example.com", Port: 2222, User: "defaultuser", Tags: []string{"tag1"}}},
			false,
		},
		{
			"target with tag", "target5", nil,
			[]Destination{
				{Name: "host2", Host: "host2.example.com", Port: 22, User: "defaultuser", Tags: []string{"tag1"}},
				{Name: "host3", Host: "host3.example.com", Port: 22, User: "defaultuser", Tags: []string{"tag1", "tag2"}}},
			false,
		},
		{
			"target with proxy", "targetwithproxy", nil,
			[]Destination{
				{Host: "host1.example.com", Port: 22, User: "defaultuser", ProxyCommand: "ssh -W %h:%p gateway.example.com", ProxyCommandParsed: []string{"ssh", "-W", "%h:%p", "gateway.example.com"}},
			},
			false,
		},
		{
			"target as group from inventory", "group1", nil,
			[]Destination{{Host: "host2.example.com", Port: 2222, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}}},
			false,
		},
		{
			"target as a tag from inventory", "tag2", nil,
			[]Destination{{Host: "host3.example.com", Port: 22, User: "defaultuser", Name: "host3", Tags: []string{"tag1", "tag2"}}},
			false,
		},
		{
			"target as a tag matching multiple from inventory", "tag1", nil,
			[]Destination{
				{Name: "host2", Host: "host2.example.com", Port: 22, User: "defaultuser", Tags: []string{"tag1"}},
				{Name: "host3", Host: "host3.example.com", Port: 22, User: "defaultuser", Tags: []string{"tag1", "tag2"}}},
			false,
		},
		{
			"target as single host by name from inventory", "host3", nil,
			[]Destination{{Host: "host3.example.com", Port: 22, User: "defaultuser", Name: "host3", Tags: []string{"tag1", "tag2"}}},
			false,
		},
		{
			"target as single host from inventory", "host3.example.com", nil,
			[]Destination{{Host: "host3.example.com", Port: 22, User: "defaultuser", Name: "host3", Tags: []string{"tag1", "tag2"}}},
			false,
		},
		{
			"target as single host with port", "host4.example.com:2222", nil,
			[]Destination{{Host: "host4.example.com", Name: "host4.example.com", Port: 2222, User: "defaultuser"}},
			false,
		},
		{
			"target as single host address", "host2.example.com", nil,
			[]Destination{{Host: "host2.example.com", Port: 22, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}}},
			false,
		},
		{
			"target as single host address with user", "user2@host5.example.com", nil,
			[]Destination{{Host: "host5.example.com", Name: "host5.example.com", Port: 22, User: "user2"}},
			false,
		},
		{
			"target as single host address, port and user", "user2@host5.example.com:2345", nil,
			[]Destination{{Host: "host5.example.com", Name: "host5.example.com", Port: 2345, User: "user2"}},
			false,
		},
		{"invalid host:port format", "host5.example.com:invalid", nil, nil, true},
		{"random host without a port", "host5.example.com", nil,
			[]Destination{{Host: "host5.example.com", Name: "host5.example.com", Port: 22, User: "defaultuser"}},
			false,
		},
		{
			"user override", "host3", &Overrides{User: "overriddenuser"},
			[]Destination{{Host: "host3.example.com", Port: 22, User: "overriddenuser", Name: "host3", Tags: []string{"tag1", "tag2"}}},
			false,
		},
		{
			"empty group direct targeting", "empty-group", nil,
			nil, // returns nil, no error
			false,
		},
		{
			"target with only empty groups", "target-empty", nil,
			nil,
			true, // should error when no hosts found
		},
	}

	// In the "normal" flow ProxyCommand is in configuration files, but in case cli argument `--target` passed that
	// represent hostname which is not exists in configuration file TargetHosts() will create "in memory" Destination,
	// and adhocProxyCommand is being used for that Destination.
	// This test checking how TargetHosts() extracts data from playbook so adhocProxyCommand is not important here
	// because the playbook structure will have original and parsed proxy command already in it.
	// It will be loaded during parsing playbook file.
	// One test case added with simulation that proxy command is configured.
	var adhocProxyCommand = ""

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p.overrides = tc.overrides

			res, err := p.TargetHosts(tc.targetName, adhocProxyCommand)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, res)
			}
		})
	}
}

func TestPlayBook_UpdateTasksTargets(t *testing.T) {
	tests := []struct {
		name     string
		playbook PlayBook
		vars     map[string]string
		expected PlayBook
	}{
		{
			name: "replace target variables",
			playbook: PlayBook{
				Tasks: []Task{{Targets: []string{"$target1", "target2"}}, {Targets: []string{"target3", "$target4"}}},
			},
			vars: map[string]string{"target1": "actualTarget1", "target4": "actualTarget4"},
			expected: PlayBook{
				Tasks: []Task{{Targets: []string{"actualTarget1", "target2"}}, {Targets: []string{"target3", "actualTarget4"}}},
			},
		},
		{
			name: "ignore single dollar sign",
			playbook: PlayBook{
				Tasks: []Task{{Targets: []string{"$"}}},
			},
			vars: map[string]string{"$": "actualTarget1"},
			expected: PlayBook{
				Tasks: []Task{{Targets: []string{"$"}}},
			},
		},
		{
			name: "ignore undefined variables",
			playbook: PlayBook{
				Tasks: []Task{{Targets: []string{"$undefined"}}},
			},
			vars: map[string]string{},
			expected: PlayBook{
				Tasks: []Task{{Targets: []string{}}},
			},
		},
		{
			name:     "playbook with no tasks",
			playbook: PlayBook{},
			vars: map[string]string{
				"target1": "actualTarget1",
			},
			expected: PlayBook{},
		},
		{
			name: "nil target variables",
			playbook: PlayBook{
				Tasks: []Task{{Targets: []string{"$target1", "target2"}}, {Targets: []string{"target3", "$target4"}}},
			},
			vars: nil,
			expected: PlayBook{
				Tasks: []Task{{Targets: []string{"target2"}}, {Targets: []string{"target3"}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.playbook.UpdateTasksTargets(tt.vars)
			assert.Equal(t, tt.expected, tt.playbook)
		})
	}
}

func TestPlayBook_loadInventory(t *testing.T) {
	// create temporary inventory files
	yamlData := []byte(`
groups:
  group1:
    - host: example.com
      port: 22
  group2:
    - host: another.com
hosts:
  - {host: one.example.com, port: 2222}
`)
	yamlFile, _ := os.CreateTemp("", "inventory-*.yaml")
	defer os.Remove(yamlFile.Name())
	_ = os.WriteFile(yamlFile.Name(), yamlData, 0o644)

	tomlData := []byte(`
[groups]
  group1 = [
    { host = "example.com", port = 22 },
  ]

  group2 = [
    { host = "another.com" },
  ]

[[hosts]]
  host = "one.example.com"
  port = 2222
`)
	tomlFile, _ := os.CreateTemp("", "inventory-*.toml")
	defer os.Remove(tomlFile.Name())
	_ = os.WriteFile(tomlFile.Name(), tomlData, 0o644)

	// create test HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Ext(r.URL.Path) {
		case ".bad":
			w.WriteHeader(http.StatusInternalServerError)
		case ".toml":
			http.ServeFile(w, r, tomlFile.Name())
		default:
			http.ServeFile(w, r, yamlFile.Name())
		}
	}))
	defer ts.Close()

	// create test cases
	testCases := []struct {
		name        string
		loc         string
		expectError bool
	}{
		{"load YAML from file", yamlFile.Name(), false},
		{"load YAML from URL", ts.URL + "/inventory.yaml", false},
		{"load YAML from URL without extension", ts.URL + "/inventory", false},
		{"load TOML from file", tomlFile.Name(), false},
		{"load TOML from URL", ts.URL + "/inventory.toml", false},
		{"load from URL with bad status", ts.URL + "/blah.bad", true},
		{"invalid URL", "http://not-a-valid-url", true},
		{"file not found", "nonexistent-file.yaml", true},
	}

	// run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &PlayBook{User: "testuser"}
			inv, err := p.loadInventory(tc.loc)

			if tc.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, inv)
			require.Len(t, inv.Groups, 3)
			require.Len(t, inv.Hosts, 1)

			allGroup := inv.Groups["all"]
			require.Len(t, allGroup, 3)
			assert.Equal(t, "another.com", allGroup[0].Host)
			assert.Equal(t, 22, allGroup[0].Port)
			assert.Equal(t, "example.com", allGroup[1].Host)
			assert.Equal(t, 22, allGroup[1].Port)
			assert.Equal(t, "one.example.com", allGroup[2].Host)
			assert.Equal(t, 2222, allGroup[2].Port)

			group1 := inv.Groups["group1"]
			require.Len(t, group1, 1)
			assert.Equal(t, "example.com", group1[0].Host)
			assert.Equal(t, 22, group1[0].Port)

			group2 := inv.Groups["group2"]
			require.Len(t, group2, 1)
			assert.Equal(t, "another.com", group2[0].Host)
			assert.Equal(t, 22, group2[0].Port)

			assert.Equal(t, "one.example.com", inv.Hosts[0].Host)
			assert.Equal(t, 2222, inv.Hosts[0].Port)
		})
	}
}

func TestPlayBook_loadInventoryWitAllGroup(t *testing.T) {
	// create temporary inventory files
	yamlData := []byte(`
groups:
  all:
    - host: example.com
      port: 22
  group2:
    - host: another.com
hosts:
  - {host: one.example.com, port: 2222}
`)

	yamlFile, _ := os.CreateTemp("", "inventory-*.yaml")
	defer os.Remove(yamlFile.Name())
	_ = os.WriteFile(yamlFile.Name(), yamlData, 0o644)

	p := &PlayBook{User: "testuser"}
	_, err := p.loadInventory(yamlFile.Name())
	require.EqualError(t, err, `group "all" is reserved for all hosts`)
}

func TestPlayBook_checkConfig(t *testing.T) {
	tbl := []struct {
		name        string
		playbook    PlayBook
		expectedErr string
	}{
		{
			name: "valid playbook",
			playbook: PlayBook{
				Tasks: []Task{
					{
						Name: "task1",
						Commands: []Cmd{
							{Script: "example_script"},
						},
					},
					{
						Name: "task2",
						Commands: []Cmd{
							{Delete: DeleteInternal{Location: "location"}},
						},
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "empty task name",
			playbook: PlayBook{
				Tasks: []Task{
					{Name: ""},
				},
			},
			expectedErr: "task name is required",
		},
		{
			name: "duplicate task name",
			playbook: PlayBook{
				Tasks: []Task{
					{Name: "task1"},
					{Name: "task1"},
				},
			},
			expectedErr: `duplicate task name "task1"`,
		},
		{
			name: "invalid command",
			playbook: PlayBook{
				Tasks: []Task{
					{
						Name: "task1",
						Commands: []Cmd{
							{Name: "c1", Script: "example_script", Delete: DeleteInternal{Location: "location"}},
						},
					},
				},
			},
			expectedErr: `task "task1" rejected, invalid command "c1": only one of [script, delete] is allowed`,
		},
		{
			name: "no commands",
			playbook: PlayBook{
				Tasks: []Task{
					{
						Name:     "task1",
						Commands: []Cmd{},
					},
				},
			},
			expectedErr: `task "task1" has no commands`,
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.playbook.checkConfig()
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPlayBook_loadSecrets(t *testing.T) {
	// mock secret provider
	secProvider := mocks.SecretsProviderMock{
		GetFunc: func(key string) (string, error) {
			if key == "secret1" {
				return "value1", nil
			}
			if key == "secret2" {
				return "value2", nil
			}
			return "", fmt.Errorf("unknown secret key %q", key)
		},
	}

	t.Run("successful secret loading", func(t *testing.T) {
		p := PlayBook{secretsProvider: &secProvider, Tasks: []Task{
			{Commands: []Cmd{{Options: CmdOptions{Secrets: []string{"secret1", "secret2"}}}}},
		}}
		err := p.loadSecrets()
		assert.NoError(t, err)
		assert.Equal(t, map[string]string{"secret1": "value1", "secret2": "value2"}, p.secrets)
	})

	t.Run("provider not set", func(t *testing.T) {
		p := PlayBook{Tasks: []Task{{Commands: []Cmd{{Options: CmdOptions{Secrets: []string{"secret1"}}}}}}}
		err := p.loadSecrets()
		assert.Error(t, err)
		assert.Equal(t, "secrets are defined in playbook (1 secrets), but provider is not set", err.Error())
	})

	t.Run("provider retrieval failure", func(t *testing.T) {
		p := PlayBook{secretsProvider: &secProvider, Tasks: []Task{{Commands: []Cmd{{Options: CmdOptions{Secrets: []string{"unknown"}}}}}}}
		err := p.loadSecrets()
		assert.Error(t, err)
		assert.Equal(t, "can't get secret \"unknown\" defined in task \"\", command \"\": unknown secret key \"unknown\"", err.Error())
	})
}

func TestPlayBook_AllTasks(t *testing.T) {
	p := PlayBook{Tasks: []Task{
		{Name: "task1", Targets: []string{"target1"}},
		{Name: "task2", Targets: []string{"target2", "target3"}},
	}}
	assert.Equal(t, 2, len(p.AllTasks()))
	assert.Equal(t, "task1", p.AllTasks()[0].Name)
	assert.Equal(t, "task2", p.AllTasks()[1].Name)
	assert.Equal(t, []string{"target1"}, p.AllTasks()[0].Targets)
	assert.Equal(t, []string{"target2", "target3"}, p.AllTasks()[1].Targets)
}

func TestPlayBook_SSHTempDir(t *testing.T) {
	tests := []struct {
		name        string
		playbookFn  string
		overrideDir string
		want        string
	}{
		{
			name:        "default temp dir",
			playbookFn:  "testdata/f1.yml",
			overrideDir: "",
			want:        "/tmp",
		},
		{
			name:        "temp dir from playbook",
			playbookFn:  "testdata/with-ssh-config.yml",
			overrideDir: "",
			want:        "/tmp/spot",
		},
		{
			name:        "temp dir from override",
			playbookFn:  "testdata/with-ssh-config.yml",
			overrideDir: "/custom/temp",
			want:        "/custom/temp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overrides := &Overrides{SSHTempDir: tt.overrideDir}
			pbook, err := New(tt.playbookFn, overrides, &secrets.NoOpProvider{})
			require.NoError(t, err)
			assert.Equal(t, tt.want, pbook.sshTempDir())

			// verify each command gets correct temp dir set
			if pbook.Tasks != nil {
				for _, tsk := range pbook.Tasks {
					for _, cmd := range tsk.Commands {
						assert.Equal(t, tt.want, cmd.SSHTempDir, "task %q command %q", tsk.Name, cmd.Name)
					}
				}
			}
		})
	}
}
func TestParseProxyCommand(t *testing.T) {
	// Not all examples of proxy commands here are valid, do not use them without checking what they are doing.
	// Many of them will not open pipe to listen on stdin. Mostly these commands here to test
	// that parseProxyCommand() can handle tricky cases of parsing with quotes and other special characters.

	t.Run("basic command", func(t *testing.T) {
		result, err := parseProxyCommand("ssh -W %h:%p gateway.example.com")
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-W", "%h:%p", "gateway.example.com"}, result)
	})

	t.Run("empty string", func(t *testing.T) {
		result, err := parseProxyCommand("")
		require.NoError(t, err)
		assert.Equal(t, []string{}, result)
	})

	t.Run("whitespace only", func(t *testing.T) {
		result, err := parseProxyCommand("   \t\n  ")
		require.NoError(t, err)
		assert.Equal(t, []string{}, result)
	})

	t.Run("single quotes", func(t *testing.T) {
		result, err := parseProxyCommand("ssh -o 'ProxyCommand=nc %h %p' gateway")
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "ProxyCommand=nc %h %p", "gateway"}, result)
	})

	t.Run("double quotes", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o "ProxyCommand=nc %h %p" gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "ProxyCommand=nc %h %p", "gateway"}, result)
	})

	t.Run("mixed quotes", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o 'User="test user"' gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", `User="test user"`, "gateway"}, result)
	})

	t.Run("escaped spaces", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o ProxyCommand=nc\ %h\ %p gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "ProxyCommand=nc %h %p", "gateway"}, result)
	})

	t.Run("escaped quotes in double quotes", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o "ProxyCommand=nc \"quoted\" %h %p" gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", `ProxyCommand=nc "quoted" %h %p`, "gateway"}, result)
	})

	t.Run("escaped quotes in single quotes", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o 'ProxyCommand=nc '\''quoted'\'' %h %p' gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "ProxyCommand=nc 'quoted' %h %p", "gateway"}, result)
	})

	t.Run("escaped backslash", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o ProxyCommand=nc\\server gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", `ProxyCommand=nc\server`, "gateway"}, result)
	})

	t.Run("complex real world example", func(t *testing.T) {
		result, err := parseProxyCommand(`gcloud compute start-iap-tunnel myinstance 22 --local-host-port=localhost:2222 --zone=us-central1-a`)
		require.NoError(t, err)
		expected := []string{"gcloud", "compute", "start-iap-tunnel", "myinstance", "22", "--local-host-port=localhost:2222", "--zone=us-central1-a"}
		assert.Equal(t, expected, result)
	})

	t.Run("command with special characters", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o "ProxyCommand=nc -X 5 -x proxy.example.com:1080 %h %p" gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "ProxyCommand=nc -X 5 -x proxy.example.com:1080 %h %p", "gateway"}, result)
	})

	t.Run("multiple spaces", func(t *testing.T) {
		result, err := parseProxyCommand("ssh    -W     %h:%p   gateway")
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-W", "%h:%p", "gateway"}, result)
	})

	t.Run("unclosed single quote", func(t *testing.T) {
		_, err := parseProxyCommand("ssh -o 'ProxyCommand=nc %h %p gateway")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unclosed quote")
	})

	t.Run("unclosed double quote", func(t *testing.T) {
		_, err := parseProxyCommand(`ssh -o "ProxyCommand=nc %h %p gateway`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unclosed quote")
	})

	t.Run("empty argument in quotes", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o "" gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "", "gateway"}, result)
	})

	t.Run("only spaces in quotes", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o "   " gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", "   ", "gateway"}, result)
	})

	t.Run("nested quotes different types", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -o 'ProxyCommand="nc %h %p"' gateway`)
		require.NoError(t, err)
		assert.Equal(t, []string{"ssh", "-o", `ProxyCommand="nc %h %p"`, "gateway"}, result)
	})

	t.Run("ssh proxy command with identity file", func(t *testing.T) {
		result, err := parseProxyCommand(`ssh -i ~/.ssh/id_rsa -o "ProxyCommand=ssh -i ~/.ssh/gateway_key gateway nc %h %p" target`)
		require.NoError(t, err)
		expected := []string{"ssh", "-i", "~/.ssh/id_rsa", "-o", "ProxyCommand=ssh -i ~/.ssh/gateway_key gateway nc %h %p", "target"}
		assert.Equal(t, expected, result)
	})

	t.Run("curl with http connect proxy - valid ssh proxycommand", func(t *testing.T) {
		result, err := parseProxyCommand(`curl -s --proxy http://proxy.example.com:8080 --proxytunnel --connect-timeout 10 http://%h:%p`)
		require.NoError(t, err)
		expected := []string{"curl", "-s", "--proxy", "http://proxy.example.com:8080", "--proxytunnel", "--connect-timeout", "10", "http://%h:%p"}
		assert.Equal(t, expected, result)
	})

	t.Run("curl with authenticated proxy - valid ssh proxycommand", func(t *testing.T) {
		result, err := parseProxyCommand(`curl -s --proxy-user "user:pass" --proxy http://proxy.company.com:8080 --proxytunnel --connect-timeout 10 http://%h:%p`)
		require.NoError(t, err)
		expected := []string{"curl", "-s", "--proxy-user", "user:pass", "--proxy", "http://proxy.company.com:8080", "--proxytunnel", "--connect-timeout", "10", "http://%h:%p"}
		assert.Equal(t, expected, result)
	})

	t.Run("curl with escaped colon in proxy auth", func(t *testing.T) {
		result, err := parseProxyCommand(`curl -s --proxy-user user:pa\:ss --proxy http://proxy.com:8080 --proxytunnel http://%h:%p`)
		require.NoError(t, err)
		expected := []string{"curl", "-s", "--proxy-user", "user:pa:ss", "--proxy", "http://proxy.com:8080", "--proxytunnel", "http://%h:%p"}
		assert.Equal(t, expected, result)
	})

	t.Run("curl with protocol and port placeholders", func(t *testing.T) {
		result, err := parseProxyCommand(`curl -s --proxy "http://corporate-proxy.company.com:8080" --proxytunnel "http://%h:%p"`)
		require.NoError(t, err)
		expected := []string{"curl", "-s", "--proxy", "http://corporate-proxy.company.com:8080", "--proxytunnel", "http://%h:%p"}
		assert.Equal(t, expected, result)
	})
}
