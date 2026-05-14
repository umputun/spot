// Package executor provides tests for the SSM executor.
package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/spot/pkg/executor/mocks"
)

func TestNewSSM(t *testing.T) {
	t.Run("creates valid SSM executor", func(t *testing.T) {
		client := &mocks.SSMClientMock{}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", 30*time.Second, logs)
		require.NoError(t, err)
		assert.Equal(t, "i-12345678", ssmExec.instanceID)
		assert.Equal(t, 30*time.Second, ssmExec.timeout)
	})

	t.Run("nil client returns error", func(t *testing.T) {
		logs := MakeLogs(false, false, nil)
		_, err := NewSSM(nil, "i-12345678", 30*time.Second, logs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SSM client is required")
	})

	t.Run("empty instance ID returns error", func(t *testing.T) {
		client := &mocks.SSMClientMock{}
		logs := MakeLogs(false, false, nil)
		_, err := NewSSM(client, "", 30*time.Second, logs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance ID is required")
	})
}

func TestSSM_Run(t *testing.T) {
	timeout := 30 * time.Second

	t.Run("empty command returns nil", func(t *testing.T) {
		client := &mocks.SSMClientMock{}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		output, err := ssmExec.Run(context.Background(), "", nil)
		require.NoError(t, err)
		assert.Nil(t, output)
	})

	t.Run("successful command returns stdout", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status:                types.CommandInvocationStatusSuccess,
					StandardOutputContent: aws.String("line1\nline2\nline3\n"),
					StandardErrorContent:  aws.String(""),
					ResponseCode:          int32(0),
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		output, err := ssmExec.Run(context.Background(), "echo hello", nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "line2", "line3"}, output)
	})

	t.Run("failed command returns error", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status:                types.CommandInvocationStatusFailed,
					StandardOutputContent: aws.String("output"),
					StandardErrorContent:  aws.String("error details"),
					ResponseCode:          int32(1),
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		output, err := ssmExec.Run(context.Background(), "false", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Failed")
		assert.Contains(t, err.Error(), "error details")
		assert.Contains(t, err.Error(), "exit code: 1")
		assert.Equal(t, []string{"output"}, output)
	})

	t.Run("timed out command returns error", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status: types.CommandInvocationStatusInProgress,
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", 1*time.Second, logs)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err = ssmExec.Run(ctx, "sleep 10", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
	})

	t.Run("context cancellation", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				time.Sleep(2 * time.Second)
				return &ssm.GetCommandInvocationOutput{
					Status: types.CommandInvocationStatusInProgress,
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", 30*time.Second, logs)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		_, err = ssmExec.Run(ctx, "sleep 10", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")
	})

	t.Run("timeout zero returns nil for empty command", func(t *testing.T) {
		client := &mocks.SSMClientMock{}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", 0, logs)
		require.NoError(t, err)

		output, err := ssmExec.Run(context.Background(), "", nil)
		require.NoError(t, err)
		assert.Nil(t, output)
	})
}

func TestSSM_Close(t *testing.T) {
	client := &mocks.SSMClientMock{}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", 30*time.Second, logs)
	require.NoError(t, err)

	err = ssmExec.Close()
	require.NoError(t, err)
}

