package main

import (
	"bytes"
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

	tests := []struct {
		name      string
		args      []string
		wantLog   string
		wantError bool
	}{
		{
			name:      "Set secret",
			args:      []string{"--key", "secretkey", "--conn", "file://" + tempDB.Name(), "set", "key1=value1"},
			wantLog:   "set command, key=key1",
			wantError: false,
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

func runCommand() error {
	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		return err
	}
	return run(p, opts)
}
