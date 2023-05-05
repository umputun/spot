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

	"github.com/umputun/spot/app/config/mocks"
)

func TestPlaybook_New(t *testing.T) {

	t.Run("good file", func(t *testing.T) {
		c, err := New("testdata/f1.yml", nil, nil)
		require.NoError(t, err)
		t.Logf("%+v", c)
		assert.Equal(t, 1, len(c.Tasks), "single task")
		assert.Equal(t, "umputun", c.User, "user")

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
		assert.ErrorContains(t, err, "can't unmarshal config testdata/bad-format.yml")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := New("testdata/bad.yml", nil, nil)
		assert.EqualError(t, err, "can't read config testdata/bad.yml: open testdata/bad.yml: no such file or directory")
	})

	t.Run("missing task name", func(t *testing.T) {
		_, err := New("testdata/no-task-name.yml", nil, nil)
		require.ErrorContains(t, err, "task name is required")
	})

	t.Run("duplicate task name", func(t *testing.T) {
		_, err := New("testdata/dup-task-name.yml", nil, nil)
		require.ErrorContains(t, err, `duplicate task name "deploy"`)
	})

	t.Run("simple playbook", func(t *testing.T) {
		c, err := New("testdata/simple-playbook.yml", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, len(c.Tasks), "1 task")
		assert.Equal(t, "default", c.Tasks[0].Name, "task name")
		assert.Equal(t, 5, len(c.Tasks[0].Commands), "5 commands")

		assert.Equal(t, 1, len(c.Targets))
		assert.Equal(t, []string{"name1", "name2"}, c.Targets["default"].Names)
		assert.Equal(t, []Destination{{Host: "127.0.0.1", Port: 2222}}, c.Targets["default"].Hosts)
	})

	t.Run("playbook with secrets", func(t *testing.T) {
		secProvider := &mocks.SecretProvider{
			GetFunc: func(key string) (string, error) {
				switch key {
				case "SEC1":
					return "VAL1", nil
				case "SEC2":
					return "VAL2", nil
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

		assert.Equal(t, map[string]string{"SEC1": "VAL1", "SEC2": "VAL2"}, p.secrets, "Secrets map for all Secrets")

		tsk, err := p.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
		assert.Equal(t, "docker", tsk.Commands[4].Name)
		assert.Equal(t, map[string]string{"SEC1": "VAL1", "SEC2": "VAL2"}, tsk.Commands[4].Secrets)
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
			"target4": {Name: "target4", Groups: []string{"group1"}, Names: []string{"host3"}},
			"target5": {Name: "target5", Tags: []string{"tag1"}},
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
			[]Destination{{Host: "host4.example.com", Port: 2222, User: "defaultuser"}},
			false,
		},
		{
			"target as single host address", "host2.example.com", nil,
			[]Destination{{Host: "host2.example.com", Port: 22, User: "defaultuser", Name: "host2", Tags: []string{"tag1"}}},
			false,
		},
		{"invalid host:port format", "host5.example.com:invalid", nil, nil, true},
		{"random host without a port", "host5.example.com", nil,
			[]Destination{{Host: "host5.example.com", Port: 22, User: "defaultuser"}},
			false,
		},
		{
			"user override", "host3", &Overrides{User: "overriddenuser"},
			[]Destination{{Host: "host3.example.com", Port: 22, User: "overriddenuser", Name: "host3", Tags: []string{"tag1", "tag2"}}},
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p.overrides = tc.overrides
			res, err := p.TargetHosts(tc.targetName)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, res)
			}
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
	secProvider := mocks.SecretProvider{
		GetFunc: func(key string) (string, error) {
			switch key {
			case "secret1":
				return "value1", nil
			case "secret2":
				return "value2", nil
			default:
				return "", fmt.Errorf("unknown secret key %q", key)
			}
		},
	}

	p := PlayBook{
		secretsProvider: &secProvider,
		Tasks: []Task{
			{
				Name: "task1",
				Commands: []Cmd{
					{
						Name: "cmd1",
						Options: CmdOptions{
							Secrets: []string{"secret1"},
						},
					},
					{
						Name: "cmd2",
						Options: CmdOptions{
							Secrets: []string{"secret2"},
						},
					},
					{
						Name: "cmd3",
						Options: CmdOptions{
							Secrets: []string{"secret2, secret3"},
						},
					},
				},
			},
		},
	}

	p.loadSecrets()
	assert.Equal(t, map[string]string{"secret1": "value1", "secret2": "value2"}, p.secrets)
}