func TestSSM_Upload(t *testing.T) {
	timeout := 30 * time.Second

	setupTest := func(t *testing.T) (*SSM, string) {
		t.Helper()
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		err := os.WriteFile(testFile, []byte("hello world"), 0o644)
		require.NoError(t, err)

		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status:                types.CommandInvocationStatusSuccess,
					StandardOutputContent: aws.String(""),
					ResponseCode:          int32(0),
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		return ssmExec, testFile
	}

	t.Run("upload file successfully", func(t *testing.T) {
		ssmExec, testFile := setupTest(t)

		err := ssmExec.Upload(context.Background(), testFile, "/tmp/test.txt", &UpDownOpts{})
		require.NoError(t, err)
	})

	t.Run("upload file with Mkdir creates remote directory", func(t *testing.T) {
		ssmExec, testFile := setupTest(t)

		err := ssmExec.Upload(context.Background(), testFile, "/tmp/newdir/test.txt", &UpDownOpts{Mkdir: true})
		require.NoError(t, err)
	})

	t.Run("upload non-existent file returns error", func(t *testing.T) {
		ssmExec, _ := setupTest(t)

		err := ssmExec.Upload(context.Background(), "/nonexistent/file.txt", "/tmp/test.txt", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read local file")
	})
}

func TestSSM_Download(t *testing.T) {
	timeout := 30 * time.Second

	client := &mocks.SSMClientMock{
		SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &types.Command{
					CommandId: aws.String("cmd-12345"),
				},
			}, nil
		},
		GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:                types.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("aGVsbG8gd29ybGQ="),
				ResponseCode:          int32(0),
			}, nil
		},
	}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
	require.NoError(t, err)

	t.Run("download file successfully", func(t *testing.T) {
		tmpDir := t.TempDir()
		downloadPath := filepath.Join(tmpDir, "downloaded.txt")

		err := ssmExec.Download(context.Background(), "/tmp/remote.txt", downloadPath, &UpDownOpts{})
		require.NoError(t, err)

		data, err := os.ReadFile(downloadPath)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(data))
	})

	t.Run("download with Mkdir creates local directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		downloadPath := filepath.Join(tmpDir, "newdir", "downloaded.txt")

		err := ssmExec.Download(context.Background(), "/tmp/remote.txt", downloadPath, &UpDownOpts{Mkdir: true})
		require.NoError(t, err)

		data, err := os.ReadFile(downloadPath)
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(data))
	})
}

func TestSSM_DownloadDecodingError(t *testing.T) {
	timeout := 30 * time.Second

	client := &mocks.SSMClientMock{
		SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &types.Command{
					CommandId: aws.String("cmd-12345"),
				},
			}, nil
		},
		GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:                types.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String("not-valid-base64!!!"),
				ResponseCode:          int32(0),
			}, nil
		},
	}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	downloadPath := filepath.Join(tmpDir, "downloaded.txt")

	err = ssmExec.Download(context.Background(), "/tmp/remote.txt", downloadPath, &UpDownOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode base64")
}

func TestSSM_Sync(t *testing.T) {
	timeout := 30 * time.Second

	setupTest := func(t *testing.T) (*SSM, string) {
		t.Helper()
		tmpDir := t.TempDir()

		testFile := filepath.Join(tmpDir, "test.txt")
		err := os.WriteFile(testFile, []byte("hello"), 0o644)
		require.NoError(t, err)

		testFile2 := filepath.Join(tmpDir, "test2.txt")
		err = os.WriteFile(testFile2, []byte("world"), 0o644)
		require.NoError(t, err)

		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status:                types.CommandInvocationStatusSuccess,
					StandardOutputContent: aws.String(""),
					ResponseCode:          int32(0),
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		return ssmExec, tmpDir
	}

	t.Run("sync directory uploads all files", func(t *testing.T) {
		ssmExec, tmpDir := setupTest(t)

		uploaded, err := ssmExec.Sync(context.Background(), tmpDir, "/tmp/remote", &SyncOpts{})
		require.NoError(t, err)
		assert.Len(t, uploaded, 2)
	})

	t.Run("sync with Delete removes extra files", func(t *testing.T) {
		ssmExec, tmpDir := setupTest(t)

		uploaded, err := ssmExec.Sync(context.Background(), tmpDir, "/tmp/remote", &SyncOpts{Delete: true})
		require.NoError(t, err)
		assert.Len(t, uploaded, 2)
	})
}

