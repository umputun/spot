package executor

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStdOutLogWriter(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		level         string
		input         string
		secrets       []string
		expectedLines []string
	}{
		{
			name:   "basic test",
			prefix: "PREFIX",
			level:  "INFO",
			input:  "Hello\nWorld\n",
			expectedLines: []string{
				"[INFO] PREFIX Hello",
				"[INFO] PREFIX World",
			},
		},
		{
			name:    "with secrets",
			prefix:  "PREFIX",
			level:   "INFO",
			input:   "Hello secret1\nWorld secret1 secret2 secret2 secret3 blah\n",
			secrets: []string{"secret1", "secret2"},
			expectedLines: []string{
				"[INFO] PREFIX Hello ****",
				"[INFO] PREFIX World **** **** **** secret3 blah",
			},
		},
		{
			name:    "with empty secrets",
			prefix:  "PREFIX",
			level:   "INFO",
			input:   "Hello secret1\nWorld secret1 secret2 secret2 secret3 blah\n",
			secrets: []string{" ", ""},
			expectedLines: []string{
				"[INFO] PREFIX Hello secret1",
				"[INFO] PREFIX World secret1 secret2 secret2 secret3 blah",
			},
		},
		{
			name:          "empty input",
			prefix:        "PREFIX",
			level:         "INFO",
			input:         "",
			expectedLines: []string{},
		},
		{
			name:   "different log level",
			prefix: "PREFIX",
			level:  "WARN",
			input:  "Warning message\n",
			expectedLines: []string{
				"[WARN] PREFIX Warning message",
			},
		},
		{
			name:   "multiple lines",
			prefix: "APP",
			level:  "DEBUG",
			input:  "Line 1\nLine 2\nLine 3\n",
			expectedLines: []string{
				"[DEBUG] APP Line 1",
				"[DEBUG] APP Line 2",
				"[DEBUG] APP Line 3",
			},
		},
		{
			name:   "trailing empty line",
			prefix: "PREFIX",
			level:  "INFO",
			input:  "Hello\nWorld\n\n",
			expectedLines: []string{
				"[INFO] PREFIX Hello",
				"[INFO] PREFIX World",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			log.SetFlags(0)

			writer := &StdOutLogWriter{
				prefix:  tc.prefix,
				level:   tc.level,
				secrets: tc.secrets,
			}

			n, err := writer.Write([]byte(tc.input))
			assert.NoError(t, err, "write() should not return an error")
			assert.Equal(t, len(tc.input), n, "write() should return the number of bytes written")

			var lines []string
			if buf.Len() > 0 {
				lines = strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			} else {
				lines = []string{}
			}

			assert.Equalf(t, len(tc.expectedLines), len(lines),
				"number of lines in the output should match the expected number: %v", lines)

			for i, expectedLine := range tc.expectedLines {
				assert.Equal(t, expectedLine, lines[i], "the output line should match the expected line")
			}
		})
	}
}

func TestColorizedWriter(t *testing.T) {
	testCases := []struct {
		name          string
		prefix        string
		hostAddr      string
		hostName      string
		input         string
		withHostAddr  string
		withHostName  string
		secrets       []string
		expectedLines []string
	}{
		{
			name:     "WithPrefix no host name",
			prefix:   "INFO",
			hostAddr: "localhost",
			input:    "This is a test message\nThis is another test message",
			expectedLines: []string{
				"[localhost] INFO This is a test message",
				"[localhost] INFO This is another test message",
			},
		},
		{
			name:     "WithPrefix with host name",
			prefix:   "INFO",
			hostAddr: "localhost",
			hostName: "my-host",
			input:    "This is a test message\nThis is another test message",
			expectedLines: []string{
				"[my-host localhost] INFO This is a test message",
				"[my-host localhost] INFO This is another test message",
			},
		},
		{
			name:     "WithPrefix with host name and secrets",
			prefix:   "INFO",
			hostAddr: "localhost",
			hostName: "my-host",
			input:    "This is a test message\nThis is another test message",
			secrets:  []string{"another", "message", "secret"},
			expectedLines: []string{
				"[my-host localhost] INFO This is a test ****",
				"[my-host localhost] INFO This is **** test ****",
			},
		},
		{
			name:     "WithoutPrefix no host name",
			prefix:   "",
			hostAddr: "localhost",
			input:    "This is a test message\nThis is another test message",
			expectedLines: []string{
				"[localhost] This is a test message",
				"[localhost] This is another test message",
			},
		},
		{
			name:         "WithoutPrefix, set host name",
			prefix:       "",
			hostAddr:     "localhost",
			input:        "This is a test message\nThis is another test message",
			withHostName: "my-host",
			withHostAddr: "127.0.0.1",
			expectedLines: []string{
				"[my-host 127.0.0.1] This is a test message",
				"[my-host 127.0.0.1] This is another test message",
			},
		},
		{
			name:     "WithoutPrefix with host name",
			prefix:   "",
			hostAddr: "localhost",
			hostName: "my-host",
			input:    "This is a test message\nThis is another test message",
			expectedLines: []string{
				"[my-host localhost] This is a test message",
				"[my-host localhost] This is another test message",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buffer := bytes.NewBuffer([]byte{})
			writer := NewColorizedWriter(buffer, tc.prefix, tc.hostAddr, tc.hostName, tc.secrets)
			if tc.withHostName != "" && tc.withHostAddr != "" {
				writer = writer.WithHost(tc.withHostAddr, tc.withHostName)
			}
			_, err := writer.Write([]byte(tc.input))
			assert.NoError(t, err)

			scanner := bufio.NewScanner(buffer)
			lineIndex := 0

			for scanner.Scan() {
				assert.Contains(t, scanner.Text(), tc.expectedLines[lineIndex])
				lineIndex++
			}

			assert.NoError(t, scanner.Err())
			assert.Equal(t, len(tc.expectedLines), lineIndex)
		})
	}
}

