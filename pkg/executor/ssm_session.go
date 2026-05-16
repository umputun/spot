// Package executor provides an SSM Session Manager implementation for AWS.
// It uses StartSession to establish a WebSocket tunnel and executes commands
// via the SSM JSON-over-WebSocket protocol.
package executor

//go:generate moq -out mocks/ssm_session_mock.go -pkg mocks -skip-ensure -fmt goimports . SSMSessionClient

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/coder/websocket"
)

const (
	ssmDocumentName       = "AWS-RunShellScript"
	ssmProtocolVersion    = "2.2"
	ssmMessageTypeCommand = "com.amazon.websession.command"
	ssmMessageTypeOutput  = "com.amazon.websession.output"
	ssmMessageTypeControl = "com.amazon.websession.control"
	// maxCommandSize is the maximum file size we can transfer via base64 (8KB, real SSM limit).
	maxCommandSize = 8 * 1024
)

// SSMSessionClient is an interface for AWS SSM StartSession operations.
// It wraps the methods needed to start a session on managed instances.
type SSMSessionClient interface {
	StartSession(ctx context.Context, params *ssm.StartSessionInput, optFns ...func(*ssm.Options)) (*ssm.StartSessionOutput, error)
}

// SSMSession implements Interface for AWS SSM Session Manager.
// It uses StartSession to establish a WebSocket connection to the managed instance,
// then executes commands via the SSM JSON-over-WebSocket protocol.
//
// Unlike the old Run Command approach (SendCommand/GetCommandInvocation), Session Manager
// provides a bidirectional WebSocket connection that can stream output in real-time.
// However, it still has these limitations:
//
//   - No SFTP support for file transfers (must use base64 encoding for small files)
//   - No SSH agent forwarding
//   - Size limits on individual commands (~8KB)
//
// File operations (Upload/Download) use base64 encoding for small files, with errors
// returned for large files. For large file transfers, use S3 as an intermediary.
type SSMSession struct {
	client     SSMSessionClient
	instanceID string
	region     string
	timeout    time.Duration
	logs       Logs
	session    *websocket.Conn
	mu         sync.Mutex
}

// NewSSMSession creates a new SSM Session Manager executor.
func NewSSMSession(client SSMSessionClient, instanceID, region string, timeout time.Duration, logs Logs) (*SSMSession, error) {
	if client == nil {
		return nil, fmt.Errorf("SSM client is required")
	}
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}
	if region == "" {
		return nil, fmt.Errorf("AWS region is required")
	}
	return &SSMSession{
		client:     client,
		instanceID: instanceID,
		region:     region,
		timeout:    timeout,
		logs:       logs,
	}, nil
}

// StartSession establishes a WebSocket connection to the SSM managed instance.
// Returns an error if the connection cannot be established.
func (s *SSMSession) StartSession(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil {
		return nil
	}

	startInput := &ssm.StartSessionInput{
		Target:       aws.String(fmt.Sprintf("instance:%s", s.instanceID)),
		DocumentName: aws.String(ssmDocumentName),
		Parameters: map[string][]string{
			"commands":      {"/bin/bash"},
			"executionMode": {"Pipe"},
		},
	}

	output, err := s.client.StartSession(ctx, startInput)
	if err != nil {
		return fmt.Errorf("SSM StartSession failed for %s: %w", s.instanceID, err)
	}
	if output == nil {
		return fmt.Errorf("SSM StartSession returned nil output for %s", s.instanceID)
	}

	streamURL := *output.StreamUrl
	token := *output.TokenValue

	wsCtx, wsCancel := context.WithTimeout(ctx, 30*time.Second)
	defer wsCancel()

	conn, _, err := websocket.Dial(wsCtx, streamURL, &websocket.DialOptions{
		Subprotocols: []string{"aws.ssmp.session"},
	})
	if err != nil {
		return fmt.Errorf("failed to establish WebSocket connection to %s: %w", streamURL, err)
	}

	s.session = conn

	authPayload := fmt.Sprintf(`{"action":"startSession","token":%s}`, token)
	authMsg := ssmFrame{
		SchemaVersion:  ssmProtocolVersion,
		MessageType:    ssmMessageTypeControl,
		SequenceNumber: 0,
		Payload:        base64.StdEncoding.EncodeToString([]byte(authPayload)),
	}
	err = s.sendFrame(conn, authMsg)
	if err != nil {
		_ = conn.Close(websocket.StatusGoingAway, "")
		return fmt.Errorf("failed to send authentication frame: %w", err)
	}

	return nil
}

// sendFrame sends an SSM JSON frame over the WebSocket connection.
func (s *SSMSession) sendFrame(conn *websocket.Conn, frame ssmFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("failed to marshal frame: %w", err)
	}
	return conn.Write(context.Background(), websocket.MessageText, data)
}

