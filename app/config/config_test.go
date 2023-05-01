package config

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {

	t.Run("good file", func(t *testing.T) {
		c, err := New("testdata/f1.yml", nil)
		require.NoError(t, err)
		t.Logf("%+v", c)
		assert.Equal(t, 1, len(c.Tasks), "single task")
		assert.Equal(t, "umputun", c.User, "user")

		tsk := c.Tasks[0]
		assert.Equal(t, 5, len(tsk.Commands), "5 commands")
		assert.Equal(t, "deploy-remark42", tsk.Name, "task name")
	})

	t.Run("incorrectly formatted file", func(t *testing.T) {
		_, err := New("testdata/bad-format.yml", nil)
		assert.ErrorContains(t, err, "can't unmarshal config testdata/bad-format.yml")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := New("testdata/bad.yml", nil)
		assert.EqualError(t, err, "can't read config testdata/bad.yml: open testdata/bad.yml: no such file or directory")
	})

	t.Run("missing task name", func(t *testing.T) {
		_, err := New("testdata/no-task-name.yml", nil)
		require.ErrorContains(t, err, "task name is required")
	})

	t.Run("duplicate task name", func(t *testing.T) {
		_, err := New("testdata/dup-task-name.yml", nil)
		require.ErrorContains(t, err, `duplicate task name "deploy"`)
	})
}

func TestPlayBook_Task(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
	require.NoError(t, err)

	t.Run("not-found", func(t *testing.T) {
		_, err = c.Task("no-such-task")
		assert.EqualError(t, err, `task "no-such-task" not found`)
	})

	t.Run("found", func(t *testing.T) {
		tsk, err := c.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
		assert.Equal(t, "deploy-remark42", tsk.Name)
	})
}

func TestPlayBook_TaskOverrideEnv(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
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

func TestCmd_GetScript(t *testing.T) {
	testCases := []struct {
		name             string
		cmd              *Cmd
		expectedScript   string
		expectedContents []string
	}{
		{
			name: "single line command without environment variables",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
			},
			expectedScript:   `sh -c "echo 'Hello, World!'"`,
			expectedContents: nil,
		},
		{
			name: "multiline command without environment variables",
			cmd: &Cmd{
				Script: `echo 'Hello, World!'
echo 'Goodbye, World!'`,
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"echo 'Hello, World!'",
				"echo 'Goodbye, World!'",
			},
		},
		{
			name: "single line command with environment variables",
			cmd: &Cmd{
				Script: "echo $GREETING",
				Environment: map[string]string{
					"GREETING": "Hello, World!",
				},
			},
			expectedScript:   `sh -c "GREETING='Hello, World!' echo $GREETING"`,
			expectedContents: nil,
		},
		{
			name: "multiline command with environment variables",
			cmd: &Cmd{
				Script: `echo $GREETING
echo $FAREWELL`,
				Environment: map[string]string{
					"GREETING": "Hello, World!",
					"FAREWELL": "Goodbye, World!",
				},
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"export FAREWELL='Goodbye, World!'",
				"export GREETING='Hello, World!'",
				"echo $GREETING",
				"echo $FAREWELL",
			},
		},
		{
			name: "multiline command with comments",
			cmd: &Cmd{
				Script: `# This is a comment
echo 'Hello, World!' # Inline comment
# Another comment
echo 'Goodbye, World!'`,
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"echo 'Hello, World!'",
				"echo 'Goodbye, World!'",
			},
		},
		{
			name: "Empty script",
			cmd: &Cmd{
				Script: "",
			},
			expectedScript:   "",
			expectedContents: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			script, reader := tc.cmd.GetScript()
			assert.Equal(t, tc.expectedScript, script)

			if reader != nil {
				contents, err := io.ReadAll(reader)
				assert.NoError(t, err)
				lines := strings.Split(string(contents), "\n")
				assert.Equal(t, tc.expectedContents, lines[:len(lines)-1])
			} else {
				assert.Nil(t, tc.expectedContents)
			}
		})
	}
}

