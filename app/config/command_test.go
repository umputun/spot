package config

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

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

func TestCmd_UnmarshalYAML(t *testing.T) {
	type testCase struct {
		name        string
		yamlInput   string
		expectedCmd Cmd
		expectedErr bool
	}

	testCases := []testCase{
		{
			name: "Simple case",
			yamlInput: `
name: test
script: echo "Hello, World!"
`,
			expectedCmd: Cmd{
				Name:   "test",
				Script: `echo "Hello, World!"`,
			},
		},

		{
			name: "Copy and Mcopy",
			yamlInput: `
name: test
copy:
  src: source
  dst: destination
`,
			expectedCmd: Cmd{
				Name: "test",
				Copy: CopyInternal{
					Source: "source",
					Dest:   "destination",
				},
			},
		},

		{
			name: "All fields",
			yamlInput: `
name: test
copy:
  src: source
  dst: destination
mcopy:
  - src: source1
    dst: destination1
  - src: source2
    dst: destination2
sync:
  src: sync-source
  dst: sync-destination
delete:
  path: path-to-delete
wait:
  interval: 5s
  timeout: 1m
  cmd: echo "waiting"
script: echo "Hello, World!"
env:
  KEY: VALUE
options:
  ignore_errors: true
  no_auto: true
  local: true
`,
			expectedCmd: Cmd{
				Name:   "test",
				Script: `echo "Hello, World!"`,
				Copy: CopyInternal{
					Source: "source",
					Dest:   "destination",
				},
				MCopy: []CopyInternal{
					{
						Source: "source1",
						Dest:   "destination1",
					},
					{
						Source: "source2",
						Dest:   "destination2",
					},
				},
				Sync: SyncInternal{
					Source: "sync-source",
					Dest:   "sync-destination",
				},
				Delete: DeleteInternal{
					Location: "path-to-delete",
				},
				Wait: WaitInternal{
					CheckDuration: time.Second * 5,
					Timeout:       time.Minute,
					Command:       `echo "waiting"`,
				},
				Environment: map[string]string{
					"KEY": "VALUE",
				},
				Options: struct {
					IgnoreErrors bool `yaml:"ignore_errors" toml:"ignore_errors"`
					NoAuto       bool `yaml:"no_auto" toml:"no_auto"`
					Local        bool `yaml:"local" toml:"local"`
				}{
					IgnoreErrors: true,
					NoAuto:       true,
					Local:        true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var c Cmd
			err := yaml.Unmarshal([]byte(tc.yamlInput), &c)

			if tc.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedCmd, c)
			}
		})
	}
}
