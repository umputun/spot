package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	mr "math/rand"
	"net"
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
	cmd       config.Cmd
	hostAddr  string
	hostName  string
	tsk       *config.Task
	exec      executor.Interface
	verbose   bool
	verbose2  bool
	sshShell  string
	sshTmpDir string
	onExit    string
}

type execCmdResp struct {
	details    string
	verbose    string
	vars       map[string]string
	onExit     execCmd
	registered map[string]string
}

type execCmdErr struct {
	err  error
	exec execCmd
}

func (e execCmdErr) Error() string { return e.err.Error() }

const tmpRemoteDirPrefix = "/tmp/.spot-" // this is a directory on remote host to store temporary files

// Script executes a script command on a target host. It can be a single line or multiline script,
// this part is translated by the prepScript function.
// If sudo option is set, it will execute the script with sudo. If output contains variables as "setvar foo=bar",
// it will return the variables as map.
func (ec *execCmd) Script(ctx context.Context) (resp execCmdResp, err error) {
	cond, err := ec.checkCondition(ctx)
	if err != nil {
		return resp, err
	}
	if !cond {
		resp.details = fmt.Sprintf(" {skip: %s}", ec.cmd.Name)
		return resp, nil
	}

	// pre-process register variable names with template substitution
	// this enables dynamic register variable names in the script
	tmpl := templater{
		hostAddr: ec.hostAddr,
		hostName: ec.hostName,
		task:     ec.tsk,
		command:  ec.cmd.Name,
		env:      ec.cmd.Environment,
	}

	// create a copy of the command with processed register variable names
	cmdCopy := ec.cmd
	processedRegister := make([]string, 0, len(ec.cmd.Register))
	for _, regVar := range ec.cmd.Register {
		processed := tmpl.apply(regVar)
		processedRegister = append(processedRegister, processed)
	}
	cmdCopy.Register = processedRegister
	ecCopy := *ec
	ecCopy.cmd = cmdCopy

	single, multiRdr := ec.cmd.GetScript()
	c, scr, teardown, err := ec.prepScript(ctx, single, multiRdr)
	if err != nil {
		return resp, &execCmdErr{err: fmt.Errorf("can't prepare script on %s: %w", ec.hostAddr, err), exec: *ec}
	}
	defer func() {
		if teardown == nil {
			return
		}
		if tErr := teardown(); tErr != nil {
			log.Printf("[WARN] can't teardown script on %s: %v", ec.hostAddr, tErr)
			if err == nil { // if there is no error yet, set teardown error
				err = ec.error(tErr)
			}
		}
	}()
	resp.details = fmt.Sprintf(" {script: %s}", c)
	if ec.cmd.Options.Sudo {
		resp.details = fmt.Sprintf(" {script: %s, sudo: true}", c)
		if strings.HasPrefix(c, ec.shell()+" ") { // single line script already has sh -c
			c = fmt.Sprintf("sudo %s", c)
		} else {
			c = fmt.Sprintf("sudo %s -c %q", ec.shell(), c)
		}
	}
	resp.verbose = scr

	out, err := ec.exec.Run(ctx, c, &executor.RunOpts{Verbose: ec.verbose})
	if err != nil {
		return resp, ec.errorFmt("can't run script on %s: %w", ec.hostAddr, err)
	}

	// collect setvar output to vars and latter it will be set to the environment. This is needed for the next commands.
	// setenv output is in the format of "setenv foo=bar" and it is appended to the output by the script itself.
	// this part done inside exec.scriptFile function.
	resp.vars = make(map[string]string)       // all variables set by the script, used for the next commands in the same task
	resp.registered = make(map[string]string) // only variables that are registered used for the next tasks too
	for _, line := range out {
		if !strings.HasPrefix(line, "setvar ") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "setvar"), "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		resp.vars[key] = val

		// use both original and processed register variables for matching
		for i, regVar := range ec.cmd.Register {
			processed := processedRegister[i]
			if key == regVar || key == processed {
				resp.registered[key] = val
				break
			}
		}
	}

	return resp, nil
}

