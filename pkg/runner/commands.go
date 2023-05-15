package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
)

// execCmd is a single command execution on a target host. It prepares the command, executes it and returns details.
// All commands directly correspond to the config.Cmd commands.
type execCmd struct {
	cmd      config.Cmd
	hostAddr string
	hostName string
	tsk      *config.Task
	exec     executor.Interface
	verbose  bool
}

const tmpRemoteDir = "/tmp/.spot" // this is a directory on remote host to store temporary files

// Script executes a script command on a target host. It can be a single line or multiline script,
// this part is translated by the prepScript function.
// If sudo option is set, it will execute the script with sudo. If output contains variables as "setvar foo=bar",
// it will return the variables as map.
func (ec *execCmd) Script(ctx context.Context) (details string, vars map[string]string, err error) {
	cond, err := ec.checkCondition(ctx)
	if err != nil {
		return "", nil, err
	}
	if !cond {
		return fmt.Sprintf(" {skip: %s}", ec.cmd.Name), nil, nil
	}

	single, multiRdr := ec.cmd.GetScript()
	c, teardown, err := ec.prepScript(ctx, single, multiRdr)
	if err != nil {
		return details, nil, fmt.Errorf("can't prepare script on %s: %w", ec.hostAddr, err)
	}
	defer func() {
		if teardown == nil {
			return
		}
		if err = teardown(); err != nil {
			log.Printf("[WARN] can't teardown script on %s: %v", ec.hostAddr, err)
		}
	}()
	details = fmt.Sprintf(" {script: %s}", c)
	if ec.cmd.Options.Sudo {
		details = fmt.Sprintf(" {script: %s, sudo: true}", c)
		c = fmt.Sprintf("sudo sh -c %q", c)
	}
	out, err := ec.exec.Run(ctx, c, ec.verbose)
	if err != nil {
		return details, nil, fmt.Errorf("can't run script on %s: %w", ec.hostAddr, err)
	}

	// collect setvar output to vars and latter it will be set to the environment. This is needed for the next commands.
	// setenv output is in the format of "setenv foo=bar" and it is appended to the output by the script itself.
	// this part done inside cmd.scriptFile function.
	vars = make(map[string]string)
	for _, line := range out {
		if !strings.HasPrefix(line, "setvar ") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "setvar"), "=", 2)
		if len(parts) != 2 {
			continue
		}
		vars[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	return details, vars, nil
}

// Copy uploads a single file or multiple files (if wildcard is used) to a target host.
// if sudo option is set, it will make a temporary directory and upload the files there,
// then move it to the final destination with sudo script execution.
func (ec *execCmd) Copy(ctx context.Context) (details string, vars map[string]string, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}

	src := tmpl.apply(ec.cmd.Copy.Source)
	dst := tmpl.apply(ec.cmd.Copy.Dest)

	if !ec.cmd.Options.Sudo {
		// if sudo is not set, we can use the original destination and upload the file directly
		details = fmt.Sprintf(" {copy: %s -> %s}", src, dst)
		if err := ec.exec.Upload(ctx, src, dst, ec.cmd.Copy.Mkdir); err != nil {
			return details, nil, fmt.Errorf("can't copy file to %s: %w", ec.hostAddr, err)
		}
		return details, nil, nil
	}

	if ec.cmd.Options.Sudo {
		// if sudo is set, we need to upload the file to a temporary directory and move it to the final destination
		details = fmt.Sprintf(" {copy: %s -> %s, sudo: true}", src, dst)
		tmpDest := filepath.Join(tmpRemoteDir, filepath.Base(dst))
		if err := ec.exec.Upload(ctx, src, tmpDest, true); err != nil { // upload to a temporary directory with mkdir
			return details, nil, fmt.Errorf("can't copy file to %s: %w", ec.hostAddr, err)
		}

		mvCmd := fmt.Sprintf("mv -f %s %s", tmpDest, dst) // move a single file
		if strings.Contains(src, "*") && !strings.HasSuffix(tmpDest, "/") {
			mvCmd = fmt.Sprintf("mv -f %s/* %s", tmpDest, dst) // move multiple files, if wildcard is used
			defer func() {
				// remove temporary directory we created under /tmp/.spot for multiple files
				if _, err := ec.exec.Run(ctx, fmt.Sprintf("rm -rf %s", tmpDest), ec.verbose); err != nil {
					log.Printf("[WARN] can't remove temporary directory on %s: %v", ec.hostAddr, err)
				}
			}()
		}
		c, _, err := ec.prepScript(ctx, mvCmd, nil)
		if err != nil {
			return details, nil, fmt.Errorf("can't prepare sudo moving command on %s: %w", ec.hostAddr, err)
		}

		sudoMove := fmt.Sprintf("sudo %s", c)
		if _, err := ec.exec.Run(ctx, sudoMove, ec.verbose); err != nil {
			return details, nil, fmt.Errorf("can't move file to %s: %w", ec.hostAddr, err)
		}
	}

	return details, nil, nil
}

