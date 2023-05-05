package executor

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDry_Run(t *testing.T) {
	ctx := context.Background()
	dry := NewDry("hostAddr", "hostName")
	res, err := dry.Run(ctx, "ls -la /srv", true)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, "ls -la /srv", res[0])
}

func TestDryUpload(t *testing.T) {
	tempFile, err := os.CreateTemp("", "spot-script")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	content := "line1\nline2\nline3\n"
	_, err = tempFile.WriteString(content)
	require.NoError(t, err)
	tempFile.Close()

	dry := &Dry{
		hostAddr: "host1.example.com",
		hostName: "host1",
	}

	stdout := captureOutput(func() {
		err = dry.Upload(context.Background(), tempFile.Name(), "remote/path/spot-script", true)
	})

	require.NoError(t, err)

	// check for logs with the "command script" and file content in the output
	assert.Contains(t, stdout, "command script remote/path/spot-script",
		"expected log entry containing 'command script' not found")
	require.Contains(t, stdout, "line1", "expected log entry containing 'line1' not found")
	require.Contains(t, stdout, "line2", "expected log entry containing 'line2' not found")
	require.Contains(t, stdout, "line3", "expected log entry containing 'line3' not found")
}

func TestDryUpload_FileOpenError(t *testing.T) {
	nonExistentFile := "non_existent_file"

	dry := &Dry{
		hostAddr: "host1.example.com",
		hostName: "host1",
	}

	err := dry.Upload(context.Background(), nonExistentFile, "remote/path/spot-script", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open non_existent_file", "expected error message containing 'open non_existent_file' not found")
}

func TestDryOperations(t *testing.T) {
	dry := &Dry{
		hostAddr: "host1.example.com",
		hostName: "host1",
	}

	testCases := []struct {
		name        string
		operation   func() error
		expectedLog string
	}{
		{
			name: "download",
			operation: func() error {
				return dry.Download(context.Background(), "remote/path", "local/path", true)
			},
			expectedLog: "[DEBUG] download local/path to remote/path, mkdir: true",
		},
		{
			name: "sync",
			operation: func() error {
				_, err := dry.Sync(context.Background(), "local/dir", "remote/dir", true)
				return err
			},
			expectedLog: "[DEBUG] sync local/dir to remote/dir, delite: true",
		},
		{
			name: "delete",
			operation: func() error {
				return dry.Delete(context.Background(), "remote/file", true)
			},
			expectedLog: "[DEBUG] delete remote/file, recursive: true",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buff := bytes.NewBuffer(nil)
			log.SetOutput(buff)
			err := tc.operation()
			require.NoError(t, err)
			stdout := buff.String()
			// check for logs with the expected log entry in the output
			assert.Contains(t, stdout, tc.expectedLog, "expected log entry not found")
		})
	}
}

func captureOutput(f func()) (stdout string) {
	// redirect stdout
	oldStdout := os.Stdout
	rout, wout, _ := os.Pipe()
	os.Stdout = wout

	// execute the function
	f()

	// stop capturing
	wout.Close()
	os.Stdout = oldStdout

	// read the captured output
	stdoutBuf, _ := io.ReadAll(rout)

	return string(stdoutBuf)
}