// Copy uploads a single file or multiple files (if wildcard is used) to a target host.
// if sudo option is set, it will make a temporary directory and upload the files there,
// then move it to the final destination with sudo script execution.
func (ec *execCmd) Copy(ctx context.Context) (resp execCmdResp, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}

	src := tmpl.apply(ec.cmd.Copy.Source)
	dst := tmpl.apply(ec.cmd.Copy.Dest)

	if !ec.cmd.Options.Sudo {
		// if sudo is not set, we can use the original destination and upload the file directly
		resp.details = fmt.Sprintf(" {copy: %s -> %s}", src, dst)
		opts := &executor.UpDownOpts{Mkdir: ec.cmd.Copy.Mkdir, Force: ec.cmd.Copy.Force, Exclude: ec.cmd.Copy.Exclude}
		if err := ec.exec.Upload(ctx, src, dst, opts); err != nil {
			return resp, ec.errorFmt("can't copy file to %s: %w", ec.hostAddr, err)
		}
		if ec.cmd.Copy.ChmodX {
			if _, err := ec.exec.Run(ctx, fmt.Sprintf("chmod +x %s", dst), &executor.RunOpts{Verbose: ec.verbose}); err != nil {
				return resp, ec.errorFmt("can't chmod +x file on %s: %w", ec.hostAddr, err)
			}
			resp.details = fmt.Sprintf(" {copy: %s -> %s, chmod: +x}", src, dst)
		}
		return resp, nil
	}

	if ec.cmd.Options.Sudo {
		// if sudo is set, we need to upload the file to a temporary directory and move it to the final destination
		tmpRemoteDir := ec.uniqueTmp(tmpRemoteDirPrefix)
		resp.details = fmt.Sprintf(" {copy: %s -> %s, sudo: true}", src, dst)
		// not using filepath.Join because we want to keep the linux slash, see https://github.com/umputun/spot/issues/144
		tmpDest := tmpRemoteDir + "/" + filepath.Base(dst)

		// upload to a temporary directory with mkdir
		err := ec.exec.Upload(ctx, src, tmpDest, &executor.UpDownOpts{Mkdir: true, Force: true, Exclude: ec.cmd.Copy.Exclude})
		if err != nil {
			return resp, ec.errorFmt("can't copy file to %s: %w", ec.hostAddr, err)
		}
		defer func() {
			// remove temporary directory we created under /tmp/.spot-<rand>
			if e := ec.exec.Delete(ctx, tmpRemoteDir, &executor.DeleteOpts{Recursive: true}); e != nil {
				log.Printf("[WARN] can't remove temporary directory %q on %s: %v", tmpRemoteDir, ec.hostAddr, e)
			}
		}()

		mvCmd := fmt.Sprintf("mv -f %s %s", tmpDest, dst) // move a single file
		if strings.Contains(src, "*") && !strings.HasSuffix(tmpDest, "/") {
			mvCmd = fmt.Sprintf("mkdir -p %s\nmv -f %s/* %s", dst, tmpDest, dst) // move multiple files, if wildcard is used
		}
		if ec.cmd.Copy.Mkdir {
			mvCmd = fmt.Sprintf("mkdir -p %s\n%s", filepath.Dir(dst), mvCmd) // create directory before moving
		}
		c, _, _, err := ec.prepScript(ctx, mvCmd, nil)
		if err != nil {
			return resp, ec.errorFmt("can't prepare sudo moving command on %s: %w", ec.hostAddr, err)
		}

		// run move command with sudo
		for _, line := range strings.Split(c, "\n") {
			sudoMove := fmt.Sprintf("sudo %s", line)
			if _, err := ec.exec.Run(ctx, sudoMove, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
				return resp, ec.errorFmt("can't move file to %s: %w", ec.hostAddr, err)
			}
		}
		if ec.cmd.Copy.ChmodX {
			if _, err := ec.exec.Run(ctx, fmt.Sprintf("sudo chmod +x %s", dst), &executor.RunOpts{Verbose: ec.verbose}); err != nil {
				return resp, ec.errorFmt("can't chmod +x file on %s: %w", ec.hostAddr, err)
			}
			resp.details = fmt.Sprintf(" {copy: %s -> %s, sudo: true, chmod: +x}", src, dst)
		}
	}

	return resp, nil
}

// Mcopy uploads multiple files to a target host. It calls copy function for each file.
func (ec *execCmd) Mcopy(ctx context.Context) (resp execCmdResp, err error) {
	msgs := []string{}
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	for _, c := range ec.cmd.MCopy {
		src := tmpl.apply(c.Source)
		dst := tmpl.apply(c.Dest)
		msgs = append(msgs, fmt.Sprintf("%s -> %s", src, dst))
		ecSingle := ec
		ecSingle.cmd.Copy = config.CopyInternal{Source: src, Dest: dst, Mkdir: c.Mkdir, Force: c.Force,
			ChmodX: c.ChmodX, Exclude: c.Exclude}
		if _, err := ecSingle.Copy(ctx); err != nil {
			return resp, ec.errorFmt("can't copy file to %s: %w", ec.hostAddr, err)
		}
	}
	resp.details = fmt.Sprintf(" {copy: %s}", strings.Join(msgs, ", "))
	return resp, nil
}