// Mcopy uploads multiple files to a target host. It calls copy function for each file.
func (ec *execCmd) Mcopy(ctx context.Context) (details string, vars map[string]string, err error) {
	msgs := []string{}
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	for _, c := range ec.cmd.MCopy {
		src := tmpl.apply(c.Source)
		dst := tmpl.apply(c.Dest)
		msgs = append(msgs, fmt.Sprintf("%s -> %s", src, dst))
		ecSingle := ec
		ecSingle.cmd.Copy = config.CopyInternal{Source: src, Dest: dst, Mkdir: c.Mkdir}
		if _, _, err := ecSingle.Copy(ctx); err != nil {
			return details, nil, fmt.Errorf("can't copy file to %s: %w", ec.hostAddr, err)
		}
	}
	details = fmt.Sprintf(" {copy: %s}", strings.Join(msgs, ", "))
	return details, nil, nil
}

// Sync synchronizes files from a source to a destination on a target host.
func (ec *execCmd) Sync(ctx context.Context) (details string, vars map[string]string, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	src := tmpl.apply(ec.cmd.Sync.Source)
	dst := tmpl.apply(ec.cmd.Sync.Dest)
	details = fmt.Sprintf(" {sync: %s -> %s}", src, dst)
	if _, err := ec.exec.Sync(ctx, src, dst, ec.cmd.Sync.Delete); err != nil {
		return details, nil, fmt.Errorf("can't sync files on %s: %w", ec.hostAddr, err)
	}
	return details, nil, nil
}

// Delete deletes files on a target host. If sudo option is set, it will execute a sudo rm commands.
func (ec *execCmd) Delete(ctx context.Context) (details string, vars map[string]string, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	loc := tmpl.apply(ec.cmd.Delete.Location)

	if !ec.cmd.Options.Sudo {
		// if sudo is not set, we can delete the file directly
		if err := ec.exec.Delete(ctx, loc, ec.cmd.Delete.Recursive); err != nil {
			return details, nil, fmt.Errorf("can't delete files on %s: %w", ec.hostAddr, err)
		}
		details = fmt.Sprintf(" {delete: %s, recursive: %v}", loc, ec.cmd.Delete.Recursive)
	}

	if ec.cmd.Options.Sudo {
		// if sudo is set, we need to delete the file using sudo by ssh-ing into the host and running the command
		cmd := fmt.Sprintf("sudo rm -f %s", loc)
		if ec.cmd.Delete.Recursive {
			cmd = fmt.Sprintf("sudo rm -rf %s", loc)
		}
		if _, err := ec.exec.Run(ctx, cmd, ec.verbose); err != nil {
			return details, nil, fmt.Errorf("can't delete file(s) on %s: %w", ec.hostAddr, err)
		}
		details = fmt.Sprintf(" {delete: %s, recursive: %v, sudo: true}", loc, ec.cmd.Delete.Recursive)
	}

	return details, nil, nil
}

