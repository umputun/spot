package executor

import (
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
	masker     *secretsMasker
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
		prefix: s.prefix, masker: s.masker, monochrome: s.monochrome}
}

func (s *colorizedWriter) WithWriter(wr io.Writer) LogWriter {
	return &colorizedWriter{wr: wr, hostAddr: s.hostAddr, hostName: s.hostName,
		prefix: s.prefix, masker: s.masker, monochrome: s.monochrome}
}

// Printf writes the given text to io.Writer with the colorized hostAddr prefix.
func (s *colorizedWriter) Printf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	msg = s.masker.mask(msg)
	_, _ = fmt.Fprint(s, msg)
}

// Write writes the given byte slice to stdout with the colorized hostAddr prefix for each line.
// If the input does not end with a newline, one is added.
func (s *colorizedWriter) Write(p []byte) (n int, err error) {
	for _, line := range splitOutputLines(string(p)) {
		hostID := s.hostAddr
		if s.hostName != "" {
			hostID = s.hostName + " " + s.hostAddr
		}
		formattedOutput := fmt.Sprintf("[%s] %s %s", hostID, s.prefix, line)
		if s.prefix == "" {
			formattedOutput = fmt.Sprintf("[%s] %s", hostID, line)
		}
		// mask after choosing the format so an empty prefix cannot discard masking
		formattedOutput = s.masker.mask(formattedOutput)
		colorizer := s.hostColorizer(s.hostAddr)
		colorizedOutput := colorizer("%s\n", formattedOutput)
		_, err = io.WriteString(s.wr, colorizedOutput)
		if err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

// hostColorizer returns a function that formats a string with a color based on the hostAddr name.
func (s *colorizedWriter) hostColorizer(host string) func(format string, a ...any) string {
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
	masker := newSecretsMasker(secrets) // compiled once here and shared by every writer below
	infoLog = &colorizedWriter{wr: os.Stdout, prefix: "", masker: masker, monochrome: bw}
	outLog = &stdOutLogWriter{prefix: " >", level: "DEBUG", masker: masker}
	errLog = &stdOutLogWriter{prefix: " !", level: "WARN", masker: masker}
	if verbose {
		outLog = &colorizedWriter{wr: os.Stdout, prefix: " >", masker: masker, monochrome: bw}
		errLog = &colorizedWriter{wr: os.Stdout, prefix: " !", masker: masker, monochrome: bw}
	}
	return Logs{Info: infoLog, Out: outLog, Err: errLog, verbose: verbose, secrets: secrets, monochrome: bw}
}

// secretsMasker replaces known secret values in log output. Patterns are compiled once per masker
// rather than per call: masking runs for every line of every command, and recompiling there
// dominated the cost of the output path.
type secretsMasker struct {
	patterns []*regexp.Regexp
}

// newSecretsMasker compiles one pattern per usable secret, in the order given, since masking
// applies them in sequence. A nil or empty list yields a masker that returns its input.
func newSecretsMasker(secrets []string) *secretsMasker {
	m := &secretsMasker{}
	for _, secret := range secrets {
		if secret == " " || secret == "" {
			continue
		}
		// for secrets with special characters (like '#', '.', etc.), we need to use QuoteMeta and avoid word boundaries
		// if the secret contains alphanumeric characters only, use word boundaries for better precision
		pattern := regexp.QuoteMeta(secret)
		if isAlphanumeric(secret) {
			pattern = `\b` + pattern + `\b`
		}
		m.patterns = append(m.patterns, regexp.MustCompile(pattern))
	}
	return m
}

// mask replaces every configured secret in s. A nil masker leaves s untouched, so a writer built
// without one still works.
func (m *secretsMasker) mask(s string) string {
	if m == nil {
		return s
	}
	for _, re := range m.patterns {
		s = re.ReplaceAllString(s, "****")
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

		// if character is not alphanumeric or underscore, return false
		if !isLowercase && !isUppercase && !isDigit && !isUnderscore {
			return false
		}
	}
	return true
}

// stdOutLogWriter is a writer that writes to log with a prefix and a log level.
type stdOutLogWriter struct {
	prefix string
	level  string
	masker *secretsMasker
}

func (w *stdOutLogWriter) Write(p []byte) (n int, err error) {
	for line := range strings.SplitSeq(string(p), "\n") {
		if line == "" {
			continue
		}
		line = w.masker.mask(line)
		log.Printf("[%s] %s %s", w.level, w.prefix, line)
	}
	return len(p), nil
}

// Printf writes the given text to log with the prefix and log level.
func (w *stdOutLogWriter) Printf(format string, v ...any) {
	log.Printf("[%s] %s %s", w.level, w.prefix, w.masker.mask(fmt.Sprintf(format, v...)))
}

// WithHost does nothing for stdOutLogWriter.
func (w *stdOutLogWriter) WithHost(_, _ string) LogWriter {
	return w
}

func (w *stdOutLogWriter) WithWriter(wr io.Writer) LogWriter {
	log.SetOutput(wr)
	return w
}