// Sync synchronizes files from a source to a destination on a target host.
func (ec *execCmd) Sync(ctx context.Context) (resp execCmdResp, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	src := tmpl.apply(ec.cmd.Sync.Source)
	dst := tmpl.apply(ec.cmd.Sync.Dest)
	resp.details = fmt.Sprintf(" {sync: %s -> %s}", src, dst)
	opts := &executor.SyncOpts{Delete: ec.cmd.Sync.Delete, Exclude: ec.cmd.Sync.Exclude, Force: ec.cmd.Sync.Force}
	if _, err := ec.exec.Sync(ctx, src, dst, opts); err != nil {
		return resp, ec.errorFmt("can't sync files on %s: %w", ec.hostAddr, err)
	}
	return resp, nil
}

// Msync synchronizes multiple locations from a source to a destination on a target host.
func (ec *execCmd) Msync(ctx context.Context) (resp execCmdResp, err error) {
	msgs := []string{}
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	for _, c := range ec.cmd.MSync {
		src := tmpl.apply(c.Source)
		dst := tmpl.apply(c.Dest)
		msgs = append(msgs, fmt.Sprintf("%s -> %s", src, dst))
		ecSingle := ec
		ecSingle.cmd.Sync = config.SyncInternal{Source: src, Dest: dst, Exclude: c.Exclude, Delete: c.Delete, Force: c.Force}
		if _, err := ecSingle.Sync(ctx); err != nil {
			return resp, ec.errorFmt("can't sync %s to %s %s: %w", src, ec.hostAddr, dst, err)
		}
	}
	resp.details = fmt.Sprintf(" {sync: %s}", strings.Join(msgs, ", "))
	return resp, nil
}

// Delete deletes files on a target host. If sudo option is set, it will execute a sudo rm commands.
func (ec *execCmd) Delete(ctx context.Context) (resp execCmdResp, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	loc := tmpl.apply(ec.cmd.Delete.Location)

	if !ec.cmd.Options.Sudo {
		// if sudo is not set, we can delete the file directly
		if err := ec.exec.Delete(ctx, loc, &executor.DeleteOpts{Recursive: ec.cmd.Delete.Recursive}); err != nil {
			return resp, ec.errorFmt("can't delete files on %s: %w", ec.hostAddr, err)
		}
		resp.details = fmt.Sprintf(" {delete: %s, recursive: %v}", loc, ec.cmd.Delete.Recursive)
	}

	if ec.cmd.Options.Sudo {
		// if sudo is set, we need to delete the file using sudo by ssh-ing into the host and running the command
		cmd := fmt.Sprintf("sudo rm -f %s", loc)
		if ec.cmd.Delete.Recursive {
			cmd = fmt.Sprintf("sudo rm -rf %s", loc)
		}
		if _, err := ec.exec.Run(ctx, cmd, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
			return resp, ec.errorFmt("can't delete file(s) on %s: %w", ec.hostAddr, err)
		}
		resp.details = fmt.Sprintf(" {delete: %s, recursive: %v, sudo: true}", loc, ec.cmd.Delete.Recursive)
	}

	return resp, nil
}

// MDelete deletes multiple locations on a target host.
func (ec *execCmd) MDelete(ctx context.Context) (resp execCmdResp, err error) {
	msgs := []string{}
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	for _, c := range ec.cmd.MDelete {
		loc := tmpl.apply(c.Location)
		ecSingle := ec
		ecSingle.cmd.Delete = config.DeleteInternal{Location: loc, Recursive: c.Recursive}
		if _, err := ecSingle.Delete(ctx); err != nil {
			return resp, ec.errorFmt("can't delete %s on %s: %w", loc, ec.hostAddr, err)
		}
		msgs = append(msgs, loc)
	}
	resp.details = fmt.Sprintf(" {delete: %s}", strings.Join(msgs, ", "))
	return resp, nil
}

