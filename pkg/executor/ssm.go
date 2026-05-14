// Package executor provides an interface for the executor as well as implementations
// for SSH, local, dry-run, and AWS SSM modes.
package executor

//go:generate moq -out mocks/ssm_mock.go -pkg mocks -skip-ensure . SSMClient

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	// pollInterval is the time between command status checks
	pollInterval = 500 * time.Millisecond
	// maxCommandSize is the maximum file size we can transfer via base64 (8KB, real SSM limit)
	maxCommandSize = 8 * 1024
)

// SSMClient is an interface for AWS SSM Run Command operations.
// It wraps the methods needed to execute commands on managed instances.
type SSMClient interface {
	SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

// SSM implements Interface for AWS SSM managed instances.
// It uses AWS SSM Run Command (AWS-RunShellScript) to execute commands on remote instances.
// File operations use base64 encoding/decoding, which has size limitations for large files.
// For large file transfers, consider using S3 as an intermediary.
type SSM struct {
	client     SSMClient
	instanceID string
	timeout    time.Duration
	logs       Logs
}

// NewSSM creates a new SSM executor for the given instance ID.
// The client is the AWS SSM client, instanceID is the EC2 instance ID,
// timeout is the maximum time to wait for command execution, and logs is for logging.
func NewSSM(client SSMClient, instanceID string, timeout time.Duration, logs Logs) (*SSM, error) {
	if client == nil {
		return nil, fmt.Errorf("SSM client is required")
	}
	if instanceID == "" {
		return nil, fmt.Errorf("instance ID is required")
	}
	return &SSM{
		client:     client,
		instanceID: instanceID,
		timeout:    timeout,
		logs:       logs,
	}, nil
}

// Run executes a command on the remote instance via SSM.
// It sends the command using AWS-RunShellScript and polls for completion.
// Returns the stdout output as []string.
func (s *SSM) Run(ctx context.Context, cmd string, opts *RunOpts) ([]string, error) {
	log.Printf("[DEBUG] run SSM command on %s: %s", s.instanceID, cmd)

	if s.client == nil {
		return nil, fmt.Errorf("SSM client is not connected")
	}

	if cmd == "" {
		return nil, nil
	}

	// Skip logging if NoLog is set (for Upload commands)
	if opts == nil || !opts.NoLog {
		_, _ = s.logs.Out.Write([]byte(cmd))
	}

	sendInput := &ssm.SendCommandInput{
		InstanceIds:    []string{s.instanceID},
		DocumentName:   aws.String("AWS-RunShellScript"),
		Parameters:     map[string][]string{"commands": {cmd}},
		TimeoutSeconds: aws.Int32(int32(s.timeout.Seconds())),
	}

	sendResp, err := s.client.SendCommand(ctx, sendInput)
	if err != nil {
		return nil, fmt.Errorf("failed to send SSM command: %w", err)
	}

	// Check for nil Command before dereferencing
	if sendResp.Command == nil || sendResp.Command.CommandId == nil {
		return nil, fmt.Errorf("SSM command response missing Command or CommandId")
	}
	commandID := *sendResp.Command.CommandId
	log.Printf("[DEBUG] SSM command %s sent to %s", commandID, s.instanceID)

	pollCtx, pollCancel := context.WithTimeout(ctx, s.timeout)
	defer pollCancel()

	var output []string
	startTime := time.Now()

	for {
		// Check context before polling
		select {
		case <-pollCtx.Done():
			return nil, fmt.Errorf("polling context expired: %w", pollCtx.Err())
		default:
		}

		time.Sleep(pollInterval)

		invResp, err := s.client.GetCommandInvocation(pollCtx, &ssm.GetCommandInvocationInput{
			CommandId:  sendResp.Command.CommandId,
			InstanceId: aws.String(s.instanceID),
		})
		if err != nil {
			// Classify errors - only continue for retriable errors
			if !isRetriableSSMError(err) {
				return nil, fmt.Errorf("SSM command %s failed: %w", commandID, err)
			}
			log.Printf("[DEBUG] SSM command %s not ready: %v", commandID, err)
			continue
		}

		status := string(invResp.Status)

		switch status {
		case "Success":
			if invResp.StandardOutputContent != nil {
				for _, line := range strings.Split(*invResp.StandardOutputContent, "\n") {
					if line != "" {
						output = append(output, line)
					}
				}
			}
			return output, nil

		case "Failed", "TimedOut", "Canceled":
			if invResp.StandardOutputContent != nil {
				for _, line := range strings.Split(*invResp.StandardOutputContent, "\n") {
					if line != "" {
						output = append(output, line)
					}
				}
			}
			errMsg := fmt.Sprintf("SSM command %s failed with status %s", commandID, status)
			if invResp.StandardErrorContent != nil {
				errMsg += fmt.Sprintf(": %s", *invResp.StandardErrorContent)
			}
			if invResp.ResponseCode != 0 {
				errMsg += fmt.Sprintf(" (exit code: %d)", invResp.ResponseCode)
			}
			return output, fmt.Errorf("%s", errMsg)

		default:
			log.Printf("[DEBUG] SSM command %s status: %s", commandID, status)
		}

		if time.Since(startTime) > s.timeout {
			return nil, fmt.Errorf("SSM command timed out after %v", s.timeout)
		}
	}
}

// isRetriableSSMError returns true if the error is likely to succeed on retry.
func isRetriableSSMError(err error) bool {
	var awsErr interface{ ErrorCode() string }
	if errors.As(err, &awsErr) {
		switch awsErr.ErrorCode() {
		case "ThrottlingException", "TooManyRequestsException":
			return true
		default:
			return false
		}
	}
	return false
}

// Upload transfers a file from local to remote via SSM using base64 encoding.
// Uses base64 -w 0 to prevent line-wrapping issues.
// For large file transfers, consider using S3 as an intermediary.
func (s *SSM) Upload(ctx context.Context, local, remote string, opts *UpDownOpts) error {
	log.Printf("[DEBUG] upload %s to %s:%s", local, s.instanceID, remote)

	data, err := os.ReadFile(local)
	if err != nil {
		return fmt.Errorf("failed to read local file %s: %w", local, err)
	}

	if len(data) > maxCommandSize {
		return fmt.Errorf("file %s is too large (%d bytes) for SSM upload, use S3 instead", local, len(data))
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	if opts != nil && opts.Mkdir {
		mkdirCmd := fmt.Sprintf("mkdir -p %s", shellQuote(filepath.Dir(remote)))
		if _, err := s.Run(ctx, mkdirCmd, nil); err != nil {
			return fmt.Errorf("failed to create remote directory: %w", err)
		}
	}

	// Use printf to avoid echo issues with special characters
	// Use base64 -w 0 to prevent line-wrapping
	// NoLog=true prevents base64 content from being logged
	writeCmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d > '%s'", encoded, shellQuote(remote))
	if _, err := s.Run(ctx, writeCmd, &RunOpts{NoLog: true}); err != nil {
		return fmt.Errorf("failed to write remote file: %w", err)
	}

	return nil
}

// Download transfers a file from remote to local via SSM using base64 encoding.
// Uses base64 -w 0 to prevent line-wrapping issues.
func (s *SSM) Download(ctx context.Context, remote, local string, opts *UpDownOpts) error {
	log.Printf("[DEBUG] download %s from %s:%s", remote, s.instanceID, local)

	if opts != nil && opts.Mkdir {
		if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
			return fmt.Errorf("failed to create local directory %s: %w", filepath.Dir(local), err)
		}
	}

	// Use base64 -w 0 to prevent line-wrapping at 76 chars
	readCmd := fmt.Sprintf("base64 -w 0 '%s'", shellQuote(remote))
	out, err := s.Run(ctx, readCmd, nil)
	if err != nil {
		return fmt.Errorf("failed to read remote file: %w", err)
	}

	// Join without \n since base64 -w 0 produces continuous output
	encoded := strings.Join(out, "")
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}

	// Write file including zero-byte files
	if err := os.WriteFile(local, data, 0o600); err != nil {
		return fmt.Errorf("failed to write local file: %w", err)
	}

	return nil
}

