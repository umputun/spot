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
		excl     []string
		expected bool
	}{
		{"exact match", "test.txt", []string{"test.txt"}, true},
		{"glob match", "test.txt", []string{"*.txt"}, true},
		{"no match", "test.txt", []string{"*.jpg"}, false},
		{"invalid pattern", "test.txt", []string{"["}, false},
		{"empty exclusion list", "test.txt", []string{}, false},
		{"empty path", "", []string{"*.txt"}, false},
		{"directory exclusion", "folder/test.txt", []string{"folder/*"}, true},
		{"directory exclusion without wildcard", "folder/test.txt", []string{"folder"}, true},
		{"recursive exclusion", "folder/subfolder/test.txt", []string{"folder/*"}, true},
		{"recursive exclusion without wildcard", "folder/subfolder/test.txt", []string{"folder"}, true},
		{"partial match", "folder/test.txt", []string{"folder/*test.txt"}, true},
		{"non-wildcard match", "folder/test.txt", []string{"folder/"}, false},
		{"match with ? wildcard", "test.txt", []string{"t?st.txt"}, true},
		{"match with multiple wildcards", "folder/subfolder/test.txt", []string{"folder/*/test.txt"}, true},
		{"case sensitivity", "Test.txt", []string{"test.txt"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isExcluded(tc.path, tc.excl)
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
