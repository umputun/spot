package executor

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
			{
				out, err := l.Run(context.Background(), tc.cmd, false)
				if tc.expectError {
					assert.Error(t, err, "expected an error")
					return
				}
				assert.NoError(t, err, "unexpected error")
				require.Equal(t, 1, len(out), "output should have exactly one line")
				assert.Equal(t, "Hello, World!", out[0], "output line should match expected value")
			}
			{
				out, err := l.Run(context.Background(), tc.cmd, true)
				if tc.expectError {
					assert.Error(t, err, "expected an error")
					return
				}
				assert.NoError(t, err, "unexpected error")
				require.Equal(t, 1, len(out), "output should have exactly one line")
				assert.Equal(t, "Hello, World!", out[0], "output line should match expected value")
			}

		})
	}
}

func TestUploadAndDownload(t *testing.T) {
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

	// we want to test both upload and download, so we create a function type. those functions should do the same thing
	type fn func(ctx context.Context, src, dst string, mkdir bool) (err error)
	l := &Local{}
	fns := []struct {
		name string
		fn   fn
	}{{"upload", l.Upload}, {"download", l.Download}}

	for _, tc := range testCases {
		for _, fn := range fns {
			t.Run(tc.name+"#"+fn.name, func(t *testing.T) {
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

				err = fn.fn(context.Background(), srcFile.Name(), dstFile, tc.mkdir)

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
				err = os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0o644)
				require.NoError(t, err)
			}

			for name, content := range tc.dstStructure {
				err = os.WriteFile(filepath.Join(dstDir, name), []byte(content), 0o644)
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
			var err error
			if tc.isDir {
				remoteFile, err = os.MkdirTemp("", "test")
				require.NoError(t, err)

				subFile, e := os.CreateTemp(remoteFile, "subfile")
				require.NoError(t, e)
				subFile.Close()
			} else {
				tempFile, e := os.CreateTemp("", "test")
				require.NoError(t, e)
				tempFile.Close()
				remoteFile = tempFile.Name()
			}

			l := &Local{}
			err = l.Delete(context.Background(), remoteFile, tc.recursive)
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

func TestClose(t *testing.T) {
	l := &Local{}
	err := l.Close()
	assert.NoError(t, err, "unexpected error")
}

func TestLocal_syncSrcToDst_InvalidSrcPath(t *testing.T) {
	l := &Local{}
	src := "non_existent_path"
	dst, err := os.MkdirTemp("", "dst")
	require.NoError(t, err)
	defer os.RemoveAll(dst)

	_, err = l.syncSrcToDst(context.Background(), src, dst)
	assert.Error(t, err, "expected an error")
}

func TestLocal_removeExtraDstFiles_InvalidDstPath(t *testing.T) {
	l := &Local{}
	src, err := os.MkdirTemp("", "src")
	require.NoError(t, err)
	defer os.RemoveAll(src)

	dst := "non_existent_path"

	err = l.removeExtraDstFiles(context.Background(), src, dst)
	assert.Error(t, err, "expected an error")
}

func TestUpload_SpecialCharacterInPath(t *testing.T) {
	l := &Local{}
	srcFile, err := os.CreateTemp("", "src")
	require.NoError(t, err)
	defer os.Remove(srcFile.Name())

	dstDir, err := os.MkdirTemp("", "dst")
	require.NoError(t, err)
	defer os.RemoveAll(dstDir)

	dstFile := filepath.Join(dstDir, "file_with_special_#_character.txt")

	err = l.Upload(context.Background(), srcFile.Name(), dstFile, true)
	assert.NoError(t, err, "unexpected error")

	dstContent, err := os.ReadFile(dstFile)
	require.NoError(t, err)
	assert.Equal(t, "", string(dstContent), "uploaded content should match source content")
}

func TestLocalCopyFile(t *testing.T) {
	l := &Local{}

	t.Run("happy path", func(t *testing.T) {
		// create a temporary directory
		tmpDir, err := os.MkdirTemp("", "copy_file_test")
		assert.NoError(t, err, "creating a temporary directory should not return an error")
		defer os.RemoveAll(tmpDir)

		src := filepath.Join(tmpDir, "source_file.txt")
		dst := filepath.Join(tmpDir, "destination_file.txt")

		// create a source file
		err = os.WriteFile(src, []byte("content"), 0o644)
		assert.NoError(t, err, "creating a source file should not return an error")

		// call copyFile
		err = l.copyFile(src, dst)
		assert.NoError(t, err, "copying an existing source file should not return an error")

		// check if the destination file was created and has the correct content
		content, err := os.ReadFile(dst)
		assert.NoError(t, err, "reading the destination file should not return an error")
		assert.Equal(t, "content", string(content), "destination file content should be the same as the source file")

		// check if the destination file has the same permissions as the source file
		srcInfo, err := os.Stat(src)
		assert.NoError(t, err, "getting source file info should not return an error")

		dstInfo, err := os.Stat(dst)
		assert.NoError(t, err, "getting destination file info should not return an error")

		assert.Equal(t, srcInfo.Mode(), dstInfo.Mode(), "destination file permissions should be the same as the source file")
	})

	t.Run("nonexistent source file", func(t *testing.T) {
		// create a temporary directory
		tmpDir, err := os.MkdirTemp("", "copy_file_test")
		assert.NoError(t, err, "creating a temporary directory should not return an error")
		defer os.RemoveAll(tmpDir)

		src := filepath.Join(tmpDir, "nonexistent_file.txt")
		dst := filepath.Join(tmpDir, "destination_file.txt")

		// call copyFile
		err = l.copyFile(src, dst)
		assert.ErrorContains(t, err, "nonexistent_file.txt: no such file or directory",
			"copying a nonexistent source file should return an error")
	})

	t.Run("cannot create destination file", func(t *testing.T) {
		// create a temporary directory
		tmpDir, err := os.MkdirTemp("", "copy_file_test")
		assert.NoError(t, err, "creating a temporary directory should not return an error")
		defer os.RemoveAll(tmpDir)

		src := filepath.Join(tmpDir, "source_file.txt")
		dst := filepath.Join(tmpDir, "destination_dir", "destination_file.txt")

		// create a source file
		err = os.WriteFile(src, []byte("content"), 0o644)
		assert.NoError(t, err, "creating a source file should not return an error")

		err = l.copyFile(src, dst)
		assert.ErrorContains(t, err, "destination_file.txt: no such file or directory",
			"creating a destination file in a nonexistent directory should return an error")
	})

	t.Run("error during chmod", func(t *testing.T) {
		// create a temporary directory
		tmpDir, err := os.MkdirTemp("", "copy_file_test")
		assert.NoError(t, err, "creating a temporary directory should not return an error")
		defer os.RemoveAll(tmpDir)

		src := filepath.Join(tmpDir, "source_file.txt")
		dst := filepath.Join(tmpDir, "destination_file.txt")

		// create a source file
		err = os.WriteFile(src, []byte("content"), 0o644)
		assert.NoError(t, err, "creating a source file should not return an error")

		// call copyFile
		err = l.copyFile(src, dst)
		assert.NoError(t, err, "copying an existing source file should not return an error")

		// remove write permission from the destination file
		err = os.Chmod(dst, 0o444)
		assert.NoError(t, err, "changing permissions of the destination file should not return an error")

		// call copyFile again
		err = l.copyFile(src, dst)
		assert.ErrorContains(t, err, "destination_file.txt: permission denied",
			"copying to a read-only destination file should return an error")
	})
}
