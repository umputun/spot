package executor

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_isExcluded(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		isDir    bool
		excl     []string
		expected bool
	}{
		{"exact match", "test.txt", false, []string{"test.txt"}, true},
		{"glob match", "test.txt", false, []string{"*.txt"}, true},
		{"no match", "test.txt", false, []string{"*.jpg"}, false},
		{"invalid pattern", "test.txt", false, []string{"["}, false},
		{"empty exclusion list", "test.txt", false, []string{}, false},
		{"empty path", "", false, []string{"*.txt"}, false},
		{"directory exclusion", "folder/test.txt", false, []string{"folder/*"}, true},
		{"directory exclusion without wildcard", "folder/test.txt", false, []string{"folder"}, true},
		{"recursive exclusion", "folder/subfolder/test.txt", false, []string{"folder/*"}, true},
		{"recursive exclusion without wildcard", "folder/subfolder/test.txt", false, []string{"folder"}, true},
		{"partial match", "folder/test.txt", false, []string{"folder/*test.txt"}, true},
		{"non-wildcard match", "folder/test.txt", false, []string{"folder/"}, false},
		{"match with ? wildcard", "test.txt", false, []string{"t?st.txt"}, true},
		{"match with multiple wildcards", "folder/subfolder/test.txt", false, []string{"folder/*/test.txt"}, true},
		{"case sensitivity", "Test.txt", false, []string{"test.txt"}, false},
		{"glob directory exclusion matches directory itself", "dir1", true, []string{"dir*/*"}, true},
		{"glob directory exclusion matches nested file", "dir1/keep.txt", false, []string{"dir*/*"}, true},
		{"glob directory exclusion no match", "other", false, []string{"dir*/*"}, false},
		{"glob directory pattern does not exclude plain file", "data.txt", false, []string{"da*/*"}, false},
		{"glob directory pattern does not match same-name file", "dir1", false, []string{"dir*/*"}, false},
		{"plain directory pattern protects directory", "folder", true, []string{"folder/*"}, true},
		{"windows path separators normalized", `folder\test.txt`, false, []string{"folder/*"}, true},
		{"windows exclude pattern normalized", "folder/test.txt", false, []string{`folder\*`}, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isExcluded(tc.path, tc.isDir, tc.excl)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func Test_isExcludedSubPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		excl     []string
		expected bool
	}{

		{"sub-path excluded", "/user/docs", []string{"/user/docs/123"}, true},
		{"sub-path with case sensitivity", "/user/docs", []string{"/user/Docs/123"}, false},
		{"sub-path not excluded", "/user/docs", []string{"/user/docs2/123"}, false},
		{"sub-path with empty exclusion", "/user/docs", []string{""}, false},
		{"ancestor of deeply nested exclusion", "/user", []string{"/user/docs/123"}, true},
		{"root is ancestor of any exclusion", ".", []string{"logs/keep.log"}, true},
		{"root with empty exclusion list", ".", []string{}, false},
		{"intermediate dir of nested exclusion", "logs", []string{"logs/archive/keep.log"}, true},
		{"sibling of nested exclusion", "data", []string{"logs/archive/keep.log"}, false},
		{"same path as exclusion is not ancestor", "logs/keep.log", []string{"logs/keep.log"}, false},
		{"glob segment in exclusion", "dir1", []string{"dir*/keep.log"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isExcludedSubPath(tt.path, tt.excl))
		})
	}
}

func Test_isWithinOneSecond(t *testing.T) {
	now := time.Now()
	testCases := []struct {
		name     string
		t1       time.Time
		t2       time.Time
		expected bool
	}{
		{"exact same time", now, now, true},
		{"within one second", now, now.Add(500 * time.Millisecond), true},
		{"exactly one second apart", now, now.Add(1 * time.Second), true},
		{"more than one second apart", now, now.Add(2 * time.Second), false},
		{"negative difference within one second", now, now.Add(-500 * time.Millisecond), true},
		{"negative difference exactly one second", now, now.Add(-1 * time.Second), true},
		{"negative difference more than one second", now, now.Add(-2 * time.Second), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isWithinOneSecond(tc.t1, tc.t2)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

// captureStdOut captures the output of a function that writes to stdout.
func captureStdOut(t *testing.T, f func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}
