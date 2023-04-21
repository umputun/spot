/* Copyright (c) 2021 Bram Vandenbogaerde And Contributors
 * You may use, distribute or modify this code under the
 * terms of the Mozilla Public License 2.0, which is distributed
 * along with the source code.
 */

package scp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type PassThru func(r io.Reader, total int64) io.Reader

type Client struct {
	// Host the host to connect to.
	Host string

	// ClientConfig the client config to use.
	ClientConfig *ssh.ClientConfig

	// Session stores the SSH session while the connection is running.
	Session *ssh.Session

	// Conn stores the SSH connection itself in order to close it after transfer.
	Conn ssh.Conn

	// Timeout the maximal amount of time to wait for a file transfer to complete.
	// Deprecated: use context.Context for each function instead.
	Timeout time.Duration

	// RemoteBinary the absolute path to the remote SCP binary.
	RemoteBinary string
}

// Connect connects to the remote SSH server, returns error if it couldn't establish a session to the SSH server.
func (a *Client) Connect() error {
	if a.Session != nil {
		return nil
	}

	client, err := ssh.Dial("tcp", a.Host, a.ClientConfig)
	if err != nil {
		return err
	}

	a.Conn = client.Conn
	a.Session, err = client.NewSession()
	if err != nil {
		return err
	}
	return nil
}

// CopyFromFile copies the contents of an os.File to a remote location, it will get the length of the file by looking it up from the filesystem.
func (a *Client) CopyFromFile(ctx context.Context, file os.File, remotePath string, permissions string) error {
	return a.CopyFromFilePassThru(ctx, file, remotePath, permissions, nil)
}

// CopyFromFilePassThru copies the contents of an os.File to a remote location, it will get the length of the file by looking it up from the filesystem.
// Access copied bytes by providing a PassThru reader factory.
func (a *Client) CopyFromFilePassThru(ctx context.Context, file os.File, remotePath string, permissions string, passThru PassThru) error {
	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	return a.CopyPassThru(ctx, &file, remotePath, permissions, stat.Size(), passThru)
}

// CopyFile copies the contents of an io.Reader to a remote location, the length is determined by reading the io.Reader until EOF
// if the file length in know in advance please use "Copy" instead.
func (a *Client) CopyFile(ctx context.Context, fileReader io.Reader, remotePath string, permissions string) error {
	return a.CopyFilePassThru(ctx, fileReader, remotePath, permissions, nil)
}

// CopyFilePassThru copies the contents of an io.Reader to a remote location, the length is determined by reading the io.Reader until EOF
// if the file length in know in advance please use "Copy" instead.
// Access copied bytes by providing a PassThru reader factory.
func (a *Client) CopyFilePassThru(ctx context.Context, fileReader io.Reader, remotePath string, permissions string, passThru PassThru) error {
	contentsBytes, err := ioutil.ReadAll(fileReader)
	if err != nil {
		return fmt.Errorf("failed to read all data from reader: %w", err)
	}
	bytesReader := bytes.NewReader(contentsBytes)

	return a.CopyPassThru(ctx, bytesReader, remotePath, permissions, int64(len(contentsBytes)), passThru)
}