// Wait waits for a command to complete on a target hostAddr. It runs the command in a loop with a check duration
// until the command succeeds or the timeout is exceeded.
func (ec *execCmd) Wait(ctx context.Context) (details string, vars map[string]string, err error) {
	single, multiRdr := ec.cmd.GetWait()
	c, teardown, err := ec.prepScript(ctx, single, multiRdr)
	if err != nil {
		return details, nil, fmt.Errorf("can't prepare script on %s: %w", ec.hostAddr, err)
	}
	defer func() {
		if teardown == nil {
			return
		}
		if err = teardown(); err != nil {
			log.Printf("[WARN] can't teardown script on %s: %v", ec.hostAddr, err)
		}
	}()

	timeout, duration := ec.cmd.Wait.Timeout, ec.cmd.Wait.CheckDuration
	if duration == 0 {
		duration = 5 * time.Second // default check duration if not set
	}
	if timeout == 0 {
		timeout = time.Hour * 24 // default timeout if not set, wait practically forever
	}

	details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v}",
		c, timeout.Truncate(100*time.Millisecond), duration.Truncate(100*time.Millisecond))

	waitCmd := fmt.Sprintf("sh -c %q", c) // run wait command in a shell
	if ec.cmd.Options.Sudo {
		details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v, sudo: true}",
			c, timeout.Truncate(100*time.Millisecond), duration.Truncate(100*time.Millisecond))
		waitCmd = fmt.Sprintf("sudo sh -c %q", c) // add sudo if needed
	}

	checkTk := time.NewTicker(duration)
	defer checkTk.Stop()
	timeoutTk := time.NewTicker(timeout)
	defer timeoutTk.Stop()

	for {
		select {
		case <-ctx.Done():
			return details, nil, ctx.Err()
		case <-timeoutTk.C:
			return details, nil, fmt.Errorf("timeout exceeded")
		case <-checkTk.C:
			if _, err := ec.exec.Run(ctx, waitCmd, false); err == nil {
				return details, nil, nil // command succeeded
			}
		}
	}
}

// Echo prints a message. It enforces the echo command to start with "echo " and adds sudo if needed.
// It returns the result of the echo command as details string.
func (ec *execCmd) Echo(ctx context.Context) (string, map[string]string, error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	echoCmd := tmpl.apply(ec.cmd.Echo)
	if !strings.HasPrefix(echoCmd, "echo ") {
		echoCmd = fmt.Sprintf("echo %s", echoCmd)
	}
	if ec.cmd.Options.Sudo {
		echoCmd = fmt.Sprintf("sudo %s", echoCmd)
	}
	out, err := ec.exec.Run(ctx, echoCmd, false)
	if err != nil {
		return "", nil, fmt.Errorf("can't run echo command on %s: %w", ec.hostAddr, err)
	}
	details := fmt.Sprintf(" {echo: %s}", strings.Join(out, "; "))
	return details, nil, nil
}

func (ec *execCmd) checkCondition(ctx context.Context) (bool, error) {
	if ec.cmd.Condition == "" {
		return true, nil // no condition, always allow
	}

	single, multiRdr, inverted := ec.cmd.GetCondition()
	c, teardown, err := ec.prepScript(ctx, single, multiRdr)
	if err != nil {
		return false, fmt.Errorf("can't prepare condition script on %s: %w", ec.hostAddr, err)
	}
	defer func() {
		if teardown == nil {
			return
		}
		if err = teardown(); err != nil {
			log.Printf("[WARN] can't teardown condition script on %s: %v", ec.hostAddr, err)
		}
	}()

	if ec.cmd.Options.Sudo { // command's sudo also applies to condition script
		c = fmt.Sprintf("sudo sh -c %q", c)
	}

	// run the condition command
	if _, err := ec.exec.Run(ctx, c, ec.verbose); err != nil {
		log.Printf("[DEBUG] condition not passed on %s: %v", ec.hostAddr, err)
		if inverted {
			return true, nil // inverted condition failed, so we return true
		}
		return false, nil
	}

	// If condition passed
	if inverted {
		return false, nil // inverted condition passed, so we return false
	}
	return true, nil
}

