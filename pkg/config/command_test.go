package config

import (
	"io"
	"os"
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
				Script: "echo Hello, World!",
			},
			expectedScript:   `/bin/sh -c 'echo Hello, World!'`,
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
			name: "multiline command with exports",
			cmd: &Cmd{
				Script: `echo 'Hello, World!'
export FOO='bar'
echo 'Goodbye, World!'
 echo "with space"
export BAR='foo'`,
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"echo 'Hello, World!'",
				"export FOO='bar'",
				"echo 'Goodbye, World!'",
				" echo \"with space\"",
				"export BAR='foo'",
				"echo \"setvar FOO:SQ=${FOO}\"",
				"echo \"setvar BAR:SQ=${BAR}\"",
			},
		},
		{
			name: "multiline command with empty exports",
			cmd: &Cmd{
				Script: `echo 'Hello, World!'
export
echo 'Goodbye, World!'
export BAR
export FOO='bar'
  export BAZ='qux'`,
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"echo 'Hello, World!'",
				"export",
				"echo 'Goodbye, World!'",
				"export BAR",
				"export FOO='bar'",
				"  export BAZ='qux'",
				"echo \"setvar FOO:SQ=${FOO}\"",
				"echo \"setvar BAZ:SQ=${BAZ}\"",
			},
		},
		{
			name: "multiline command with shebang on non-first line",
			cmd: &Cmd{
				Script: `echo 'Hello, World!'
#!/bin/bash
export
echo 'Goodbye, World!'
export BAR
export FOO='bar'`,
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"echo 'Hello, World!'",
				"#!/bin/bash",
				"export",
				"echo 'Goodbye, World!'",
				"export BAR",
				"export FOO='bar'",
				"echo \"setvar FOO:SQ=${FOO}\"",
			},
		},
		{
			name: "multiline command with shebang on second line, but after initial empty line",
			cmd: &Cmd{
				Script: `
#!/bin/bash
echo 'Hello, World!'
export
echo 'Goodbye, World!'
export BAR
export FOO='bar'`,
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/bash",
				"set -e",
				"echo 'Hello, World!'",
				"export",
				"echo 'Goodbye, World!'",
				"export BAR",
				"export FOO='bar'",
				"echo \"setvar FOO:SQ=${FOO}\"",
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
			expectedScript:   `/bin/sh -c 'export GREETING="Hello, World!"; echo $GREETING'`,
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
				`export FAREWELL="Goodbye, World!"`,
				`export GREETING="Hello, World!"`,
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
				"# This is a comment",
				"echo 'Hello, World!' # Inline comment",
				"# Another comment",
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
		{
			name: "single line command with export",
			cmd: &Cmd{
				Script: "export GREETING='Hello, World!'",
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"export GREETING='Hello, World!'",
				"echo \"setvar GREETING:SQ=${GREETING}\"",
			},
		},
		{
			name: "single line with register",
			cmd: &Cmd{
				Script: `echo 'Hello, World!'
echo 'Goodbye, World!'`,
				Register: []string{"foo"},
			},
			expectedScript: "",
			expectedContents: []string{
				"#!/bin/sh",
				"set -e",
				"echo 'Hello, World!'",
				"echo 'Goodbye, World!'",
				"echo setvar foo=${foo}",
			},
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
	c, err := New("testdata/f1.yml", nil, nil)
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")

	t.Run("script", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[3]
		assert.Equal(t, "git", cmd.Name, "name")
		res := cmd.scriptCommand(cmd.Script)
		assert.Equal(t, `/bin/sh -c 'git clone https://example.com/remark42.git /srv || true; cd /srv; git pull'`, res)
	})

	t.Run("no-script", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[1]
		assert.Equal(t, "copy configuration", cmd.Name)
		res := cmd.scriptCommand(cmd.Script)
		assert.Equal(t, "", res)
	})

	t.Run("script with env", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[4]
		assert.Equal(t, "docker", cmd.Name)
		res := cmd.scriptCommand(cmd.Script)
		assert.Equal(t, `/bin/sh -c 'export BAR="qux"; export FOO="bar"; docker pull umputun/remark42:latest; docker stop remark42 || true; docker rm remark42 || true; docker run -d --name remark42 -p 8080:8080 umputun/remark42:latest'`, res)
	})
}