// ssmFrame represents a frame in the SSM Session Manager protocol.
type ssmFrame struct {
	SchemaVersion  string `json:"schemaVersion"`
	MessageType    string `json:"messageType"`
	SequenceNumber int    `json:"sequenceNumber,omitempty"`
	Payload        string `json:"payload"`
}

// Run executes a command on the remote instance via SSM Session Manager.
// It sends the command through the WebSocket connection and reads the output.
// Returns the stdout output as []string, with each line as an element.
func (s *SSMSession) Run(ctx context.Context, cmd string, opts *RunOpts) ([]string, error) {
	log.Printf("[DEBUG] run SSM command on %s: %s", s.instanceID, cmd)

	if cmd == "" {
		return nil, nil
	}

	if opts == nil || !opts.NoLog {
		_, _ = s.logs.Out.Write([]byte(cmd))
	}

	if err := s.StartSession(ctx); err != nil {
		return nil, err
	}

	conn := s.session
	if conn == nil {
		return nil, fmt.Errorf("SSM session not established")
	}

	cmdMsg := ssmFrame{
		SchemaVersion:  ssmProtocolVersion,
		MessageType:    ssmMessageTypeCommand,
		SequenceNumber: 1,
		Payload:        base64.StdEncoding.EncodeToString([]byte(cmd)),
	}
	if err := s.sendFrame(conn, cmdMsg); err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}

	var stdOut strings.Builder
	var stdErr strings.Builder
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			msgType, data, err := conn.Read(ctx)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				log.Printf("[DEBUG] SSM session read error: %v", err)
				return
			}
			if msgType != websocket.MessageText {
				continue
			}
			var frame ssmFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				log.Printf("[DEBUG] failed to unmarshal SSM frame: %v", err)
				continue
			}
			payload, err := base64.StdEncoding.DecodeString(frame.Payload)
			if err != nil {
				log.Printf("[DEBUG] failed to decode SSM payload: %v", err)
				continue
			}
			output := string(payload)
			s.mu.Lock()
			switch frame.MessageType {
			case ssmMessageTypeOutput:
				stdOut.WriteString(output)
			case "com.amazon.websession.stderr":
				stdErr.WriteString(output)
			}
			s.mu.Unlock()
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
		_ = conn.Close(websocket.StatusGoingAway, "")
		return nil, fmt.Errorf("SSM command failed: %w", ctx.Err())
	case <-time.After(s.timeout):
		_ = conn.Close(websocket.StatusGoingAway, "")
		return nil, fmt.Errorf("SSM command timed out after %v", s.timeout)
	}

	var output []string
	scanner := bufio.NewScanner(strings.NewReader(stdOut.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			output = append(output, line)
		}
	}

	stdErrStr := stdErr.String()
	if stdErrStr != "" {
		return output, fmt.Errorf("SSM command failed: %s", stdErrStr)
	}

	return output, nil
}

