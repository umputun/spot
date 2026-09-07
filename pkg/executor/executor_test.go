package executor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
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

func TestSplitOutputLines(t *testing.T) {
	tbl := []struct {
		name string
		in   string
		res  []string
	}{
		{"empty", "", nil},
		{"single line, no trailing newline", "hello", []string{"hello"}},
		{"single line with trailing newline", "hello\n", []string{"hello"}},
		{"multiple lines", "line1\nline2\nline3\n", []string{"line1", "line2", "line3"}},
		{"blank line in the middle", "line1\n\nline2\n", []string{"line1", "", "line2"}},
		{"single newline", "\n", []string{""}},
		{"trailing blank line", "line1\n\n", []string{"line1", ""}},
		{"crlf line endings", "line1\r\nline2\r\n", []string{"line1", "line2"}},
	}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.res, splitOutputLines(tt.in))
		})
	}
}

func TestLineCapture(t *testing.T) {
	tbl := []struct {
		name string
		in   string
		res  []string
	}{
		{"empty", "", nil},
		{"single line, no trailing newline", "hello", []string{"hello"}},
		{"single line with trailing newline", "hello\n", []string{"hello"}},
		{"multiple lines", "line1\nline2\nline3\n", []string{"line1", "line2", "line3"}},
		{"blank line in the middle", "line1\n\nline2\n", []string{"line1", "", "line2"}},
		{"single newline", "\n", []string{""}},
		{"trailing blank line", "line1\n\n", []string{"line1", ""}},
		{"crlf line endings", "line1\r\nline2\r\n", []string{"line1", "line2"}},
	}

	// a nil predicate reproduces splitOutputLines whatever the write boundaries are, including a
	// chunk size that splits a line, a crlf pair or a run of newlines
	for _, tt := range tbl {
		for _, chunk := range []int{0, 1, 2, 3, 7} {
			t.Run(fmt.Sprintf("%s/chunk %d", tt.name, chunk), func(t *testing.T) {
				lc := &lineCapture{}
				writeInChunks(t, lc, tt.in, chunk)
				assert.Equal(t, tt.res, lc.result())
				assert.Equal(t, splitOutputLines(tt.in), lc.result(), "result is idempotent and matches the batch split")
			})
		}
	}
}

func TestLineCaptureKeepLine(t *testing.T) {
	t.Run("keeps only accepted lines", func(t *testing.T) {
		lc := &lineCapture{keep: func(line string) bool { return strings.HasPrefix(line, "setvar ") }}
		writeInChunks(t, lc, "noise\nsetvar a=1\nmore noise\nsetvar b=2\n", 3)
		assert.Equal(t, []string{"setvar a=1", "setvar b=2"}, lc.result())
	})

	t.Run("rejecting everything retains nothing", func(t *testing.T) {
		lc := &lineCapture{keep: func(string) bool { return false }}
		writeInChunks(t, lc, "one\ntwo\nthree", 0)
		assert.Nil(t, lc.result())
	})

	t.Run("predicate sees an unterminated final line", func(t *testing.T) {
		var seen []string
		lc := &lineCapture{keep: func(line string) bool { seen = append(seen, line); return true }}
		writeInChunks(t, lc, "first\nlast-no-newline", 4)
		assert.Equal(t, []string{"first"}, seen, "the final line is only offered once result is called")
		assert.Equal(t, []string{"first", "last-no-newline"}, lc.result())
	})

	t.Run("line longer than a scanner token limit survives", func(t *testing.T) {
		long := strings.Repeat("x", 1<<20)
		lc := &lineCapture{}
		writeInChunks(t, lc, long+"\nshort\n", 4096)
		require.Len(t, lc.result(), 2)
		assert.Equal(t, long, lc.result()[0])
		assert.Equal(t, "short", lc.result()[1])
	})
}

// writeInChunks feeds s to w in fixed-size pieces, or in one write when size is 0, so a test can
// pin behavior across the write boundaries a real command produces.
func writeInChunks(t *testing.T, w io.Writer, s string, size int) {
	t.Helper()
	if size <= 0 {
		_, err := w.Write([]byte(s))
		require.NoError(t, err)
		return
	}
	for i := 0; i < len(s); i += size {
		end := min(i+size, len(s))
		_, err := w.Write([]byte(s[i:end]))
		require.NoError(t, err)
	}
}

func TestLineCaptureReleasesLargeStagingBuffer(t *testing.T) {
	// a long line arriving in pieces has to be staged, but once it is consumed the buffer must go:
	// keeping it would pin the longest line for as long as the command runs, which is the retention
	// this type exists to remove
	lc := &lineCapture{keep: func(string) bool { return false }}
	writeInChunks(t, lc, strings.Repeat("x", 4<<20)+"\n", 4096)
	assert.LessOrEqual(t, cap(lc.partial), 64<<10, "the staging buffer is released once the line is consumed")

	writeInChunks(t, lc, "short\n", 2) // still usable for the output that follows
	assert.Nil(t, lc.result())
}