func TestSSM_Delete(t *testing.T) {
	timeout := 30 * time.Second

	t.Run("delete file successfully", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status:                types.CommandInvocationStatusSuccess,
					StandardOutputContent: aws.String(""),
					ResponseCode:          int32(0),
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		err = ssmExec.Delete(context.Background(), "/tmp/test.txt", &DeleteOpts{Recursive: false})
		require.NoError(t, err)
	})

	t.Run("delete non-existent file returns nil", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return nil, fmt.Errorf("No such file or directory")
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		err = ssmExec.Delete(context.Background(), "/tmp/nonexistent.txt", &DeleteOpts{Recursive: false})
		require.NoError(t, err)
	})

	t.Run("delete recursive", func(t *testing.T) {
		client := &mocks.SSMClientMock{
			SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
				return &ssm.SendCommandOutput{
					Command: &types.Command{
						CommandId: aws.String("cmd-12345"),
					},
				}, nil
			},
			GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
				return &ssm.GetCommandInvocationOutput{
					Status:                types.CommandInvocationStatusSuccess,
					StandardOutputContent: aws.String(""),
					ResponseCode:          int32(0),
				}, nil
			},
		}
		logs := MakeLogs(false, false, nil)
		ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
		require.NoError(t, err)

		err = ssmExec.Delete(context.Background(), "/tmp/testdir", &DeleteOpts{Recursive: true})
		require.NoError(t, err)
	})
}

func TestSSM_UploadLargeFile(t *testing.T) {
	timeout := 30 * time.Second

	tmpDir := t.TempDir()
	largeFile := filepath.Join(tmpDir, "large.txt")
	data := make([]byte, maxCommandSize+1)
	for i := range data {
		data[i] = byte(i % 256)
	}
	err := os.WriteFile(largeFile, data, 0o644)
	require.NoError(t, err)

	client := &mocks.SSMClientMock{}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
	require.NoError(t, err)

	err = ssmExec.Upload(context.Background(), largeFile, "/tmp/large.txt", &UpDownOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
	assert.Contains(t, err.Error(), "use S3 instead")
}

func TestSSM_DownloadWithEmptyOutput(t *testing.T) {
	timeout := 30 * time.Second

	client := &mocks.SSMClientMock{
		SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &types.Command{
					CommandId: aws.String("cmd-12345"),
				},
			}, nil
		},
		GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:                types.CommandInvocationStatusSuccess,
				StandardOutputContent: aws.String(""),
				ResponseCode:          int32(0),
			}, nil
		},
	}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	downloadPath := filepath.Join(tmpDir, "downloaded.txt")

	err = ssmExec.Download(context.Background(), "/tmp/remote.txt", downloadPath, &UpDownOpts{})
	require.NoError(t, err) // Empty output → write empty file → no error

	// Verify file exists but is empty
	data, err := os.ReadFile(downloadPath)
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestSSM_Cleanup(t *testing.T) {
	timeout := 30 * time.Second

	client := &mocks.SSMClientMock{}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
	require.NoError(t, err)

	err = ssmExec.Close()
	require.NoError(t, err)
}

func TestSSM_RunWithExitCode(t *testing.T) {
	timeout := 30 * time.Second

	client := &mocks.SSMClientMock{
		SendCommandFunc: func(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &types.Command{
					CommandId: aws.String("cmd-12345"),
				},
			}, nil
		},
		GetCommandInvocationFunc: func(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:                types.CommandInvocationStatusFailed,
				StandardOutputContent: aws.String("some output"),
				StandardErrorContent:  aws.String("command failed"),
				ResponseCode:          int32(42),
			}, nil
		},
	}
	logs := MakeLogs(false, false, nil)
	ssmExec, err := NewSSM(client, "i-12345678", timeout, logs)
	require.NoError(t, err)

	output, err := ssmExec.Run(context.Background(), "exit 42", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit code: 42")
	assert.Equal(t, []string{"some output"}, output)
}

func TestSSM_InterfaceImplementation(t *testing.T) {
	var _ Interface = (*SSM)(nil)
}