func TestCmd_getScriptCommand(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")

	t.Run("script", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[3]
		assert.Equal(t, "git", cmd.Name, "name")
		res := cmd.getScriptCommand()
		assert.Equal(t, `sh -c "git clone https://example.com/remark42.git /srv || true; cd /srv; git pull"`, res)
	})

	t.Run("no-script", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[1]
		assert.Equal(t, "copy configuration", cmd.Name)
		res := cmd.getScriptCommand()
		assert.Equal(t, "", res)
	})

	t.Run("script with env", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[4]
		assert.Equal(t, "docker", cmd.Name)
		res := cmd.getScriptCommand()
		assert.Equal(t, `sh -c "BAR='qux' FOO='bar' docker pull umputun/remark42:latest; docker stop remark42 || true; docker rm remark42 || true; docker run -d --name remark42 -p 8080:8080 umputun/remark42:latest"`, res)
	})
}

func TestCmd_getScriptFile(t *testing.T) {
	tests := []struct {
		name     string
		cmd      *Cmd
		expected string
	}{
		{
			name: "no environment variables",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
			},
			expected: "#!/bin/sh\nset -e\necho 'Hello, World!'\n",
		},
		{
			name: "with one environment variable",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
				Environment: map[string]string{
					"VAR1": "value1",
				},
			},
			expected: "#!/bin/sh\nset -e\nexport VAR1='value1'\necho 'Hello, World!'\n",
		},
		{
			name: "with multiple environment variables",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
				Environment: map[string]string{
					"VAR1": "value1",
					"VAR2": "value2",
				},
			},
			expected: "#!/bin/sh\nset -e\nexport VAR1='value1'\nexport VAR2='value2'\necho 'Hello, World!'\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := tt.cmd.getScriptFile()
			scriptContentBytes, err := io.ReadAll(reader)
			assert.NoError(t, err)
			scriptContent := string(scriptContentBytes)
			assert.Equal(t, tt.expected, scriptContent)
		})
	}
}

