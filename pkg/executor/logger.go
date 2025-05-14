package executor

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/fatih/color"
)

// LogWriter is an interface for writing logs.
// Some implementations support colorization and secrets masking.
type LogWriter interface {
	io.Writer
	Printf(format string, v ...any)
	WithHost(hostAddr, hostName string) LogWriter
	WithWriter(wr io.Writer) LogWriter
}

// Logs is a struct that contains two LogWriters, one for stdout and one for stderr.
type Logs struct {
	Info LogWriter
	Out  LogWriter
	Err  LogWriter

	verbose    bool
	secrets    []string
	monochrome bool
}

// WithHost creates a new Logs with the given hostAddr name for each LogWriter.
func (l Logs) WithHost(hostAddr, hostName string) Logs {
	return Logs{
		Info: l.Info.WithHost(hostAddr, hostName),
		Out:  l.Out.WithHost(hostAddr, hostName),
		Err:  l.Err.WithHost(hostAddr, hostName),
	}
}

// WithSecrets creates a new Logs with the given secrets.
func (l Logs) WithSecrets(secrets []string) Logs {
	return MakeLogs(l.verbose, l.monochrome, secrets)
}

// ColorizedWriter is a writer that colorizes the output based on the hostAddr name.
type colorizedWriter struct {
	wr         io.Writer
	prefix     string
	hostAddr   string
	hostName   string
	secrets    []string
	monochrome bool
}

// WithHost creates a new StdoutColorWriter with the given hostAddr name.
func (s *colorizedWriter) WithHost(hostAddr, hostName string) LogWriter {
	if strings.HasPrefix(hostAddr, hostName+":") {
		// in case if we don't have hostName it was set to hostAddr without port
		// we want to prevent log prefix duplication, i.e. [dev1.umputun.dev dev1.umputun.dev:22]
		hostName = ""
	}
	return &colorizedWriter{wr: s.wr, hostAddr: hostAddr, hostName: hostName,
		prefix: s.prefix, secrets: s.secrets, monochrome: s.monochrome}
}

func (s *colorizedWriter) WithWriter(wr io.Writer) LogWriter {
	return &colorizedWriter{wr: wr, hostAddr: s.hostAddr, hostName: s.hostName,
		prefix: s.prefix, secrets: s.secrets, monochrome: s.monochrome}
}

// Printf writes the given text to io.Writer with the colorized hostAddr prefix.
func (s *colorizedWriter) Printf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	msg = maskSecrets(msg, s.secrets)
	_, _ = fmt.Fprint(s, msg)
}

// Write writes the given byte slice to stdout with the colorized hostAddr prefix for each line.
// If the input does not end with a newline, one is added.
func (s *colorizedWriter) Write(p []byte) (n int, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		line := scanner.Text()
		hostID := s.hostAddr
		if s.hostName != "" {
			hostID = s.hostName + " " + s.hostAddr
		}
		formattedOutput := fmt.Sprintf("[%s] %s %s", hostID, s.prefix, line)
		formattedOutput = maskSecrets(formattedOutput, s.secrets)

		if s.prefix == "" {
			formattedOutput = fmt.Sprintf("[%s] %s", hostID, line)
		}
		colorizer := s.hostColorizer(s.hostAddr)
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
func (s *colorizedWriter) hostColorizer(host string) func(format string, a ...interface{}) string {
	colors := []color.Attribute{
		color.FgHiRed, color.FgHiGreen, color.FgHiYellow,
		color.FgHiBlue, color.FgHiMagenta, color.FgHiCyan,
		color.FgRed, color.FgGreen, color.FgYellow,
		color.FgBlue, color.FgMagenta, color.FgCyan,
	}
	i := int(crc32.ChecksumIEEE([]byte(host))) % len(colors)
	c := colors[i]
	if s.monochrome {
		c = color.Reset
	}
	return color.New(c).SprintfFunc()
}

// MakeLogs creates a new set of loggers for stdout and stderr and logger for the main info.
// If verbose is true, the stdout and stderr logger will be colorized.
// infoLog is always colorized and used to log the main info, like the command that is being executed.
func MakeLogs(verbose, bw bool, secrets []string) Logs {
	var infoLog, outLog, errLog LogWriter
	infoLog = &colorizedWriter{wr: os.Stdout, prefix: "", secrets: secrets, monochrome: bw}
	outLog = &stdOutLogWriter{prefix: " >", level: "DEBUG", secrets: secrets}
	errLog = &stdOutLogWriter{prefix: " !", level: "WARN", secrets: secrets}
	if verbose {
		outLog = &colorizedWriter{wr: os.Stdout, prefix: " >", secrets: secrets, monochrome: bw}
		errLog = &colorizedWriter{wr: os.Stdout, prefix: " !", secrets: secrets, monochrome: bw}
	}
	return Logs{Info: infoLog, Out: outLog, Err: errLog, verbose: verbose, secrets: secrets, monochrome: bw}
}

func maskSecrets(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret == " " || secret == "" {
			continue
		}
		// for secrets with special characters (like '#', '.', etc.), we need to use QuoteMeta and avoid word boundaries
		// if the secret contains alphanumeric characters only, use word boundaries for better precision
		pattern := regexp.QuoteMeta(secret)
		if isAlphanumeric(secret) {
			re := regexp.MustCompile(`\b` + pattern + `\b`)
			s = re.ReplaceAllString(s, "****")
		} else {
			re := regexp.MustCompile(pattern)
			s = re.ReplaceAllString(s, "****")
		}
	}
	return s
}

// isAlphanumeric checks if a string contains only alphanumeric characters and underscores
func isAlphanumeric(s string) bool {
	for _, r := range s {
		isLowercase := r >= 'a' && r <= 'z'
		isUppercase := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		isUnderscore := r == '_'
		
		// If character is not alphanumeric or underscore, return false
		if !isLowercase && !isUppercase && !isDigit && !isUnderscore {
			return false
		}
	}
	return true
}

// stdOutLogWriter is a writer that writes to log with a prefix and a log level.
type stdOutLogWriter struct {
	prefix  string
	level   string
	secrets []string
}

func (w *stdOutLogWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		line = maskSecrets(line, w.secrets)
		log.Printf("[%s] %s %s", w.level, w.prefix, line)
	}
	return len(p), nil
}

// Printf writes the given text to log with the prefix and log level.
func (w *stdOutLogWriter) Printf(format string, v ...any) {
	log.Printf("[%s] %s %s", w.level, w.prefix, fmt.Sprintf(format, v...))
}

// WithHost does nothing for stdOutLogWriter.
func (w *stdOutLogWriter) WithHost(_, _ string) LogWriter {
	return w
}

func (w *stdOutLogWriter) WithWriter(wr io.Writer) LogWriter {
	log.SetOutput(wr)
	return w
}
