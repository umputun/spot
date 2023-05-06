package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	ctx := context.Background()
	l := &Local{}

	t.Run("single line out success", func(t *testing.T) {
		out, e := l.Run(ctx, "echo 'hello world'", false)
		require.NoError(t, e)
		assert.Equal(t, []string{"hello world"}, out)
	})

	t.Run("single line out fail", func(t *testing.T) {
		_, e := l.Run(ctx, "nonexistent-command", false)
		require.Error(t, e)
	})

	t.Run("multi line out success", func(t *testing.T) {
		// Prepare the test environment
		_, err := l.Run(ctx, "mkdir -p /tmp/st", true)
		require.NoError(t, err)
		_, err = l.Run(ctx, "cp testdata/data1.txt /tmp/st/data1.txt", true)
		require.NoError(t, err)
		_, err = l.Run(ctx, "cp testdata/data2.txt /tmp/st/data2.txt", true)
		require.NoError(t, err)

		out, err := l.Run(ctx, "ls -1 /tmp/st", false)
		require.NoError(t, err)
		assert.Equal(t, 2, len(out))
		assert.Equal(t, "data1.txt", out[0])
		assert.Equal(t, "data2.txt", out[1])
	})

	t.Run("multi line out fail", func(t *testing.T) {
		_, err := l.Run(ctx, "nonexistent-command", false)
		require.Error(t, err)
	})

	t.Run("find out", func(t *testing.T) {
		out, e := l.Run(ctx, "find /tmp/st -type f", true)
		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Contains(t, out, "/tmp/st/data1.txt")
		assert.Contains(t, out, "/tmp/st/data2.txt")
	})

	t.Run("with secrets", func(t *testing.T) {
		originalStdout := os.Stdout
		reader, writer, _ := os.Pipe()
		os.Stdout = writer

		// Set up the test environment
		l.SetSecrets([]string{"data2"})
		defer l.SetSecrets(nil)
		out, e := l.Run(ctx, "find /tmp/st -type f", true)
		writer.Close()
		os.Stdout = originalStdout

		capturedStdout, err := io.ReadAll(reader)
		require.NoError(t, err)

		require.NoError(t, e)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		assert.Equal(t, []string{"/tmp/st/data1.txt", "/tmp/st/data2.txt"}, out)
		t.Logf("capturedStdout: %s", capturedStdout)
		assert.NotContains(t, string(capturedStdout), "data2", "captured stdout should not contain secrets")
		assert.Contains(t, string(capturedStdout), "****", "captured stdout should contain masked secrets")
	})
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

func TestUploadDownloadWithGlob(t *testing.T) {
	// create some temporary test files with content
	tmpDir, err := os.MkdirTemp("", "test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	data1File := filepath.Join(tmpDir, "data1.txt")
	err = os.WriteFile(data1File, []byte("data1 content"), 0o644)
	require.NoError(t, err)

	data2File := filepath.Join(tmpDir, "data2.txt")
	err = os.WriteFile(data2File, []byte("data2 content"), 0o644)
	require.NoError(t, err)

	// create a temporary destination directory
	dstDir, err := os.MkdirTemp("", "dst")
	require.NoError(t, err)
	defer os.RemoveAll(dstDir)

	type fn func(ctx context.Context, src, dst string, mkdir bool) (err error)

	l := &Local{}
	fns := []struct {
		name string
		fn   fn
	}{{"upload", l.Upload}}

	for _, tc := range []struct {
		name        string
		src         string
		dst         string
		mkdir       bool
		expectError bool
	}{
		{
			name:  "successful upload with mkdir=true",
			src:   filepath.Join(tmpDir, "*.txt"),
			dst:   dstDir,
			mkdir: true,
		},
		{
			name: "successful upload with mkdir=false",
			src:  filepath.Join(tmpDir, "*.txt"),
			dst:  dstDir,
		},
		{
			name:        "failed upload with non-existent source file",
			src:         filepath.Join(tmpDir, "nonexistent.txt"),
			dst:         dstDir,
			mkdir:       false,
			expectError: true,
		},
		{
			name:        "failed upload with non-existent directory and mkdir=false",
			src:         filepath.Join(tmpDir, "*.txt"),
			dst:         filepath.Join(tmpDir, "nonexistent", "dst"),
			mkdir:       false,
			expectError: true,
		},
		{
			name:        "failed upload with invalid glob pattern",
			src:         filepath.Join(tmpDir, "*.txt["),
			dst:         dstDir,
			mkdir:       false,
			expectError: true,
		},
	} {
		for _, fn := range fns {
			t.Run(fmt.Sprintf("%s#%s", tc.name, fn.name), func(t *testing.T) {
				err := fn.fn(context.Background(), tc.src, tc.dst, tc.mkdir)

				if tc.expectError {
					assert.Error(t, err, "expected an error")
					return
				}

				assert.NoError(t, err, "unexpected error")

				// assert that all files were uploaded
				files, err := os.ReadDir(dstDir)
				require.NoError(t, err)
				assert.Len(t, files, 2, "unexpected number of uploaded files")

				// assert that the contents of the uploaded files match the contents of the source files
				for _, f := range files {
					dstContent, err := os.ReadFile(filepath.Join(dstDir, f.Name()))
					require.NoError(t, err)
					assert.Equal(t, fmt.Sprintf("data%d content", f.Name()[4]-'0'), string(dstContent),
						"uploaded content should match source content")
				}
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

func TestSyncSrcToDst_UnhappyPath(t *testing.T) {
	l := &Local{}

	t.Run("context canceled", func(t *testing.T) {
		tmpSrcDir, err := os.MkdirTemp("", "src")
		assert.NoError(t, err, "creating a temporary source directory should not return an error")
		defer os.RemoveAll(tmpSrcDir)

		tmpDstDir, err := os.MkdirTemp("", "dst")
		assert.NoError(t, err, "creating a temporary destination directory should not return an error")
		defer os.RemoveAll(tmpDstDir)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = l.syncSrcToDst(ctx, tmpSrcDir, tmpDstDir)
		assert.Error(t, err, "syncSrcToDst should return an error when the context is canceled")
	})

	t.Run("error while walking source directory", func(t *testing.T) {
		invalidSrcPath := "invalid-src-path"
		tmpDstDir, err := os.MkdirTemp("", "dst")
		assert.NoError(t, err, "creating a temporary destination directory should not return an error")
		defer os.RemoveAll(tmpDstDir)

		_, err = l.syncSrcToDst(context.Background(), invalidSrcPath, tmpDstDir)
		assert.Error(t, err, "syncSrcToDst should return an error when there's an error while walking the source directory")
	})
}