// prepScript prepares a script for execution. Script can be either a single command or a multiline script.
// In case of a single command, it just applies templates to it. In case of a multiline script, it creates
// a temporary file with the script chmod as +x and uploads to remote host to /tmp.
// it also  returns a teardown function to remove the temporary file after the command execution.
func (ec *execCmd) prepScript(ctx context.Context, s string, r io.Reader) (cmd string, teardown func() error, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name}

	if s != "" { // single command, nothing to do just apply templates
		return tmpl.apply(s), nil, nil
	}

	// multiple commands, create a temporary script

	// read the script from the reader and apply templates
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, r); err != nil {
		return "", nil, fmt.Errorf("can't read script: %w", err)
	}
	rdr := bytes.NewBuffer([]byte(tmpl.apply(buf.String())))
	// make a temporary file and copy the script to it
	tmp, err := os.CreateTemp("", "spot-script")
	if err != nil {
		return "", nil, fmt.Errorf("can't create temporary file: %w", err)
	}
	if _, err = io.Copy(tmp, rdr); err != nil {
		return "", nil, fmt.Errorf("can't copy script to temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", nil, fmt.Errorf("can't close temporary file: %w", err)
	}

	// make the script executable locally, upload preserves the permissions
	if err = os.Chmod(tmp.Name(), 0o700); err != nil { // nolint
		return "", nil, fmt.Errorf("can't chmod temporary file: %w", err)
	}

	// get temp file name for remote hostAddr
	dst := filepath.Join(tmpRemoteDir, filepath.Base(tmp.Name())) // nolint

	// upload the script to the remote hostAddr
	if err = ec.exec.Upload(ctx, tmp.Name(), dst, true); err != nil {
		return "", nil, fmt.Errorf("can't upload script to %s: %w", ec.hostAddr, err)
	}
	cmd = fmt.Sprintf("sh -c %s", dst)

	teardown = func() error {
		// remove the script from the remote hostAddr, should be invoked by the caller after the command is executed
		if err := ec.exec.Delete(ctx, dst, false); err != nil {
			return fmt.Errorf("can't remove temporary remote script %s (%s): %w", dst, ec.hostAddr, err)
		}
		return nil
	}

	return cmd, teardown, nil
}

// templater is a helper struct to apply templates to a command
type templater struct {
	hostAddr string
	hostName string
	command  string
	env      map[string]string
	task     *config.Task
	err      error
}

// apply applies templates to a string to replace predefined vars placeholders with actual values
// it also applies the task environment variables to the string
func (tm *templater) apply(inp string) string {
	apply := func(inp, from, to string) string {
		// replace either {SPOT_REMOTE_HOST} ${SPOT_REMOTE_HOST} or $SPOT_REMOTE_HOST format
		res := strings.ReplaceAll(inp, fmt.Sprintf("${%s}", from), to)
		res = strings.ReplaceAll(res, fmt.Sprintf("$%s", from), to)
		res = strings.ReplaceAll(res, fmt.Sprintf("{%s}", from), to)
		return res
	}

	res := inp
	res = apply(res, "SPOT_REMOTE_HOST", tm.hostAddr)
	res = apply(res, "SPOT_REMOTE_NAME", tm.hostName)
	res = apply(res, "SPOT_COMMAND", tm.command)
	res = apply(res, "SPOT_REMOTE_USER", tm.task.User)
	res = apply(res, "SPOT_TASK", tm.task.Name)
	if tm.err != nil {
		res = apply(res, "SPOT_ERROR", tm.err.Error())
	} else {
		res = apply(res, "SPOT_ERROR", "")
	}

	for k, v := range tm.env {
		res = apply(res, k, v)
	}

	return res
}
