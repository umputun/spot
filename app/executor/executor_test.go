package executor

import (
	"bytes"
	"log"
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

			writer := &stdOutLogWriter{
				prefix: tc.prefix,
				level:  tc.level,
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