// wait waits for the waitgroup for the specified max timeout.
// Returns true if waiting timed out.
func wait(wg *sync.WaitGroup, ctx context.Context) error {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()

	select {
	case <-c:
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

// checkResponse checks the response it reads from the remote, and will return a single error in case
// of failure.
func checkResponse(r io.Reader) error {
	response, err := ParseResponse(r)
	if err != nil {
		return err
	}

	if response.IsFailure() {
		return errors.New(response.GetMessage())
	}

	return nil

}

// Copy copies the contents of an io.Reader to a remote location.
func (a *Client) Copy(ctx context.Context, r io.Reader, remotePath string, permissions string, size int64) error {
	return a.CopyPassThru(ctx, r, remotePath, permissions, size, nil)
}

// CopyPassThru copies the contents of an io.Reader to a remote location.
// Access copied bytes by providing a PassThru reader factory
func (a *Client) CopyPassThru(ctx context.Context, r io.Reader, remotePath string, permissions string, size int64, passThru PassThru) error {
	stdout, err := a.Session.StdoutPipe()
	if err != nil {
		return err
	}
	w, err := a.Session.StdinPipe()
	if err != nil {
		return err
	}
	defer w.Close()

	if passThru != nil {
		r = passThru(r, size)
	}

	filename := path.Base(remotePath)

	wg := sync.WaitGroup{}
	wg.Add(2)

	errCh := make(chan error, 2)

	go func() {
		defer wg.Done()
		defer w.Close()

		_, err = fmt.Fprintln(w, "C"+permissions, size, filename)
		if err != nil {
			errCh <- err
			return
		}

		if err = checkResponse(stdout); err != nil {
			errCh <- err
			return
		}

		_, err = io.Copy(w, r)
		if err != nil {
			errCh <- err
			return
		}

		_, err = fmt.Fprint(w, "\x00")
		if err != nil {
			errCh <- err
			return
		}

		if err = checkResponse(stdout); err != nil {
			errCh <- err
			return
		}
	}()

	go func() {
		defer wg.Done()
		err := a.Session.Run(fmt.Sprintf("%s -qt %q", a.RemoteBinary, remotePath))
		if err != nil {
			errCh <- err
			return
		}
	}()

	if a.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.Timeout)
		defer cancel()
	}

	if err := wait(&wg, ctx); err != nil {
		return err
	}

	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// CopyFromRemote copies a file from the remote to the local file given by the `file`
// parameter. Use `CopyFromRemotePassThru` if a more generic writer
// is desired instead of writing directly to a file on the file system.?
func (a *Client) CopyFromRemote(ctx context.Context, file *os.File, remotePath string) error {
	return a.CopyFromRemotePassThru(ctx, file, remotePath, nil)
}

// CopyFromRemotePassThru copies a file from the remote to the given writer. The passThru parameter can be used
// to keep track of progress and how many bytes that were download from the remote.
// `passThru` can be set to nil to disable this behaviour.
func (a *Client) CopyFromRemotePassThru(ctx context.Context, w io.Writer, remotePath string, passThru PassThru) error {
	wg := sync.WaitGroup{}
	errCh := make(chan error, 4)

	wg.Add(1)
	go func() {
		var err error

		defer func() {
			// NOTE: this might send an already sent error another time, but since we only receive opne, this is fine. On the "happy-path" of this function, the error will be `nil` therefore completing the "err<-errCh" at the bottom of the function.
			errCh <- err
			// We must unblock the go routine first as we block on reading the channel later
			wg.Done()

		}()

		r, err := a.Session.StdoutPipe()
		if err != nil {
			errCh <- err
			return
		}

		in, err := a.Session.StdinPipe()
		if err != nil {
			errCh <- err
			return
		}
		defer in.Close()

		err = a.Session.Start(fmt.Sprintf("%s -f %q", a.RemoteBinary, remotePath))
		if err != nil {
			errCh <- err
			return
		}

		err = Ack(in)
		if err != nil {
			errCh <- err
			return
		}

		res, err := ParseResponse(r)
		if err != nil {
			errCh <- err
			return
		}
		if res.IsFailure() {
			errCh <- errors.New(res.GetMessage())
			return
		}

		infos, err := res.ParseFileInfos()
		if err != nil {
			errCh <- err
			return
		}

		err = Ack(in)
		if err != nil {
			errCh <- err
			return
		}

		if passThru != nil {
			r = passThru(r, infos.Size)
		}

		_, err = CopyN(w, r, infos.Size)
		if err != nil {
			errCh <- err
			return
		}

		err = Ack(in)
		if err != nil {
			errCh <- err
			return
		}

		err = a.Session.Wait()
		if err != nil {
			errCh <- err
			return
		}
	}()

	if a.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.Timeout)
		defer cancel()
	}

	if err := wait(&wg, ctx); err != nil {
		return err
	}
	finalErr := <-errCh
	close(errCh)
	return finalErr
}

func (a *Client) Close() {
	if a.Session != nil {
		a.Session.Close()
	}
	if a.Conn != nil {
		a.Conn.Close()
	}
}