// Upload transfers a file from local to remote via SSM Session Manager.
// Uses base64 encoding, with size limits for practical transfer sizes.
// For large file transfers, use S3 as an intermediary.
func (s *SSMSession) Upload(ctx context.Context, local, remote string, opts *UpDownOpts) error {
	log.Printf("[DEBUG] upload %s to %s:%s", local, s.instanceID, remote)

	// #nosec G304 -- local file path from function parameter
	data, err := os.ReadFile(local)
	if err != nil {
		return fmt.Errorf("failed to read local file %s: %w", local, err)
	}

	if len(data) > maxCommandSize {
		return fmt.Errorf("file %s is too large (%d bytes) for SSM upload, use S3 instead", local, len(data))
	}

	if opts != nil && opts.Mkdir {
		mkdirCmd := fmt.Sprintf("mkdir -p %s", shellQuote(filepath.Dir(remote)))
		if _, err := s.Run(ctx, mkdirCmd, nil); err != nil {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	writeCmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d > '%s'", encoded, shellQuote(remote))
	if _, err := s.Run(ctx, writeCmd, &RunOpts{NoLog: true}); err != nil {
		return fmt.Errorf("failed to write remote file: %w", err)
	}

	return nil
}

// Download transfers a file from remote to local via SSM Session Manager.
// Uses base64 encoding for file transfer.
func (s *SSMSession) Download(ctx context.Context, remote, local string, opts *UpDownOpts) error {
	log.Printf("[DEBUG] download %s from %s:%s", remote, s.instanceID, local)

	if opts != nil && opts.Mkdir {
		if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
			return fmt.Errorf("failed to create local directory %s: %w", filepath.Dir(local), err)
		}
	}

	readCmd := fmt.Sprintf("base64 -w 0 '%s'", shellQuote(remote))
	out, err := s.Run(ctx, readCmd, nil)
	if err != nil {
		return fmt.Errorf("failed to read remote file: %w", err)
	}

	encoded := strings.Join(out, "")
	if encoded == "" {
		if err := os.WriteFile(local, []byte{}, 0o600); err != nil {
			return fmt.Errorf("failed to write empty local file: %w", err)
		}
		return nil
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}

	if err := os.WriteFile(local, data, 0o600); err != nil {
		return fmt.Errorf("failed to write local file: %w", err)
	}

	return nil
}

// Sync synchronizes local directory to remote via SSM Session Manager.
// It uploads all local files to the remote directory and optionally
// deletes remote files that don't exist locally.
func (s *SSMSession) Sync(ctx context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error) {
	log.Printf("[DEBUG] sync %s to %s:%s", localDir, s.instanceID, remoteDir)

	uploaded, err := s.syncUpload(ctx, localDir, remoteDir, opts)
	if err != nil {
		return nil, err
	}

	if opts != nil && opts.Delete {
		return s.syncDeleteExtras(ctx, localDir, remoteDir, uploaded, opts)
	}
	return uploaded, nil
}

// syncUpload uploads all local files to the remote directory.
func (s *SSMSession) syncUpload(ctx context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error) {
	var uploaded []string
	var excl []string
	if opts != nil {
		excl = opts.Exclude
	}

	err := filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}
		if isExcluded(relPath, excl) {
			return nil
		}
		remotePath := filepath.Join(remoteDir, relPath)
		if err := s.Upload(ctx, path, remotePath, &UpDownOpts{Mkdir: true}); err != nil {
			return err
		}
		uploaded = append(uploaded, relPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return uploaded, nil
}

// syncDeleteExtras deletes remote files that don't exist locally.
func (s *SSMSession) syncDeleteExtras(ctx context.Context, localDir, remoteDir string, uploaded []string, opts *SyncOpts) ([]string, error) {
	var excl []string
	if opts != nil {
		excl = opts.Exclude
	}

	localFiles := make(map[string]bool)
	if err := filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(localDir, path)
		if !isExcluded(relPath, excl) {
			localFiles[relPath] = true
		}
		return nil
	}); err != nil {
		log.Printf("[WARN] failed to walk local directory: %v", err)
		return uploaded, nil
	}

	listCmd := fmt.Sprintf("find '%s' -type f 2>/dev/null", shellQuote(remoteDir))
	out, err := s.Run(ctx, listCmd, nil)
	if err != nil {
		log.Printf("[WARN] failed to list remote files for delete: %v", err)
		return uploaded, nil
	}

	for _, remotePath := range out {
		relPath, err := filepath.Rel(remoteDir, remotePath)
		if err != nil {
			continue
		}
		if localFiles[relPath] || isExcluded(relPath, excl) {
			continue
		}
		if err := s.Delete(ctx, remotePath, &DeleteOpts{Recursive: false}); err != nil {
			log.Printf("[WARN] failed to delete extra remote file %s: %v", remotePath, err)
		}
	}
	return uploaded, nil
}

// Delete removes a file or directory on the remote instance.
// If the file doesn't exist, returns nil (no error).
func (s *SSMSession) Delete(ctx context.Context, remoteFile string, opts *DeleteOpts) error {
	log.Printf("[DEBUG] delete %s on %s", remoteFile, s.instanceID)

	checkCmd := fmt.Sprintf("test -f '%s' || test -d '%s'", shellQuote(remoteFile), shellQuote(remoteFile))
	_, err := s.Run(ctx, checkCmd, nil)
	if err != nil {
		return nil
	}

	recursive := opts != nil && opts.Recursive
	cmd := fmt.Sprintf("rm -rf '%s'", shellQuote(remoteFile))
	if !recursive {
		cmd = fmt.Sprintf("rm '%s'", shellQuote(remoteFile))
	}

	_, err = s.Run(ctx, cmd, nil)
	return err
}

// Close closes the SSM Session Manager WebSocket connection.
func (s *SSMSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil {
		err := s.session.Close(websocket.StatusInternalError, "closed by spot")
		s.session = nil
		return err
	}
	return nil
}

// isRetriableSSMError returns true if the error is likely to succeed on retry.
func isRetriableSSMError(err error) bool {
	var awsErr interface{ ErrorCode() string }
	if errors.As(err, &awsErr) {
		switch awsErr.ErrorCode() {
		case "ThrottlingException", "TooManyRequestsException", "RequestLimitExceeded":
			return true
		default:
			return false
		}
	}
	return false
}

// shellQuote wraps a string in single quotes for shell safety.
// It escapes single quotes by ending the quote, adding an escaped quote, and starting a new quote.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