func TestPlaybook_TargetHosts(t *testing.T) {
	p := &PlayBook{
		User: "default_user",
		Targets: map[string]Target{
			"target1": {
				Hosts: []Destination{
					{Host: "host1", Port: 22, User: "user1"},
					{Host: "host2", Port: 2222},
					{Host: "host3", Name: "host3_name", Port: 2020, User: "user3"},
				},
			},
			"target2": {
				InventoryFile: Inventory{Location: "testdata/hosts-with-groups.yml", Groups: []string{"gr1"}},
			},
			"target3": {},
			"target4": {
				InventoryFile: Inventory{Location: "testdata/hosts-with-groups.yml"},
			},
		},
	}

	tests := []struct {
		name       string
		targetName string
		overrides  *Overrides
		want       []Destination
		wantErr    bool
	}{
		{
			name:       "target from config",
			targetName: "target1",
			want: []Destination{
				{Host: "host1", Port: 22, User: "user1"},
				{Host: "host2", Port: 2222, User: "default_user"},
				{Name: "host3_name", Host: "host3", Port: 2020, User: "user3"},
			},
			wantErr: false,
		},
		{
			name:       "overrides target hosts from inventory, name match",
			targetName: "target4",
			overrides: &Overrides{
				FilterHosts: []string{"h6", "h5"},
			},
			want: []Destination{
				{Name: "h5", Host: "h5.example.com", Port: 2233, User: "default_user"},
				{Name: "h6", Host: "h6.example.com", Port: 22, User: "user3"},
			},
			wantErr: false,
		},
		{
			name:       "overrides target hosts from inventory address match",
			targetName: "target4",
			overrides: &Overrides{
				FilterHosts: []string{"h5.example.com", "h7.example.com"},
			},
			want: []Destination{
				{Name: "h5", Host: "h5.example.com", Port: 2233, User: "default_user"},
				{Name: "", Host: "h7.example.com", Port: 22, User: "user3"},
			},
			wantErr: false,
		},
		{
			name:       "overrides target hosts direct, name and address match",
			targetName: "target1",
			overrides: &Overrides{
				FilterHosts: []string{"host3_name", "bad-host", "host2"},
			},
			want: []Destination{
				{Name: "host3_name", Host: "host3", Port: 2020, User: "user3"},
				{Name: "", Host: "host2", Port: 2222, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "overrides target hosts direct, address match",
			targetName: "target1",
			overrides: &Overrides{
				FilterHosts: []string{"host1", "bad-host", "host2"},
			},
			want: []Destination{
				{Name: "", Host: "host1", Port: 22, User: "user1"},
				{Name: "", Host: "host2", Port: 2222, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "target not found",
			targetName: "nonexistent",
			wantErr:    true,
		},
		{
			name:       "target without anything defined",
			targetName: "target3",
			wantErr:    true,
		},
		{
			name:       "target as ip",
			targetName: "127.0.0.1:2222",
			want: []Destination{
				{Host: "127.0.0.1", Port: 2222, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "target as ip, no port",
			targetName: "127.0.0.1",
			want: []Destination{
				{Host: "127.0.0.1", Port: 22, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "target as fqdn",
			targetName: "example.com:2222",
			want: []Destination{
				{Host: "example.com", Port: 2222, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "target as fqdn, no port",
			targetName: "host.example.com",
			want: []Destination{
				{Host: "host.example.com", Port: 22, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "target as localhost with port",
			targetName: "localhost:50958",
			want: []Destination{
				{Host: "localhost", Port: 50958, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "valid target with inventory file",
			targetName: "target2",
			want: []Destination{
				{Host: "h1.example.com", Port: 22, User: "default_user", Name: "h1"},
				{Host: "h2.example.com", Port: 2233, User: "default_user", Name: "h2"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 22, User: "user2", Name: "h4"},
			},
			wantErr: false,
		},
		{
			name:       "overrides inventory file",
			targetName: "target2",
			overrides: &Overrides{
				InventoryFile: "testdata/override_inventory.yml",
			},
			want: []Destination{
				{Host: "host3", Port: 22, User: "default_user"},
				{Host: "host4", Port: 2222, User: "user2"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.overrides = tt.overrides
			got, err := p.TargetHosts(tt.targetName)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestPlayBook_TargetHostsOverrides(t *testing.T) {

	t.Run("override hosts with file", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{InventoryFile: "testdata/hosts-without-groups.yml"})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []Destination{
			{Name: "h2", Host: "h2.example.com", Port: 2233, User: "umputun"},
			{Name: "h3", Host: "h3.example.com", Port: 22, User: "user1"},
			{Name: "h4", Host: "h4.example.com", Port: 22, User: "user2"},
			{Name: "hh1", Host: "hh1.example.com", Port: 22, User: "umputun"},
			{Name: "hh2", Host: "hh2.example.com", Port: 2233, User: "user1"},
		}, res)
	})

	t.Run("override hosts with file not found", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{InventoryFile: "testdata/hosts_not_found"})
		require.NoError(t, err)
		_, err = c.TargetHosts("blah")
		require.ErrorContains(t, err, "no such file or directory")
		t.Log(err)
	})

	t.Run("override hosts with http", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fh, err := os.Open("testdata/hosts-without-groups.yml")
			require.NoError(t, err)
			defer fh.Close()
			_, err = io.Copy(w, fh)
			require.NoError(t, err)
		}))
		defer ts.Close()
		c, err := New("testdata/f1.yml", &Overrides{InventoryURL: ts.URL})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []Destination{
			{Name: "h2", Host: "h2.example.com", Port: 2233, User: "umputun"},
			{Name: "h3", Host: "h3.example.com", Port: 22, User: "user1"},
			{Name: "h4", Host: "h4.example.com", Port: 22, User: "user2"},
			{Name: "hh1", Host: "hh1.example.com", Port: 22, User: "umputun"},
			{Name: "hh2", Host: "hh2.example.com", Port: 2233, User: "user1"},
		}, res)
	})

	t.Run("override hosts with http failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()
		c, err := New("testdata/f1.yml", &Overrides{InventoryURL: ts.URL})
		require.NoError(t, err)
		_, err = c.TargetHosts("blah")
		require.ErrorContains(t, err, "status: 500 Internal Server Error")
		t.Log(err)
	})
}

func TestPlayBook_parseInventoryGroups(t *testing.T) {
	playbook := &PlayBook{User: "defaultUser"}

	tests := []struct {
		name      string
		inventory string
		groups    []string
		want      []Destination
	}{
		{
			name:      "all groups",
			inventory: "testdata/hosts-with-groups.yml",
			groups:    nil,
			want: []Destination{
				{Host: "h1.example.com", Port: 22, User: "defaultUser", Name: "h1"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser", Name: "h2"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 22, User: "user2", Name: "h4"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser", Name: "h5"},
				{Host: "h6.example.com", Port: 22, User: "user3", Name: "h6"},
				{Host: "h7.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "group 1",
			inventory: "testdata/hosts-with-groups.yml",
			groups:    []string{"gr1"},
			want: []Destination{
				{Host: "h1.example.com", Port: 22, User: "defaultUser", Name: "h1"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser", Name: "h2"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 22, User: "user2", Name: "h4"},
			},
		},
		{
			name:      "group 2",
			inventory: "testdata/hosts-with-groups.yml",
			groups:    []string{"gr2"},
			want: []Destination{
				{Host: "h5.example.com", Port: 2233, User: "defaultUser", Name: "h5"},
				{Host: "h6.example.com", Port: 22, User: "user3", Name: "h6"},
				{Host: "h7.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "group 1 and 2",
			inventory: "testdata/hosts-with-groups.yml",
			groups:    []string{"gr1", "gr2"},
			want: []Destination{
				{Host: "h1.example.com", Port: 22, User: "defaultUser", Name: "h1"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser", Name: "h2"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 22, User: "user2", Name: "h4"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser", Name: "h5"},
				{Host: "h6.example.com", Port: 22, User: "user3", Name: "h6"},
				{Host: "h7.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "empty group",
			inventory: "testdata/hosts-with-groups.yml",
			groups:    []string{},
			want: []Destination{
				{Host: "h1.example.com", Port: 22, User: "defaultUser", Name: "h1"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser", Name: "h2"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 22, User: "user2", Name: "h4"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser", Name: "h5"},
				{Host: "h6.example.com", Port: 22, User: "user3", Name: "h6"},
				{Host: "h7.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "non-existent group",
			inventory: "testdata/hosts-with-groups.yml",
			groups:    []string{"non-existent"},
			want:      []Destination{},
		},
		{
			name:      "hosts inventory",
			inventory: "testdata/hosts-without-groups.yml",
			want: []Destination{
				{Name: "h2", Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Name: "h3", Host: "h3.example.com", Port: 22, User: "user1"},
				{Name: "h4", Host: "h4.example.com", Port: 22, User: "user2"},
				{Name: "hh1", Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Name: "hh2", Host: "hh2.example.com", Port: 2233, User: "user1"}},
		},
		{
			name:      "hosts inventory but group name set",
			inventory: "testdata/hosts-without-groups.yml",
			groups:    []string{"some"},
			want: []Destination{
				{Name: "h2", Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Name: "h3", Host: "h3.example.com", Port: 22, User: "user1"},
				{Name: "h4", Host: "h4.example.com", Port: 22, User: "user2"},
				{Name: "hh1", Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Name: "hh2", Host: "hh2.example.com", Port: 2233, User: "user1"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, err := os.Open(tt.inventory)
			require.NoError(t, err)
			defer reader.Close()
			got, err := playbook.parseInventory(reader, tt.groups)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