func TestCmd_getScriptCommandCustomShell(t *testing.T) {
	c, err := New("testdata/f1.yml", &Overrides{SSHShell: "/bin/bash"}, nil)
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")

	t.Run("script", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[3]
		assert.Equal(t, "git", cmd.Name, "name")
		res := cmd.scriptCommand(cmd.Script)
		assert.Equal(t, `/bin/bash -c 'git clone https://example.com/remark42.git /srv || true; cd /srv; git pull'`, res)
	})

	t.Run("no-script", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[1]
		assert.Equal(t, "copy configuration", cmd.Name)
		res := cmd.scriptCommand(cmd.Script)
		assert.Equal(t, "", res)
	})

	t.Run("script with env", func(t *testing.T) {
		cmd := c.Tasks[0].Commands[4]
		assert.Equal(t, "docker", cmd.Name)
		res := cmd.scriptCommand(cmd.Script)
		assert.Equal(t, `/bin/bash -c 'export BAR="qux"; export FOO="bar"; docker pull umputun/remark42:latest; docker stop remark42 || true; docker rm remark42 || true; docker run -d --name remark42 -p 8080:8080 umputun/remark42:latest'`, res)
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
			name: "with custom shebang",
			cmd: &Cmd{
				Script: "#!/bin/bash\necho 'Hello, World!'",
			},
			expected: "#!/bin/bash\nset -e\necho 'Hello, World!'\n",
		},
		{
			name: "with non-default shell",
			cmd: &Cmd{
				Script:   "echo 'Hello, World!'",
				SSHShell: "/bin/zsh",
			},
			expected: "#!/bin/zsh\nset -e\necho 'Hello, World!'\n",
		},
		{
			name: "with one environment variable",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
				Environment: map[string]string{
					"VAR1": "value1",
				},
			},
			expected: "#!/bin/sh\nset -e\nexport VAR1=\"value1\"\necho 'Hello, World!'\n",
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
			expected: "#!/bin/sh\nset -e\nexport VAR1=\"value1\"\nexport VAR2=\"value2\"\necho 'Hello, World!'\n",
		},
		{
			name: "with multiple environment variables and secrets",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
				Environment: map[string]string{
					"VAR1": "value1",
					"VAR2": "value2",
				},
				Secrets: map[string]string{
					"SEC1": "secret1",
				},
				Options: CmdOptions{
					Secrets: []string{"SEC1"},
				},
			},
			expected: "#!/bin/sh\nset -e\nexport VAR1=\"value1\"\nexport VAR2=\"value2\"\nexport SEC1=\"secret1\"\necho 'Hello, World!'\n",
		},
		{
			name: "with multiple secrets",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'",
				Secrets: map[string]string{
					"SEC1": "secret1",
					"SEC2": "secret2",
					"SEC3": "secret3",
				},
				Options: CmdOptions{
					Secrets: []string{"SEC1", "SEC2"},
				},
			},
			expected: "#!/bin/sh\nset -e\nexport SEC1=\"secret1\"\nexport SEC2=\"secret2\"\necho 'Hello, World!'\n",
		},
		{
			name: "with exports",
			cmd: &Cmd{
				Script: "echo 'Hello, World!'\nexport var1=blah\n export var2=baz",
			},
			expected: "#!/bin/sh\nset -e\necho 'Hello, World!'\nexport var1=blah\n export var2=baz\necho setvar var1=${var1}\necho setvar var2=${var2}\n",
		},
		{
			name: "with exports and register",
			cmd: &Cmd{
				Script:   "echo 'Hello, World!'\nexport var1=blah\n export var2=baz",
				Register: []string{"var21", "var22", "var23"},
			},
			expected: "#!/bin/sh\nset -e\necho 'Hello, World!'\nexport var1=blah\n export var2=baz\necho setvar var1=${var1}\necho setvar var2=${var2}\necho setvar var21=${var21}\necho setvar var22=${var22}\necho setvar var23=${var23}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := tt.cmd.scriptFile(tt.cmd.Script, tt.cmd.Register)
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
			name: "simple case",
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
			name: "delete multiple sets",
			yamlInput: `
name: test
delete:
  - {path: source1}
  - {path: source2}
`,
			expectedCmd: Cmd{
				Name:    "test",
				MDelete: []DeleteInternal{{Location: "source1"}, {Location: "source2"}},
			},
		},
		{
			name: "simple copy",
			yamlInput: `
name: test
copy: {src: source, dst: destination}
`,
			expectedCmd: Cmd{
				Name: "test",
				Copy: CopyInternal{Source: "source", Dest: "destination", Direction: "push"},
			},
		},
		{
			name: "copy multiple sets",
			yamlInput: `
name: test
copy:
  - {src: source1, dst: destination1}
  - {src: source2, dst: destination2}
`,
			expectedCmd: Cmd{
				Name:  "test",
				MCopy: []CopyInternal{{Source: "source1", Dest: "destination1", Direction: "push"}, {Source: "source2", Dest: "destination2", Direction: "push"}},
			},
		},

		{
			name: "simple sync",
			yamlInput: `
name: sync dirs
sync: {src: ".", dst: "/srv", exclude: [".DS_Store", ".git", ".idea", ".gitignore", "var", "spot.yml"]}
`,
			expectedCmd: Cmd{
				Name: "sync dirs",
				Sync: SyncInternal{Source: ".", Dest: "/srv", Exclude: []string{".DS_Store", ".git", ".idea", ".gitignore", "var", "spot.yml"}},
			},
		},
		{
			name: "msync",
			yamlInput: `
name: test
sync:
  - {src: source1, dst: destination1}
  - {src: source2, dst: destination2}
`,
			expectedCmd: Cmd{
				Name:  "test",
				MSync: []SyncInternal{{Source: "source1", Dest: "destination1"}, {Source: "source2", Dest: "destination2"}},
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
  secrets: [s1, s2]
`,
			expectedCmd: Cmd{
				Name:   "test",
				Script: `echo "Hello, World!"`,
				Copy: CopyInternal{
					Source:    "source",
					Dest:      "destination",
					Direction: "push",
				},
				MCopy: []CopyInternal{
					{
						Source:    "source1",
						Dest:      "destination1",
						Direction: "push",
					},
					{
						Source:    "source2",
						Dest:      "destination2",
						Direction: "push",
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
				Options: CmdOptions{
					IgnoreErrors: true,
					NoAuto:       true,
					Local:        true,
					Secrets:      []string{"s1", "s2"},
				},
			},
		},
		{
			name: "copy with pull direction",
			yamlInput: `
name: download file
copy: {src: /remote/file.txt, dst: ./local/, direction: pull}
`,
			expectedCmd: Cmd{
				Name: "download file",
				Copy: CopyInternal{Source: "/remote/file.txt", Dest: "./local/", Direction: "pull"},
			},
		},
		{
			name: "copy with explicit push direction",
			yamlInput: `
name: upload file
copy: {src: ./local/file.txt, dst: /remote/, direction: push}
`,
			expectedCmd: Cmd{
				Name: "upload file",
				Copy: CopyInternal{Source: "./local/file.txt", Dest: "/remote/", Direction: "push"},
			},
		},
		{
			name: "multiple copy with mixed directions",
			yamlInput: `
name: mixed copy
copy:
  - {src: /remote/config.yml, dst: ./backup/, direction: pull}
  - {src: ./new-config.yml, dst: /remote/, direction: push}
  - {src: /remote/data.csv, dst: ./data/, direction: pull}
`,
			expectedCmd: Cmd{
				Name: "mixed copy",
				MCopy: []CopyInternal{
					{Source: "/remote/config.yml", Dest: "./backup/", Direction: "pull"},
					{Source: "./new-config.yml", Dest: "/remote/", Direction: "push"},
					{Source: "/remote/data.csv", Dest: "./data/", Direction: "pull"},
				},
			},
		},
		{
			name: "copy without direction defaults to push",
			yamlInput: `
name: default push
copy: {src: ./file.txt, dst: /remote/}
`,
			expectedCmd: Cmd{
				Name: "default push",
				Copy: CopyInternal{Source: "./file.txt", Dest: "/remote/", Direction: "push"},
			},
		},
		{
			name: "multiple copy without direction defaults to push",
			yamlInput: `
name: default push multi
copy:
  - {src: ./file1.txt, dst: /remote/}
  - {src: ./file2.txt, dst: /remote/}
`,
			expectedCmd: Cmd{
				Name: "default push multi",
				MCopy: []CopyInternal{
					{Source: "./file1.txt", Dest: "/remote/", Direction: "push"},
					{Source: "./file2.txt", Dest: "/remote/", Direction: "push"},
				},
			},
		},
		{
			name: "invalid direction value",
			yamlInput: `
name: invalid direction
copy: {src: ./file.txt, dst: /remote/, direction: invalid}
`,
			expectedErr: true,
		},
		{
			name: "invalid direction in mcopy",
			yamlInput: `
name: invalid mcopy direction
copy:
  - {src: ./file1.txt, dst: /remote/, direction: push}
  - {src: ./file2.txt, dst: /remote/, direction: wrong}
`,
			expectedErr: true,
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

func TestCmd_validate(t *testing.T) {
	tbl := []struct {
		name        string
		cmd         Cmd
		expectedErr string
	}{
		{"only script", Cmd{Script: "example_script"}, ""},
		{"only copy", Cmd{Copy: CopyInternal{Source: "source", Dest: "dest"}}, ""},
		{"only mcopy", Cmd{MCopy: []CopyInternal{{Source: "source1", Dest: "dest1"}, {Source: "source2", Dest: "dest2"}}}, ""},
		{"only delete", Cmd{Delete: DeleteInternal{Location: "location"}}, ""},
		{"only sync", Cmd{Sync: SyncInternal{Source: "source", Dest: "dest"}}, ""},
		{"only msync", Cmd{MSync: []SyncInternal{{Source: "source", Dest: "dest"}}}, ""},
		{"only wait", Cmd{Wait: WaitInternal{Command: "command"}}, ""},
		{"only line", Cmd{Line: LineInternal{File: "/etc/bashrc", Match: "^PS1=", Delete: true}}, ""},
		{"line without operation", Cmd{Line: LineInternal{File: "/etc/bashrc", Match: "^PS1="}},
			"one of [script, copy, mcopy, delete, mdelete, sync, msync, wait, line, echo] must be set"},
		{"multiple fields set", Cmd{Script: "example_script", Copy: CopyInternal{Source: "source", Dest: "dest"}},
			"only one of [script, copy] is allowed"},
		{"nothing set", Cmd{}, "one of [script, copy, mcopy, delete, mdelete, sync, msync, wait, line, echo] must be set"},
		{"script with register", Cmd{Script: "example_script", Register: []string{"a", "b"}}, ""},
		{"unexpected register", Cmd{Copy: CopyInternal{Source: "source", Dest: "dest"}, Register: []string{"a", "b"}},
			"register is only allowed with script command"},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.validate()
			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCmd_GetWait(t *testing.T) {
	testCases := []struct {
		name           string
		cmd            *Cmd
		expectedCmd    string
		expectedReader io.Reader
	}{
		{
			name: "single-line wait command",
			cmd: &Cmd{
				Wait: WaitInternal{
					Timeout: time.Second * 10,
					Command: "echo Hello, World!",
				},
			},
			expectedCmd: `/bin/sh -c 'echo Hello, World!'`,
		},
		{
			name: "multi-line wait command",
			cmd: &Cmd{
				Wait: WaitInternal{
					Timeout: time.Second * 20,
					Command: `echo 'Hello, World!'
echo 'Goodbye, World!'`,
				},
			},
			expectedReader: strings.NewReader(`#!/bin/sh
set -e
echo 'Hello, World!'
echo 'Goodbye, World!'
`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, reader := tc.cmd.GetWait()
			assert.Equal(t, tc.expectedCmd, cmd)

			if tc.expectedReader != nil {
				expectedBytes, err := io.ReadAll(tc.expectedReader)
				assert.NoError(t, err)

				actualBytes, err := io.ReadAll(reader)
				assert.NoError(t, err)

				assert.Equal(t, string(expectedBytes), string(actualBytes))
			} else {
				assert.Nil(t, reader)
			}
		})
	}
}

func TestCmd_GetCondition(t *testing.T) {
	testCases := []struct {
		name           string
		cmd            *Cmd
		expectedCmd    string
		expectedReader io.Reader
		expectedInvert bool
	}{
		{
			name:           "single-line wait command",
			cmd:            &Cmd{Condition: "echo Hello, World!"},
			expectedCmd:    `/bin/sh -c 'echo Hello, World!'`,
			expectedInvert: false,
		},
		{
			name:           "single-line wait command inverted",
			cmd:            &Cmd{Condition: "! echo Hello, World!"},
			expectedCmd:    `/bin/sh -c 'echo Hello, World!'`,
			expectedInvert: true,
		},
		{
			name: "multi-line wait command",
			cmd: &Cmd{Condition: `echo 'Hello, World!'
echo 'Goodbye, World!'`,
			},
			expectedReader: strings.NewReader(`#!/bin/sh
set -e
echo 'Hello, World!'
echo 'Goodbye, World!'
`),
			expectedInvert: false,
		},
		{
			name: "multi-line wait command inverted",
			cmd: &Cmd{Condition: `!echo 'Hello, World!'
echo 'Goodbye, World!'`,
			},
			expectedReader: strings.NewReader(`#!/bin/sh
set -e
echo 'Hello, World!'
echo 'Goodbye, World!'
`),
			expectedInvert: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, reader, invert := tc.cmd.GetCondition()
			assert.Equal(t, tc.expectedCmd, cmd)
			assert.Equal(t, tc.expectedInvert, invert)

			if tc.expectedReader != nil {
				expectedBytes, err := io.ReadAll(tc.expectedReader)
				assert.NoError(t, err)

				actualBytes, err := io.ReadAll(reader)
				assert.NoError(t, err)

				assert.Equal(t, string(expectedBytes), string(actualBytes))
			} else {
				assert.Nil(t, reader)
			}
		})
	}
}

func TestHasShebang(t *testing.T) {
	testCases := []struct {
		name string
		inp  string
		exp  bool
	}{
		{"empty string", "", false},
		{"no newline", "test data", false},
		{"newline, no shebang", "test\ndata", false},
		{"newline, shebang not at start", "test\n# data", false},
		{"newline, shebang at start", "#!test\ndata", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &Cmd{} // assuming Cmd is your type with hasShebang method
			require.Equal(t, tc.exp, cmd.hasShebang(tc.inp))
		})
	}
}

func TestCmd_shell(t *testing.T) {
	t.Run("shell is not set", func(t *testing.T) {
		c := Cmd{}
		assert.Equal(t, "/bin/sh", c.shell())
	})

	t.Run("shell is set", func(t *testing.T) {
		c := Cmd{SSHShell: "/bin/bash"}
		assert.Equal(t, "/bin/bash", c.shell())
	})

	t.Run("shell is not set, local", func(t *testing.T) {
		c := Cmd{Options: CmdOptions{Local: true}}
		exp := "/bin/sh"
		if os.Getenv("SHELL") != "" {
			exp = os.Getenv("SHELL")
		}
		assert.Equal(t, exp, c.shell())
	})

	t.Run("shell is set, local", func(t *testing.T) {
		c := Cmd{LocalShell: "/bin/bash", Options: CmdOptions{Local: true}}
		assert.Equal(t, "/bin/bash", c.shell())
	})
}
