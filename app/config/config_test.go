package config

import (
	"io"
	"net/http"
	"net/http/httptest"
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

		tsk := c.Tasks["deploy-remark42"]
		assert.Equal(t, 5, len(tsk.Commands), "5 commands")
	})

	t.Run("incorrectly formatted file", func(t *testing.T) {
		_, err := New("testdata/bad-format.yml", nil)
		assert.ErrorContains(t, err, "can't unmarshal config testdata/bad-format.yml")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := New("testdata/bad.yml", nil)
		assert.EqualError(t, err, "can't read config testdata/bad.yml: open testdata/bad.yml: no such file or directory")
	})
}

func TestPlayBook_Task(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
	require.NoError(t, err)

	t.Run("not-found", func(t *testing.T) {
		_, err = c.Task("no-such-task")
		assert.EqualError(t, err, "task no-such-task not found")
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
		cmd := c.Tasks["deploy-remark42"].Commands[3]
		assert.Equal(t, "git", cmd.Name, "name")
		res := cmd.getScriptCommand()
		assert.Equal(t, `sh -c "git clone https://example.com/remark42.git /srv || true; cd /srv; git pull"`, res)
	})

	t.Run("no-script", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[1]
		assert.Equal(t, "copy configuration", cmd.Name)
		res := cmd.getScriptCommand()
		assert.Equal(t, "", res)
	})

	t.Run("script with env", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[4]
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
					{Host: "host3"},
				},
			},
			"target2": {
				InventoryFile: Inventory{Location: "testdata/hosts", Group: "gr1"},
			},
			"target3": {},
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
				{Host: "host3", Port: 22, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "target from config, user from overrides",
			targetName: "target1",
			want: []Destination{
				{Host: "host1", Port: 22, User: "user1"},
				{Host: "host2", Port: 2222, User: "user2"},
				{Host: "host3", Port: 22, User: "user2"},
			},
			overrides: &Overrides{
				User: "user2",
			},
			wantErr: false,
		},
		{
			name:       "overrides target hosts with port",
			targetName: "target1",
			overrides: &Overrides{
				TargetHosts: []string{"host2:2222"},
			},
			want: []Destination{
				{Host: "host2", Port: 2222, User: "default_user"},
			},
			wantErr: false,
		},
		{
			name:       "overrides target hosts without port",
			targetName: "target1",
			overrides: &Overrides{
				TargetHosts: []string{"host2"},
			},
			want: []Destination{
				{Host: "host2", Port: 22, User: "default_user"},
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
				{Host: "hh1.example.com", Port: 22, User: "default_user"},
				{Host: "h2.example.com", Port: 2233, User: "default_user"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 2233, User: "user2"},
			},
			wantErr: false,
		},
		{
			name:       "overrides inventory file",
			targetName: "target2",
			overrides: &Overrides{
				InventoryFile: "testdata/override_inventory",
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

	t.Run("override hosts directly", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{TargetHosts: []string{"h1", "h2"}})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah") // no such target
		require.NoError(t, err)
		assert.Equal(t, []Destination{{Host: "h1", Port: 22, User: "umputun"}, {Host: "h2", Port: 22, User: "umputun"}}, res)
	})

	t.Run("override hosts with file", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{InventoryFile: "testdata/hosts"})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []Destination{
			{Host: "hh1.example.com", Port: 22, User: "umputun"},
			{Host: "h2.example.com", Port: 2233, User: "umputun"},
			{Host: "h3.example.com", Port: 22, User: "user1"},
			{Host: "h4.example.com", Port: 2233, User: "user2"},
			{Host: "h5.example.com", Port: 2233, User: "umputun"},
			{Host: "h6.example.com", Port: 22, User: "user3"},
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
			_, err := w.Write([]byte("h1.example.com:2223 user2\nh2.example.com\nlocalhost:2222\n8.8.8.8 user3\n"))
			require.NoError(t, err)
		}))
		defer ts.Close()
		c, err := New("testdata/f1.yml", &Overrides{InventoryURL: ts.URL})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []Destination{
			{Host: "h1.example.com", Port: 2223, User: "user2"},
			{Host: "h2.example.com", Port: 22, User: "umputun"},
			{Host: "localhost", Port: 2222, User: "umputun"},
			{Host: "8.8.8.8", Port: 22, User: "user3"},
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

func TestPlayBook_parseInventory(t *testing.T) {
	inventoryContent := `[gr1]
hh1.example.com
#blah blah blah
h2.example.com:2233
h3.example.com user1
h4.example.com:2233 user2

[gr2]
h5.example.com:2233
h6.example.com user3
`
	playbook := &PlayBook{User: "defaultUser"}

	tests := []struct {
		name         string
		groupName    string
		want         []Destination
		removeGroups bool
	}{
		{
			name:      "All groups",
			groupName: "all",
			want: []Destination{
				{Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 2233, User: "user2"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h6.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "Group 1",
			groupName: "gr1",
			want: []Destination{
				{Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 2233, User: "user2"},
			},
		},
		{
			name:      "Group 2",
			groupName: "gr2",
			want: []Destination{
				{Host: "h5.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h6.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "Empty group name",
			groupName: "",
			want: []Destination{
				{Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 2233, User: "user2"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h6.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:         "No-group inventory",
			groupName:    "",
			removeGroups: true,
			want: []Destination{
				{Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 2233, User: "user2"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h6.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:         "No-group inventory but name is set to all",
			groupName:    "all",
			removeGroups: true,
			want: []Destination{
				{Host: "hh1.example.com", Port: 22, User: "defaultUser"},
				{Host: "h2.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h3.example.com", Port: 22, User: "user1"},
				{Host: "h4.example.com", Port: 2233, User: "user2"},
				{Host: "h5.example.com", Port: 2233, User: "defaultUser"},
				{Host: "h6.example.com", Port: 22, User: "user3"},
			},
		},
		{
			name:      "Non-existent group",
			groupName: "non-existent",
			want:      []Destination{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader
			if tt.removeGroups {
				inventoryContentCleaned := strings.ReplaceAll(inventoryContent, "[gr1]", "")
				inventoryContentCleaned = strings.ReplaceAll(inventoryContentCleaned, "[gr2]", "")
				reader = strings.NewReader(inventoryContentCleaned)
			} else {
				reader = strings.NewReader(inventoryContent)
			}
			got, err := playbook.parseInventory(reader, tt.groupName)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