// Wait waits for a command to complete on a target hostAddr. It runs the command in a loop with a check duration
// until the command succeeds or the timeout is exceeded.
func (ec *execCmd) Wait(ctx context.Context) (resp execCmdResp, err error) {
	single, multiRdr := ec.cmd.GetWait()
	c, script, teardown, err := ec.prepScript(ctx, single, multiRdr)
	if err != nil {
		return resp, ec.errorFmt("can't prepare script on %s: %w", ec.hostAddr, err)
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

	resp.details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v}",
		c, timeout.Truncate(100*time.Millisecond), duration.Truncate(100*time.Millisecond))

	waitCmd := fmt.Sprintf("%s -c %q", ec.shell(), c) // run wait command in a shell
	if ec.cmd.Options.Sudo {
		resp.details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v, sudo: true}",
			c, timeout.Truncate(100*time.Millisecond), duration.Truncate(100*time.Millisecond))
		waitCmd = fmt.Sprintf("sudo %s -c %q", ec.shell(), c) // add sudo if needed
	}
	resp.verbose = script

	checkTk := time.NewTicker(duration)
	defer checkTk.Stop()
	timeoutTk := time.NewTicker(timeout)
	defer timeoutTk.Stop()

	for {
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-timeoutTk.C:
			return resp, ec.errorFmt("timeout exceeded")
		case <-checkTk.C:
			if _, err := ec.exec.Run(ctx, waitCmd, nil); err == nil {
				return resp, nil // command succeeded
			}
		}
	}
}

// Echo prints a message. It enforces the echo command to start with "echo " and adds sudo if needed.
// It returns the result of the echo command as details string.
func (ec *execCmd) Echo(ctx context.Context) (resp execCmdResp, err error) {
	// check the condition if it exists
	cond, err := ec.checkCondition(ctx)
	if err != nil {
		return resp, err
	}
	if !cond {
		resp.details = fmt.Sprintf(" {skip: %s}", ec.cmd.Name)
		return resp, nil
	}

	// only proceed with echo if there was no condition or condition passed
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	echoCmd := tmpl.apply(ec.cmd.Echo)
	if !strings.HasPrefix(echoCmd, "echo ") {
		echoCmd = fmt.Sprintf("echo %q", echoCmd)
	}
	if ec.cmd.Options.Sudo {
		echoCmd = fmt.Sprintf("sudo %s -c '%s'", ec.shell(), echoCmd)
	}
	out, err := ec.exec.Run(ctx, echoCmd, nil)
	if err != nil {
		return resp, ec.errorFmt("can't run echo command on %s: %w", ec.hostAddr, err)
	}
	resp.details = fmt.Sprintf(" {echo: %s}", strings.Join(out, "; "))
	return resp, nil
}

func (ec *execCmd) checkCondition(ctx context.Context) (bool, error) {
	if ec.cmd.Condition == "" {
		return true, nil // no condition, always allow
	}

	single, multiRdr, inverted := ec.cmd.GetCondition()
	c, _, teardown, err := ec.prepScript(ctx, single, multiRdr)
	if err != nil {
		return false, ec.errorFmt("can't prepare condition script on %s: %w", ec.hostAddr, err)
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
		c = fmt.Sprintf("sudo %s -c %q", ec.shell(), c)
	}

	// run the condition command
	if _, err := ec.exec.Run(ctx, c, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
		log.Printf("[DEBUG] condition not passed on %s: %v", ec.hostAddr, err)
		if inverted {
			return true, nil // inverted condition failed, so we return true
		}
		return false, nil
	}

	// if condition passed
	if inverted {
		return false, nil // inverted condition passed, so we return false
	}
	return true, nil
}

