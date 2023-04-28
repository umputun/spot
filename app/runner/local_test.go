package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	testCases := []struct {
		name        string
		cmd         string
		expectError bool
	}{
		{
			name: "successful command execution",
			cmd:  "echo 'Hello, World!'",
		},
		{
			name:        "failed command execution",
			cmd:         "nonexistent-command",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			l := &Local{}
			out, err := l.Run(context.Background(), tc.cmd)

			if tc.expectError {
				assert.Error(t, err, "expected an error")
				return
			}
			assert.NoError(t, err, "unexpected error")
			require.Equal(t, 1, len(out), "output should have exactly one line")
			assert.Equal(t, "Hello, World!", out[0], "output line should match expected value")
		})
	}
}

func TestUpload(t *testing.T) {
	testCases := []struct {
		name        string
		srcContent  string
		dstDir      string
		mkdir       bool
		expectError bool
	}{
		{
			name:       "successful upload with mkdir=true",
			srcContent: "test content",
			dstDir:     "dst",
			mkdir:      true,
		},
		{
			name:       "successful upload with mkdir=false",
			srcContent: "test content",
			dstDir:     "",
			mkdir:      false,
		},
		{
			name:        "failed upload with non-existent directory and mkdir=false",
			srcContent:  "test content",
			dstDir:      "nonexistent",
			mkdir:       false,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srcFile, err := os.CreateTemp("", "src")
			require.NoError(t, err)
			defer os.Remove(srcFile.Name())

			_, err = srcFile.WriteString(tc.srcContent)
			require.NoError(t, err)
			srcFile.Close()

			baseDstDir, err := os.MkdirTemp("", "dst")
			require.NoError(t, err)
			defer os.RemoveAll(baseDstDir)

			dstDir := baseDstDir
			if tc.dstDir != "" {
				dstDir = filepath.Join(baseDstDir, tc.dstDir)
			}

			dstFile := filepath.Join(dstDir, filepath.Base(srcFile.Name()))

			l := &Local{}
			err = l.Upload(context.Background(), srcFile.Name(), dstFile, tc.mkdir)

			if tc.expectError {
				assert.Error(t, err, "expected an error")
				return
			}

			assert.NoError(t, err, "unexpected error")
			dstContent, err := os.ReadFile(dstFile)
			require.NoError(t, err)
			assert.Equal(t, tc.srcContent, string(dstContent), "uploaded content should match source content")
		})
	}
}

func TestLocal_Sync(t *testing.T) {

	testCases := []struct {
		name         string
		srcStructure map[string]string
		dstStructure map[string]string
		del          bool
		expected     []string
	}{
		{
			name: "sync non-empty src to empty dst",
			srcStructure: map[string]string{
				"file1.txt": "content1",
				"file2.txt": "content2",
			},
			dstStructure: nil,
			del:          false,
			expected: []string{
				"file1.txt",
				"file2.txt",
			},
		},
		{
			name: "sync non-empty src to non-empty dst with no extra files",
			srcStructure: map[string]string{
				"file1.txt": "content1",
				"file2.txt": "content2",
			},
			dstStructure: map[string]string{
				"file1.txt": "old content",
				"file2.txt": "old content",
			},
			del: false,
			expected: []string{
				"file1.txt",
				"file2.txt",
			},
		},
		{
			name: "sync non-empty src to non-empty dst with extra files and del=false",
			srcStructure: map[string]string{
				"file1.txt": "content1",
			},
			dstStructure: map[string]string{
				"file1.txt": "old content",
				"file2.txt": "old content",
			},
			del: false,
			expected: []string{
				"file1.txt",
			},
		},
		{
			name: "sync non-empty src to non-empty dst with extra files and del=true",
			srcStructure: map[string]string{
				"file1.txt": "content1",
			},
			dstStructure: map[string]string{
				"file1.txt": "old content",
				"file2.txt": "old content",
			},
			del: true,
			expected: []string{
				"file1.txt",
			},
		},
	}

	svc := Local{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srcDir, err := os.MkdirTemp("", "src")
			require.NoError(t, err)
			defer os.RemoveAll(srcDir)

			dstDir, err := os.MkdirTemp("", "dst")
			require.NoError(t, err)
			defer os.RemoveAll(dstDir)

			for name, content := range tc.srcStructure {
				err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0o644)
				require.NoError(t, err)
			}

			for name, content := range tc.dstStructure {
				err := os.WriteFile(filepath.Join(dstDir, name), []byte(content), 0o644)
				require.NoError(t, err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			copiedFiles, err := svc.Sync(ctx, srcDir, dstDir, tc.del)
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.expected, copiedFiles)

			for _, name := range tc.expected {
				srcData, err := os.ReadFile(filepath.Join(srcDir, name))
				require.NoError(t, err)

				dstData, err := os.ReadFile(filepath.Join(dstDir, name))
				require.NoError(t, err)

				assert.Equal(t, srcData, dstData, "content should match for file: %s", name)
			}

			// if del is true, verify that extra files in the dst directory have been deleted
			if tc.del {
				dstFiles, err := os.ReadDir(dstDir)
				require.NoError(t, err)

				extraFiles := make(map[string]struct{}, len(tc.dstStructure))
				for name := range tc.dstStructure {
					extraFiles[name] = struct{}{}
				}
				for _, name := range tc.expected {
					delete(extraFiles, name)
				}

				for _, fileInfo := range dstFiles {
					_, ok := extraFiles[fileInfo.Name()]
					assert.False(t, ok, "extra file %s should have been deleted", fileInfo.Name())
				}
			}
		})
	}
}

func TestDelete(t *testing.T) {
	testCases := []struct {
		name        string
		recursive   bool
		isDir       bool
		expectError bool
	}{
		{
			name:      "successful delete file with recursive=false",
			isDir:     false,
			recursive: false,
		},
		{
			name:      "successful delete directory with recursive=true",
			isDir:     true,
			recursive: true,
		},
		{
			name:        "failed delete directory with recursive=false",
			isDir:       true,
			recursive:   false,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var remoteFile string
			if tc.isDir {
				remoteFile, err := os.MkdirTemp("", "test")
				require.NoError(t, err)

				subFile, err := os.CreateTemp(remoteFile, "subfile")
				require.NoError(t, err)
				subFile.Close()
			} else {
				tempFile, err := os.CreateTemp("", "test")
				require.NoError(t, err)
				tempFile.Close()
				remoteFile = tempFile.Name()
			}

			l := &Local{}
			err := l.Delete(context.Background(), remoteFile, tc.recursive)

			if tc.expectError {
				assert.Error(t, err, "expected an error")
			} else {
				assert.NoError(t, err, "unexpected error")

				_, err := os.Stat(remoteFile)
				assert.True(t, os.IsNotExist(err), "remote file should be deleted")
			}
		})
	}
}