// Sync synchronizes local directory to remote via SSM.
// It uploads all local files to the remote directory and optionally deletes remote files that don't exist locally.
func (s *SSM) Sync(ctx context.Context, localDir, remoteDir string, opts *SyncOpts) ([]string, error) {
	log.Printf("[DEBUG] sync %s to %s:%s", localDir, s.instanceID, remoteDir)

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

	if opts != nil && opts.Delete {
		localFiles := make(map[string]bool)
		err := filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			relPath, _ := filepath.Rel(localDir, path)
			if isExcluded(relPath, excl) {
				return nil
			}
			localFiles[relPath] = true
			return nil
		})
		if err != nil {
			return nil, err
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
			if localFiles[relPath] {
				continue
			}
			if isExcluded(relPath, excl) {
				continue
			}
			if err := s.Delete(ctx, remotePath, &DeleteOpts{Recursive: false}); err != nil {
				log.Printf("[WARN] failed to delete extra remote file %s: %v", remotePath, err)
			}
		}
	}

	return uploaded, nil
}

// Delete removes a file or directory on the remote instance.
// If the file doesn't exist, returns nil (no error).
func (s *SSM) Delete(ctx context.Context, remoteFile string, opts *DeleteOpts) error {
	log.Printf("[DEBUG] delete %s on %s", remoteFile, s.instanceID)

	// Check if the file exists using SSM (locale-independent)
	checkCmd := fmt.Sprintf("test -f '%s' || test -d '%s'", shellQuote(remoteFile), shellQuote(remoteFile))
	_, err := s.Run(ctx, checkCmd, nil)
	if err != nil {
		// File doesn't exist, which is OK for rm
		return nil
	}

	// File exists, proceed with deletion
	recursive := opts != nil && opts.Recursive
	cmd := fmt.Sprintf("rm -rf '%s'", shellQuote(remoteFile))
	if !recursive {
		cmd = fmt.Sprintf("rm '%s'", shellQuote(remoteFile))
	}

	_, err = s.Run(ctx, cmd, nil)
	return err
}

// Close does nothing for SSM (no persistent connection).
func (s *SSM) Close() error {
	return nil
}

// shellQuote wraps a string in single quotes for shell safety.
// It escapes single quotes by ending the quote, adding an escaped quote, and starting a new quote.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