func TestMakeOutAndErrWriters(t *testing.T) {
	hostAddr := "example.com"
	outMsg := "Hello, out!"
	errMsg := "Hello, err!"

	t.Run("verbose", func(t *testing.T) {
		originalStdout := os.Stdout

		rOut, wOut, _ := os.Pipe()
		os.Stdout = wOut

		outWriter, errWriter := MakeOutAndErrWriters(hostAddr, "", true, nil)
		io.WriteString(outWriter, outMsg)
		io.WriteString(errWriter, errMsg)

		wOut.Close()
		os.Stdout = originalStdout

		var bufOut bytes.Buffer
		io.Copy(&bufOut, rOut)

		t.Logf("bufOut:\n%s", bufOut.String())
		assert.Contains(t, bufOut.String(), "[example.com]  > Hello, out!", "captured stdout should contain the out message")
		assert.Contains(t, bufOut.String(), "[example.com]  ! Hello, err!", "captured stderr should contain the err message")
	})

	t.Run("non-verbose", func(t *testing.T) {
		outWriter, errWriter := MakeOutAndErrWriters(hostAddr, "", false, nil)
		bufOut := bytes.Buffer{}
		log.SetOutput(&bufOut)
		io.WriteString(outWriter, outMsg)
		io.WriteString(errWriter, errMsg)

		t.Logf("bufOut:\n%s", bufOut.String())
		assert.Contains(t, bufOut.String(), "[DEBUG]  > Hello, out!", "captured stdout should contain the out message")
		assert.Contains(t, bufOut.String(), "[WARN]  ! Hello, err!", "captured stderr should contain the err message")
	})

	t.Run("with secrets", func(t *testing.T) {
		outWriter, errWriter := MakeOutAndErrWriters(hostAddr, "", false, []string{"Hello"})
		bufOut := bytes.Buffer{}
		log.SetOutput(&bufOut)
		io.WriteString(outWriter, outMsg)
		io.WriteString(errWriter, errMsg)

		t.Logf("bufOut:\n%s", bufOut.String())
		assert.Contains(t, bufOut.String(), "[DEBUG]  > ****, out!", "captured stdout should contain the out message")
		assert.Contains(t, bufOut.String(), "[WARN]  ! ****, err!", "captured stderr should contain the err message")
	})
}

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
		{"invalid pattern", "test.txt", []string{"["}, false}, // invalid pattern
		{"empty exclusion list", "test.txt", []string{}, false},
		{"empty path", "", []string{"*.txt"}, false},
		{"directory exclusion", "folder/test.txt", []string{"folder/*"}, true},
		{"partial match", "folder/test.txt", []string{"folder/*test.txt"}, true},
		{"non-wildcard match", "folder/test.txt", []string{"folder/"}, false},
		{"match with ? wildcard", "test.txt", []string{"t?st.txt"}, true},
		{"match with multiple wildcards", "folder/subfolder/test.txt", []string{"folder/*/test.txt"}, true},
		{"case sensitivity", "Test.txt", []string{"test.txt"}, false}, // assumes a case-sensitive file system
		{"recursive exclusion", "folder/subfolder/test.txt", []string{"folder/*"}, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isExcluded(tc.path, tc.excl)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
