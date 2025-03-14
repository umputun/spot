package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/jessevdk/go-flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpotSecrets(t *testing.T) {
	tempDB, err := os.CreateTemp("", "test.db")
	require.NoError(t, err)
	defer os.Remove(tempDB.Name())
	setupLog(true)

	tests := []struct {
		name      string
		args      []string
		wantLog   string
		wantError bool
	}{
		{
			name:      "Set secret",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "set", "key1", "value1"},
			wantLog:   "set command, key=key1",
			wantError: false,
		},
		{
			name:      "Set secret, no value",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "set", "key1"},
			wantLog:   "set command, key=key1",
			wantError: true,
		},
		{
			name:      "Get secret",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "get", "key1"},
			wantLog:   "get command, key=key1\nkey=key1, value=value1",
			wantError: false,
		},
		{
			name:      "Get non-existent secret",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "get", "key2"},
			wantLog:   "get command, key=key2",
			wantError: true,
		},
		{
			name:      "Delete secret",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "del", "key1"},
			wantLog:   "del command, key=key1\nkey=key1 deleted",
			wantError: false,
		},
		{
			name:      "Delete non-existent secret",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "del", "key2"},
			wantLog:   "del command, key=key2",
			wantError: true,
		},
		{
			name:      "List secrets",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "list", "abc"},
			wantLog:   `list command, key-prefix="abc"`,
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = append([]string{"spot"}, tc.args...)
			var buf bytes.Buffer
			log.SetOutput(&buf)

			err := runCommand()
			if tc.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			logged := buf.String()
			exps := strings.Split(tc.wantLog, "\n")
			for _, exp := range exps {
				assert.Contains(t, logged, exp)
			}
		})
	}
}

func TestSpotSecrets_ListWithAndWithoutPrefix(t *testing.T) {
	tempDB, err := os.CreateTemp("", "test.db")
	require.NoError(t, err)
	defer os.Remove(tempDB.Name())

	setArgs := func(key, value string) []string {
		return []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "set", key, value}
	}

	keysAndValues := [][2]string{
		{"key1", "value1"},
		{"key2", "value2"},
		{"key3", "value3"},
		{"key4", "value4"},
		{"prefix_key5", "value5"},
		{"prefix_key6", "value6"},
	}

	for _, kv := range keysAndValues {
		os.Args = append([]string{"spot"}, setArgs(kv[0], kv[1])...)
		require.NoError(t, runCommand())
	}

	testCases := []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{
			name:       "Without prefix",
			args:       []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "list"},
			wantOutput: "key1\tkey2\tkey3\tkey4\t\nprefix_key5\tprefix_key6\t\n",
		},
		{
			name:       "With prefix",
			args:       []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "list", "prefix_"},
			wantOutput: "prefix_key5\tprefix_key6\t\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = append([]string{"spot"}, tc.args...)

			// capture the standard output
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err = runCommand()
			require.NoError(t, err)

			// restore the original standard output
			_ = w.Close()
			os.Stdout = oldStdout

			var buf bytes.Buffer
			_, _ = io.Copy(&buf, r)
			output := buf.String()
			assert.Equal(t, tc.wantOutput, output)
		})
	}
}

func TestMainFunc(t *testing.T) {
	tempDB, err := os.CreateTemp("", "test.db")
	require.NoError(t, err)
	defer os.Remove(tempDB.Name())

	testCases := []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{
			name:       "Main help output",
			args:       []string{"--help"},
			wantOutput: "spot secrets",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// set the command-line arguments
			os.Args = append([]string{"spot"}, tc.args...)

			// capture the standard output
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// replace the exit function with a custom one
			exited := false
			exitFunc = func(int) {
				exited = true
			}

			main()

			// restore the original exit function and standard output
			exitFunc = os.Exit
			_ = w.Close()
			os.Stdout = oldStdout

			// check if the custom exit function was called
			assert.True(t, exited)

			// check the captured standard output
			var buf bytes.Buffer
			_, _ = io.Copy(&buf, r)
			output := buf.String()
			assert.Contains(t, output, tc.wantOutput)
		})
	}
}

func runCommand() error {
	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		return err
	}
	return run(p, opts)
}
