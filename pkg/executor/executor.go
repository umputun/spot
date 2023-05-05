// Package executor provides an interface for the executor as well as a local and remote implementation.
// The executor is used to run commands on the local machine or on a remote machine.
package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"strings"

	"github.com/fatih/color"
)

// Interface is an interface for the executor.
// Implemented by Remote and Local structs.
type Interface interface {
	Run(ctx context.Context, c string, verbose bool) (out []string, err error)
	Upload(ctx context.Context, local, remote string, mkdir bool) (err error)
	Download(ctx context.Context, remote, local string, mkdir bool) (err error)
	Sync(ctx context.Context, localDir, remoteDir string, del bool) ([]string, error)
	Delete(ctx context.Context, remoteFile string, recursive bool) (err error)
	Close() error
}

// StdOutLogWriter is a writer that writes log with a prefix and a log level.
type StdOutLogWriter struct {
	prefix string
	level  string
}

// NewStdoutLogWriter creates a new StdOutLogWriter.
func NewStdoutLogWriter(prefix, level string) *StdOutLogWriter {
	return &StdOutLogWriter{prefix: prefix, level: level}
}

func (w *StdOutLogWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		log.Printf("[%s] %s %s", w.level, w.prefix, line)
	}
	return len(p), nil
}

// ColorizedWriter is a writer that colorizes the output based on the hostAddr name.
type ColorizedWriter struct {
	wr       io.Writer
	prefix   string
	hostAddr string
	hostName string
}

// NewColorizedWriter creates a new ColorizedWriter with the given hostAddr name.
func NewColorizedWriter(wr io.Writer, prefix, hostAddr, hostName string) *ColorizedWriter {
	return &ColorizedWriter{wr: wr, hostAddr: hostAddr, hostName: hostName, prefix: prefix}
}

// WithHost creates a new StdoutColorWriter with the given hostAddr name.
func (s *ColorizedWriter) WithHost(hostAddr, hostName string) *ColorizedWriter {
	return &ColorizedWriter{wr: s.wr, hostAddr: hostAddr, hostName: hostName, prefix: s.prefix}
}

// Write writes the given byte slice to stdout with the colorized hostAddr prefix for each line.
// If the input does not end with a newline, one is added.
func (s *ColorizedWriter) Write(p []byte) (n int, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		line := scanner.Text()
		hostID := s.hostAddr
		if s.hostName != "" {
			hostID = s.hostName + " " + s.hostAddr
		}
		formattedOutput := fmt.Sprintf("[%s] %s %s", hostID, s.prefix, line)
		if s.prefix == "" {
			formattedOutput = fmt.Sprintf("[%s] %s", hostID, line)
		}
		colorizer := hostColorizer(s.hostAddr)
		colorizedOutput := colorizer("%s\n", formattedOutput)
		_, err = io.WriteString(s.wr, colorizedOutput)
		if err != nil {
			return 0, err
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// hostColorizer returns a function that formats a string with a color based on the hostAddr name.
func hostColorizer(host string) func(format string, a ...interface{}) string {
	colors := []color.Attribute{
		color.FgHiRed, color.FgHiGreen, color.FgHiYellow,
		color.FgHiBlue, color.FgHiMagenta, color.FgHiCyan,
		color.FgRed, color.FgGreen, color.FgYellow,
		color.FgBlue, color.FgMagenta, color.FgCyan,
	}
	i := crc32.ChecksumIEEE([]byte(host)) % uint32(len(colors))
	return color.New(colors[i]).SprintfFunc()
}

// MakeOutAndErrWriters creates a new StdoutLogWriter and StdoutLogWriter for the given hostAddr.
func MakeOutAndErrWriters(hostAddr, hostName string, verbose bool) (outWr, errWr io.Writer) {
	var outLog, errLog io.Writer
	if verbose {
		outLog = NewColorizedWriter(os.Stdout, " >", hostAddr, hostName)
		errLog = NewColorizedWriter(os.Stdout, " !", hostAddr, hostName)
	} else {
		outLog = NewStdoutLogWriter(" >", "DEBUG")
		errLog = NewStdoutLogWriter(" !", "WARN")
	}
	return outLog, errLog
}