// prepScript prepares a script for execution. Script can be either a single command or a multiline script.
// In case of a single command, it just applies templates to it. In case of a multiline script, it creates
// a temporary file with the script chmod as +x and uploads to remote host to /tmp.
// it also  returns a teardown function to remove the temporary file after the command execution.
func (ec *execCmd) prepScript(ctx context.Context, s string, r io.Reader) (cmd, scr string, teardown func() error, err error) {
	tmpl := templater{
		hostAddr: ec.hostAddr,
		hostName: ec.hostName,
		task:     ec.tsk,
		command:  ec.cmd.Name,
		env:      ec.cmd.Environment, // include environment variables for variable expansion
	}

	if s != "" { // single command, nothing to do just apply templates
		return tmpl.apply(s), "", nil, nil
	}

	// multiple commands, create a temporary script

	// read the script from the reader and apply templates
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, r); err != nil {
		return "", "", nil, ec.errorFmt("can't read script: %w", err)
	}
	rdr := bytes.NewBuffer([]byte(tmpl.apply(buf.String())))

	// prepare scr(ipt) for reporting
	for _, l := range strings.Split(rdr.String(), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		scr += fmt.Sprintf(" + %s\n", strings.ReplaceAll(l, "%", "%%"))
	}

	// make a temporary file and copy the script to it
	tmp, err := os.CreateTemp("", "spot-script")
	if err != nil {
		return "", "", nil, ec.errorFmt("can't create temporary file: %w", err)
	}
	if _, err = io.Copy(tmp, rdr); err != nil {
		return "", "", nil, ec.errorFmt("can't copy script to temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", "", nil, ec.errorFmt("can't close temporary file: %w", err)
	}
	defer func() {
		// remove local copy of the script after upload or in case of error
		if err := os.Remove(tmp.Name()); err != nil {
			log.Printf("[WARN] can't remove local temp script %s: %v", tmp.Name(), err)
		} else {
			log.Printf("[DEBUG] removed local temp script %s", tmp.Name())
		}
	}()
	// make the script executable locally, upload preserves the permissions
	if err = os.Chmod(tmp.Name(), 0o700); err != nil { // nolint
		return "", "", nil, ec.errorFmt("can't chmod temporary file: %w", err)
	}

	// get temp file name for remote hostAddr
	tmpRemoteDir := ec.uniqueTmp(tmpRemoteDirPrefix)
	// the direct join with unix separator is intentional, we want to preserve the slash regardless of the host's OS
	// see https://github.com/umputun/spot/issues/138
	dst := tmpRemoteDir + "/" + filepath.Base(tmp.Name()) // nolint
	scr = fmt.Sprintf("script: %s\n", dst) + scr

	// upload the script to the remote hostAddr
	if err = ec.exec.Upload(ctx, tmp.Name(), dst, &executor.UpDownOpts{Mkdir: true}); err != nil {
		return "", "", nil, ec.errorFmt("can't upload script to %s: %w", ec.hostAddr, err)
	}
	cmd = fmt.Sprintf("%s -c %s", ec.shell(), dst)

	teardown = func() error {
		log.Printf("[DEBUG] removed local temp script %s", tmp.Name())

		// remove the temp dir with the script from the remote hostAddr,
		// should be invoked by the caller after the command is executed
		if err := ec.exec.Delete(ctx, tmpRemoteDir, &executor.DeleteOpts{Recursive: true}); err != nil {
			return ec.errorFmt("can't remove temporary remote script %s (%s): %w", dst, ec.hostAddr, err)
		}
		return nil
	}

	return cmd, scr, teardown, nil
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
// it also applies the task environment variables to strings
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

	// split hostAddr to SPOT_REMOTE_ADDR and SPOT_REMOTE_PORT
	host, port, err := net.SplitHostPort(tm.hostAddr)
	if err == nil {
		res = apply(res, "SPOT_REMOTE_ADDR", host)
		res = apply(res, "SPOT_REMOTE_PORT", port)
	} else {
		res = apply(res, "SPOT_REMOTE_ADDR", tm.hostAddr)
		res = apply(res, "SPOT_REMOTE_PORT", "22") // set to default ssh port
	}

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

func (ec *execCmd) uniqueTmp(defaultPrefix string) string {
	prefix := defaultPrefix
	if ec.sshTmpDir != "" {
		_, last := filepath.Split(defaultPrefix) // get the last part of the path from default
		prefix = filepath.Join(ec.sshTmpDir, last)
	}
	rndInt := func() int64 {
		var randomInt int64
		// try using the cryptographic random number generator
		err := binary.Read(rand.Reader, binary.BigEndian, &randomInt)
		if err == nil {
			return int64(math.Abs(float64(randomInt)))
		}
		// fallback to math/rand
		randomInt = mr.Int63() // nolint gosec // this used as fallback only
		return randomInt
	}
	return fmt.Sprintf("%s%d", prefix, rndInt())
}

func (ec *execCmd) error(err error) *execCmdErr {
	return &execCmdErr{err: err, exec: *ec}
}

func (ec *execCmd) errorFmt(format string, a ...any) *execCmdErr {
	return &execCmdErr{err: fmt.Errorf(format, a...), exec: *ec}
}

func (ec *execCmd) shell() string {
	if ec.sshShell == "" {
		return "/bin/sh"
	}
	if ec.cmd.Options.Local {
		if os.Getenv("SHELL") == "" {
			return "/bin/sh" // default to /bin/sh if SHELL env var is not set
		}
		return os.Getenv("SHELL") // local commands always use local sh
	}
	return ec.sshShell
}
