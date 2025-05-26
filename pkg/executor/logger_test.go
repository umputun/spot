package executor

import (
	"bufio"
	"bytes"
	"io"
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

			writer := &stdOutLogWriter{
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
			name:     "INFO no host name",
			prefix:   "INFO",
			hostAddr: "localhost",
			input:    "This is a test message\nThis is another test message",
			expectedLines: []string{
				"[localhost] INFO This is a test message",
				"[localhost] INFO This is another test message",
			},
		},
		{
			name:     "INFO with host name",
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
			name:     "host name and secrets",
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
			name:     "no host name, no prefix",
			prefix:   "",
			hostAddr: "localhost",
			input:    "This is a test message\nThis is another test message",
			expectedLines: []string{
				"[localhost] This is a test message",
				"[localhost] This is another test message",
			},
		},
		{
			name:         "set host name using WithHost",
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
			name:         "set host name and matching host addr using WithHost",
			prefix:       "",
			hostAddr:     "localhost",
			input:        "This is a test message\nThis is another test message",
			withHostName: "127.0.0.1",
			withHostAddr: "127.0.0.1:80",
			expectedLines: []string{
				"[127.0.0.1:80] This is a test message",
				"[127.0.0.1:80] This is another test message",
			},
		},
		{
			name:     "with host name, no prefix",
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
			var writer LogWriter
			buffer := bytes.NewBuffer([]byte{})
			writer = &colorizedWriter{wr: buffer, prefix: tc.prefix,
				hostAddr: tc.hostAddr, hostName: tc.hostName, secrets: tc.secrets}
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
		out := captureStdOut(t, func() {
			logs := MakeLogs(true, false, nil)
			io.WriteString(logs.Out.WithHost(hostAddr, "host1"), outMsg)
			io.WriteString(logs.Err.WithHost(hostAddr, "host2"), errMsg)
		})
		t.Logf("bufOut:\n%s", out)
		assert.Contains(t, out, "[host1 example.com]  > Hello, out!", "captured stdout should contain the out message")
		assert.Contains(t, out, "[host2 example.com]  ! Hello, err!", "captured stderr should contain the err message")
	})

	t.Run("non-verbose", func(t *testing.T) {
		logs := MakeLogs(false, false, nil)
		bufOut := bytes.Buffer{}
		log.SetOutput(&bufOut)
		io.WriteString(logs.Out.WithHost(hostAddr, "host1"), outMsg)
		io.WriteString(logs.Err.WithHost(hostAddr, "host2"), errMsg)

		t.Logf("bufOut:\n%s", bufOut.String())
		assert.Contains(t, bufOut.String(), "[DEBUG]  > Hello, out!", "captured stdout should contain the out message")
		assert.Contains(t, bufOut.String(), "[WARN]  ! Hello, err!", "captured stderr should contain the err message")
	})

	t.Run("with secrets, non-verbose", func(t *testing.T) {
		logs := MakeLogs(false, false, []string{"Hello"})
		bufOut := bytes.Buffer{}
		log.SetOutput(&bufOut)
		log.SetOutput(&bufOut)
		io.WriteString(logs.Out.WithHost(hostAddr, "host1"), outMsg)
		io.WriteString(logs.Err.WithHost(hostAddr, "host2"), errMsg)

		t.Logf("bufOut:\n%s", bufOut.String())
		assert.Contains(t, bufOut.String(), "[DEBUG]  > ****, out!", "captured stdout should contain the out message")
		assert.Contains(t, bufOut.String(), "[WARN]  ! ****, err!", "captured stderr should contain the err message")
	})

	t.Run("with secrets, verbose", func(t *testing.T) {
		out := captureStdOut(t, func() {
			logs := MakeLogs(true, false, []string{"Hello"})
			io.WriteString(logs.Out.WithHost(hostAddr, "host1"), outMsg)
			io.WriteString(logs.Err.WithHost(hostAddr, "host2"), errMsg)
		})

		t.Logf("bufOut:\n%s", out)
		assert.Contains(t, out, "[host1 example.com]  > ****, out!", "captured stdout should contain the out message")
		assert.Contains(t, out, "[host2 example.com]  ! ****, err!", "captured stderr should contain the err message")

	})
}

func TestMaskSecrets(t *testing.T) {
	tests := []struct {
		input    string
		secrets  []string
		expected string
	}{
		{"myPassword is password", []string{"password"}, "myPassword is ****"},
		{"password password, password withpassword", []string{"password"}, "**** ****, **** withpassword"},
		{"multiple secrets mySecret and myPassword", []string{"mySecret", "myPassword"}, "multiple secrets **** and ****"},
		{"no secrets here", []string{"password"}, "no secrets here"},
		{"edge case:secret", []string{"secret"}, "edge case:****"},
		{"", []string{"password"}, ""},
		{"only spaces ", []string{" "}, "only spaces "},
		{"1234567890 xyz $%^&", []string{"1234567890"}, "**** xyz $%^&"},
		{"secret@domain.com", []string{"secret@domain.com"}, "****"},
		{"key=secret,val=secret", []string{"secret"}, "key=****,val=****"},
		// new test cases for special characters and periods
		{"password123#", []string{"password123#"}, "****"},
		{"password. with period", []string{"password."}, "**** with period"},
		{"special!characters@in#password", []string{"special!characters@in#password"}, "****"},
		{"token321.", []string{"token321."}, "****"},
		// test case from the issue where the script is shown with secrets
		{"secret=\"token321.\"; secret_special=\"password123#\"; echo \"${secret} ${secret_special}\"",
			[]string{"token321.", "password123#"}, "secret=\"****\"; secret_special=\"****\"; echo \"${secret} ${secret_special}\""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			output := maskSecrets(tt.input, tt.secrets)
			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestColorizedWriter_PrintfWithSecrets(t *testing.T) {
	tests := []struct {
		input    string
		secrets  []string
		expected string
	}{
		{"myPassword is password", []string{"password"}, "[my-host localhost] myPassword is ****\n"},
		{"password password, password withpassword", []string{"password"}, "[my-host localhost] **** ****, **** withpassword\n"},
		{"multiple secrets mySecret and myPassword", []string{"mySecret", "myPassword"},
			"[my-host localhost] multiple secrets **** and ****\n"},
		{"no secrets here", []string{"password"}, "[my-host localhost] no secrets here\n"},
		{"edge case:secret", []string{"secret"}, "[my-host localhost] edge case:****\n"},
		{"", []string{"password"}, ""},
		{"only spaces ", []string{" "}, "[my-host localhost] only spaces \n"},
		{"1234567890", []string{"1234567890"}, "[my-host localhost] ****\n"},
		{"secret@domain.com", []string{"secret@domain.com"}, "[my-host localhost] ****\n"},
		{"key=secret,val=secret", []string{"secret"}, "[my-host localhost] key=****,val=****\n"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var buf bytes.Buffer
			writer := &colorizedWriter{wr: &buf, secrets: tt.secrets, hostAddr: "localhost", hostName: "my-host"}
			writer.Printf("%s", tt.input) //nolint
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestIsAlphanumeric(t *testing.T) {
	tests := []struct {
		input  string
		result bool
	}{
		{"password", true},
		{"Password123", true},
		{"user_name", true},
		{"password.", false},
		{"password123#", false},
		{"token!", false},
		{"secret@domain.com", false},
		{"", true},
		{"123", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isAlphanumeric(tt.input)
			assert.Equal(t, tt.result, result)
		})
	}
}
