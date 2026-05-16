// Package executor provides tests for the SSM Session Manager executor.
package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/spot/pkg/executor/mocks"
)

func TestNewSSMSession(t *testing.T) {
	t.Run("creates valid SSMSession executor", func(t *testing.T) {
		client := &mocks.SSMSessionClientMock{}
		ssmExec, err := NewSSMSession(client, "i-12345678", "us-east-1", 30*time.Second, MakeLogs(false, false, nil))
		require.NoError(t, err)
		assert.Equal(t, "i-12345678", ssmExec.instanceID)
		assert.Equal(t, "us-east-1", ssmExec.region)
		assert.Equal(t, 30*time.Second, ssmExec.timeout)
	})

	t.Run("nil client returns error", func(t *testing.T) {
		_, err := NewSSMSession(nil, "i-12345678", "us-east-1", 30*time.Second, MakeLogs(false, false, nil))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SSM client is required")
	})

	t.Run("empty instance ID returns error", func(t *testing.T) {
		client := &mocks.SSMSessionClientMock{}
		_, err := NewSSMSession(client, "", "us-east-1", 30*time.Second, MakeLogs(false, false, nil))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance ID is required")
	})

	t.Run("empty region returns error", func(t *testing.T) {
		client := &mocks.SSMSessionClientMock{}
		_, err := NewSSMSession(client, "i-12345678", "", 30*time.Second, MakeLogs(false, false, nil))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "AWS region is required")
	})
}

func TestSSMSession_InterfaceImplementation(t *testing.T) {
	var _ Interface = (*SSMSession)(nil)
}

func TestSSMSession_Run(t *testing.T) {
	t.Run("empty command returns nil", func(t *testing.T) {
		client := &mocks.SSMSessionClientMock{}
		ssmExec, err := NewSSMSession(client, "i-12345678", "us-east-1", 30*time.Second, MakeLogs(false, false, nil))
		require.NoError(t, err)

		output, err := ssmExec.Run(context.Background(), "", nil)
		require.NoError(t, err)
		assert.Nil(t, output)
	})
}

func TestSSMSession_Cleanup(t *testing.T) {
	client := &mocks.SSMSessionClientMock{}
	ssmExec, err := NewSSMSession(client, "i-12345678", "us-east-1", 30*time.Second, MakeLogs(false, false, nil))
	require.NoError(t, err)

	err := ssmExec.Close()
	require.NoError(t, err)
}

func TestSSMSession_UploadLargeFile(t *testing.T) {
	tmpDir := t.TempDir()
	largeFile := filepath.Join(tmpDir, "large.txt")
	data := make([]byte, maxCommandSize+1)
	for i := range data {
		data[i] = byte(i % 256)
	}
	err := os.WriteFile(largeFile, data, 0o644)
	require.NoError(t, err)

	client := &mocks.SSMSessionClientMock{}
	ssmExec, err := NewSSMSession(client, "i-12345678", "us-east-1", 30*time.Second, MakeLogs(false, false, nil))
	require.NoError(t, err)

	err = ssmExec.Upload(context.Background(), largeFile, "/tmp/large.txt", &UpDownOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
	assert.Contains(t, err.Error(), "use S3 instead")
}

// TestShellQuote tests the shellQuote helper function.
func TestShellQuote(t *testing.T) {
	t.Run("simple path", func(t *testing.T) {
		assert.Equal(t, "'/tmp/test.txt'", shellQuote("/tmp/test.txt"))
	})

	t.Run("path with spaces", func(t *testing.T) {
		assert.Equal(t, "'/tmp/my file.txt'", shellQuote("/tmp/my file.txt"))
	})

	t.Run("path with single quotes", func(t *testing.T) {
		assert.Equal(t, "'/tmp/my'\\''file.txt'", shellQuote("/tmp/my'file.txt"))
	})

	t.Run("empty string", func(t *testing.T) {
		assert.Equal(t, "''", shellQuote(""))
	})
}

// ssmAPIError implements ErrorCode() for testing isRetriableSSMError.
type ssmAPIError struct{ code string }
func (e *ssmAPIError) Error() string      { return e.code }
func (e *ssmAPIError) ErrorCode() string { return e.code }

// TestIsRetriableSSMError tests the isRetriableSSMError helper function.
func TestIsRetriableSSMError(t *testing.T) {
	tests := []struct {
		code      string
		retriable bool
	}{
	{"ThrottlingException", true},
	{"TooManyRequestsException", true},
	{"RequestLimitExceeded", true},
	{"AccessDenied", false},
	{"InvalidInstanceId", false},
	{"network timeout", false},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			err := &ssmAPIError{code: tt.code}
			assert.Equal(t, tt.retriable, isRetriableSSMError(err))
		})
	}
}
