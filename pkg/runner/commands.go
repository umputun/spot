package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	mr "math/rand"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
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
			c = ec.wrapWithSudo(c)
		} else {
			c = ec.wrapWithSudo(fmt.Sprintf("%s -c %q", ec.shell(), c))
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
		// parse format: key=value or key:SQ=value for single-quoted values
		trimmed := strings.TrimPrefix(line, "setvar ")
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}

		keyPart := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// check if this was a single-quoted variable
		var key string
		var singleQuoted bool
		if strings.HasSuffix(keyPart, ":SQ") {
			key = keyPart[:len(keyPart)-3]
			singleQuoted = true
		} else {
			key = keyPart
			singleQuoted = false
		}

		// for single-quoted values, add a marker prefix
		if singleQuoted {
			val = "__SQ__:" + val
		}
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

	// default to "push" if direction is not set (extra safety, config should set it)
	direction := ec.cmd.Copy.Direction
	if direction == "" {
		direction = "push"
	}

	// validate direction
	if direction != "push" && direction != "pull" {
		return resp, ec.errorFmt("invalid copy direction %q, must be 'push' or 'pull'", direction)
	}

	// check for invalid combinations
	if ec.cmd.Options.Local && direction == "pull" {
		return resp, ec.errorFmt("cannot use direction=pull with local execution")
	}

	// warn if chmod+x is used with pull
	if direction == "pull" && ec.cmd.Copy.ChmodX {
		log.Printf("[WARN] chmod+x ignored for download operations")
	}

	// handle download (pull) direction
	if direction == "pull" {
		return ec.copyPull(ctx, src, dst)
	}

	// handle upload (push) direction
	return ec.copyPush(ctx, src, dst)
}

// copyPush uploads files from local machine to remote host
func (ec *execCmd) copyPush(ctx context.Context, src, dst string) (resp execCmdResp, err error) {
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

	// if sudo is set, we need to upload the file to a temporary directory and move it to the final destination
	tmpRemoteDir := ec.uniqueTmp(tmpRemoteDirPrefix)
	resp.details = fmt.Sprintf(" {copy: %s -> %s, sudo: true}", src, dst)
	// not using filepath.Join because we want to keep the linux slash, see https://github.com/umputun/spot/issues/144
	tmpDest := tmpRemoteDir + "/" + filepath.Base(dst)

	// upload to a temporary directory with mkdir
	err = ec.exec.Upload(ctx, src, tmpDest, &executor.UpDownOpts{Mkdir: true, Force: true, Exclude: ec.cmd.Copy.Exclude})
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
	for line := range strings.SplitSeq(c, "\n") {
		sudoMove := ec.wrapWithSudo(line)
		if _, err := ec.exec.Run(ctx, sudoMove, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
			return resp, ec.errorFmt("can't move file to %s: %w", ec.hostAddr, err)
		}
	}
	if ec.cmd.Copy.ChmodX {
		chmodCmd := ec.wrapWithSudo(fmt.Sprintf("chmod +x %s", dst))
		if _, err := ec.exec.Run(ctx, chmodCmd, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
			return resp, ec.errorFmt("can't chmod +x file on %s: %w", ec.hostAddr, err)
		}
		resp.details = fmt.Sprintf(" {copy: %s -> %s, sudo: true, chmod: +x}", src, dst)
	}

	return resp, nil
}

// copyPull downloads files from remote host to local machine
func (ec *execCmd) copyPull(ctx context.Context, src, dst string) (resp execCmdResp, err error) {
	if !ec.cmd.Options.Sudo {
		// direct download without sudo
		resp.details = fmt.Sprintf(" {copy: %s <- %s, direction: pull}", dst, src)
		opts := &executor.UpDownOpts{
			Mkdir:   ec.cmd.Copy.Mkdir,
			Force:   ec.cmd.Copy.Force,
			Exclude: ec.cmd.Copy.Exclude,
		}
		if err := ec.exec.Download(ctx, src, dst, opts); err != nil {
			return resp, ec.errorFmt("can't download file from %s: %w", ec.hostAddr, err)
		}
		return resp, nil
	}

	// sudo handling for download - copy to temp on remote with sudo, then download
	tmpRemoteDir := ec.uniqueTmp("/tmp/.spot-download-")
	resp.details = fmt.Sprintf(" {copy: %s <- %s, direction: pull, sudo: true}", dst, src)

	// check if source contains glob pattern
	hasGlob := strings.ContainsAny(src, "*?[")

	var cpCmd string
	var downloadSrc string

	if hasGlob {
		// for glob patterns: don't quote source to allow shell expansion, copy all to temp dir
		// the destination should be just the directory, not include the glob pattern
		// use -L to dereference symlinks - relative symlinks become dangling when copied to /tmp
		cpCmd = fmt.Sprintf("mkdir -p %q && cp -rL %s %q && chmod -R +r %q",
			tmpRemoteDir, src, tmpRemoteDir, tmpRemoteDir)
		// download entire temp directory content
		// not using path.Join to keep the linux slash
		downloadSrc = tmpRemoteDir + "/*"
	} else {
		// for single files: quote everything (existing behavior)
		// not using filepath.Join to keep the linux slash
		// use -L to dereference symlinks - relative symlinks become dangling when copied to /tmp
		tmpSrc := tmpRemoteDir + "/" + filepath.Base(src)
		cpCmd = fmt.Sprintf("mkdir -p %q && cp -rL %q %q && chmod -R +r %q",
			tmpRemoteDir, src, tmpSrc, tmpSrc)
		downloadSrc = tmpSrc
	}

	// run copy with sudo on remote - this wraps the entire command sequence
	sudoCmd := ec.wrapWithSudo(fmt.Sprintf("%s -c %q", ec.shell(), cpCmd))
	if _, err := ec.exec.Run(ctx, sudoCmd, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
		return resp, ec.errorFmt("can't prepare file for download with sudo on %s: %w", ec.hostAddr, err)
	}

	// cleanup function to remove temp directory on remote
	defer func() {
		cleanCmd := ec.wrapWithSudo(fmt.Sprintf("rm -rf %q", tmpRemoteDir))
		if _, e := ec.exec.Run(ctx, cleanCmd, &executor.RunOpts{Verbose: false}); e != nil {
			log.Printf("[WARN] can't remove temporary directory %q on %s: %v", tmpRemoteDir, ec.hostAddr, e)
		}
	}()

	// download from temp location (no sudo needed now)
	opts := &executor.UpDownOpts{
		Mkdir:   ec.cmd.Copy.Mkdir,
		Force:   ec.cmd.Copy.Force,
		Exclude: ec.cmd.Copy.Exclude,
	}
	if err := ec.exec.Download(ctx, downloadSrc, dst, opts); err != nil {
		return resp, ec.errorFmt("can't download file from %s: %w", ec.hostAddr, err)
	}

	return resp, nil
}

// Mcopy uploads or downloads multiple files to/from a target host. It calls copy function for each file.
func (ec *execCmd) Mcopy(ctx context.Context) (resp execCmdResp, err error) {
	msgs := []string{}
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	for _, c := range ec.cmd.MCopy {
		src := tmpl.apply(c.Source)
		dst := tmpl.apply(c.Dest)
		arrow := "->"
		if c.Direction == "pull" {
			arrow = "<-"
		}
		msgs = append(msgs, fmt.Sprintf("%s %s %s", src, arrow, dst))
		ecSingle := ec
		ecSingle.cmd.Copy = config.CopyInternal{Source: src, Dest: dst, Direction: c.Direction, Mkdir: c.Mkdir,
			Force: c.Force, ChmodX: c.ChmodX, Exclude: c.Exclude}
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
	opts := &executor.SyncOpts{Delete: ec.cmd.Sync.Delete, Exclude: ec.cmd.Sync.Exclude}
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
		ecSingle.cmd.Sync = config.SyncInternal{Source: src, Dest: dst, Exclude: c.Exclude, Delete: c.Delete}
		if _, err := ecSingle.Sync(ctx); err != nil {
			return resp, ec.errorFmt("can't sync %s to %s %s: %w", src, ec.hostAddr, dst, err)
		}
	}
	resp.details = fmt.Sprintf(" {sync: %s}", strings.Join(msgs, ", "))
	return resp, nil
}

// checkDeleteExclude validates the exclude option against the other delete flags for the given location.
// Exclude requires a recursive delete (it filters directory contents) and cannot be combined with sudo,
// as sudo deletion runs a plain rm that cannot apply exclusion patterns. Returns an error naming the
// offending location, or nil if the combination is valid.
func (ec *execCmd) checkDeleteExclude(loc string, del config.DeleteInternal) error {
	if len(del.Exclude) == 0 {
		return nil
	}
	if !del.Recursive {
		return ec.errorFmt("delete with exclude requires recursive delete (recur: true) for %q", loc)
	}
	if ec.cmd.Options.Sudo {
		return ec.errorFmt("delete with exclude is not supported with sudo for %q", loc)
	}
	return nil
}

// Delete deletes files on a target host. If sudo option is set, it will execute a sudo rm commands.
func (ec *execCmd) Delete(ctx context.Context) (resp execCmdResp, err error) {
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	loc := tmpl.apply(ec.cmd.Delete.Location)

	if e := ec.checkDeleteExclude(loc, ec.cmd.Delete); e != nil {
		return resp, e
	}

	if !ec.cmd.Options.Sudo {
		// if sudo is not set, we can delete the file directly
		opts := &executor.DeleteOpts{Recursive: ec.cmd.Delete.Recursive, Exclude: ec.cmd.Delete.Exclude}
		if err := ec.exec.Delete(ctx, loc, opts); err != nil {
			return resp, ec.errorFmt("can't delete files on %s: %w", ec.hostAddr, err)
		}
		resp.details = fmt.Sprintf(" {delete: %s, recursive: %v}", loc, ec.cmd.Delete.Recursive)
	}

	if ec.cmd.Options.Sudo {
		// if sudo is set, we need to delete the file using sudo by ssh-ing into the host and running the command
		cmd := fmt.Sprintf("rm -f %s", loc)
		if ec.cmd.Delete.Recursive {
			cmd = fmt.Sprintf("rm -rf %s", loc)
		}
		cmd = ec.wrapWithSudo(cmd)
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

	// validate all locations upfront, before any of them is deleted
	for _, c := range ec.cmd.MDelete {
		if e := ec.checkDeleteExclude(tmpl.apply(c.Location), c); e != nil {
			return resp, e
		}
	}

	for _, c := range ec.cmd.MDelete {
		loc := tmpl.apply(c.Location)
		ecSingle := ec
		ecSingle.cmd.Delete = config.DeleteInternal{Location: loc, Recursive: c.Recursive, Exclude: c.Exclude}
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
		if tErr := teardown(); tErr != nil {
			log.Printf("[WARN] can't teardown script on %s: %v", ec.hostAddr, tErr)
			if err == nil { // don't overwrite the primary error with teardown error
				err = ec.error(tErr)
			}
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
		waitCmd = ec.wrapWithSudo(fmt.Sprintf("%s -c %q", ec.shell(), c)) // add sudo if needed
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
		echoCmd = ec.wrapWithSudo(fmt.Sprintf("%s -c '%s'", ec.shell(), echoCmd))
	}
	out, err := ec.exec.Run(ctx, echoCmd, nil)
	if err != nil {
		return resp, ec.errorFmt("can't run echo command on %s: %w", ec.hostAddr, err)
	}
	resp.details = fmt.Sprintf(" {echo: %s}", strings.Join(out, "; "))
	return resp, nil
}

// Line modifies file content by operating on lines that match a regex pattern.
// It supports three operations: delete lines, replace lines, or append a line if pattern not found.
func (ec *execCmd) Line(ctx context.Context) (resp execCmdResp, err error) {
	// check the condition if it exists
	cond, err := ec.checkCondition(ctx)
	if err != nil {
		return resp, err
	}
	if !cond {
		resp.details = fmt.Sprintf(" {skip: %s}", ec.cmd.Name)
		return resp, nil
	}

	// apply templating to all fields
	tmpl := templater{
		hostAddr: ec.hostAddr,
		hostName: ec.hostName,
		task:     ec.tsk,
		command:  ec.cmd.Name,
		env:      ec.cmd.Environment,
	}
	file := tmpl.apply(ec.cmd.Line.File)
	match := tmpl.apply(ec.cmd.Line.Match)
	replace := tmpl.apply(ec.cmd.Line.Replace)
	appendLine := tmpl.apply(ec.cmd.Line.Append)

	// determine the operation and build the command
	var operation string
	var operationCmd string

	switch {
	case ec.cmd.Line.Delete:
		operation = "delete"
		// use sed to delete lines matching the pattern
		operationCmd = fmt.Sprintf("sed -i '\\|%s|d' %s", match, file)

	case replace != "":
		operation = "replace"
		// use sed to replace lines matching the pattern.
		// strip leading ^ and trailing $ from match since sed pattern already wraps with ^.* and .*$
		sedMatch := strings.TrimPrefix(match, "^")
		sedMatch = strings.TrimSuffix(sedMatch, "$")
		operationCmd = fmt.Sprintf("sed -i 's|^.*%s.*$|%s|' %s", sedMatch, replace, file)

	case appendLine != "":
		operation = "append"
		// first check if the pattern exists in the file
		checkCmd := fmt.Sprintf("grep -q '%s' %s", match, file)
		checkCmd = ec.wrapWithSudo(checkCmd)
		if _, err := ec.exec.Run(ctx, checkCmd, &executor.RunOpts{Verbose: ec.verbose}); err != nil {
			// pattern not found, append the line using tee -a for proper sudo support
			if ec.cmd.Options.Sudo {
				operationCmd = fmt.Sprintf("echo '%s' | sudo tee -a %s > /dev/null", appendLine, file)
			} else {
				operationCmd = fmt.Sprintf("echo '%s' | tee -a %s > /dev/null", appendLine, file)
			}
		} else {
			// pattern found, skip
			resp.details = fmt.Sprintf(" {line: %s, match: %s, skip: pattern found}", file, match)
			return resp, nil
		}

	default:
		return resp, ec.errorFmt("invalid line command configuration: no operation specified")
	}

	// handle sudo if needed (append operation handles sudo internally)
	if operation != "append" {
		operationCmd = ec.wrapWithSudo(operationCmd)
	}

	// execute the operation
	_, err = ec.exec.Run(ctx, operationCmd, &executor.RunOpts{Verbose: ec.verbose})
	if err != nil {
		return resp, ec.errorFmt("can't execute line %s on %s: %w", operation, ec.hostAddr, err)
	}

	resp.details = fmt.Sprintf(" {line: %s, %s: %s}", file, operation, match)
	return resp, nil
}

// Template renders a local Go text/template file with the command environment and SPOT_* variables,
// then uploads the result to a remote host. It supports mkdir, force and chmod+x options, and works
// with sudo the same way as the copy command.
func (ec *execCmd) Template(ctx context.Context) (resp execCmdResp, err error) {
	cond, err := ec.checkCondition(ctx)
	if err != nil {
		return resp, err
	}
	if !cond {
		resp.details = fmt.Sprintf(" {skip: %s}", ec.cmd.Name)
		return resp, nil
	}

	// resolve src/dst paths via spot's variable substitution
	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	src := tmpl.apply(ec.cmd.Template.Source)
	dst := tmpl.apply(ec.cmd.Template.Dest)

	// read template file from local filesystem
	data, err := os.ReadFile(src) // nolint:gosec // user-configured template path
	if err != nil {
		return resp, ec.errorFmt("can't read template %q: %w", src, err)
	}

	tplData := tmpl.vars()
	for _, k := range ec.cmd.Options.Secrets {
		if v, ok := ec.cmd.Secrets[k]; ok {
			tplData[k] = v
		}
	}

	// parse and execute template
	parsed, err := template.New(filepath.Base(src)).Option("missingkey=error").Parse(string(data))
	if err != nil {
		return resp, ec.errorFmt("can't parse template %q: %w", src, err)
	}
	var rendered bytes.Buffer
	if err = parsed.Execute(&rendered, tplData); err != nil {
		return resp, ec.errorFmt("can't execute template %q: %w", src, err)
	}

	// write rendered output to a temp file for upload
	tmp, err := os.CreateTemp("", "spot-template")
	if err != nil {
		return resp, ec.errorFmt("can't create temp file for rendered template: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if rErr := os.Remove(tmpName); rErr != nil {
			log.Printf("[WARN] can't remove temp template file %s: %v", tmpName, rErr)
		}
	}()
	if _, err = tmp.Write(rendered.Bytes()); err != nil {
		tmp.Close() // nolint:gosec // close best-effort before returning error
		return resp, ec.errorFmt("can't write rendered template to temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return resp, ec.errorFmt("can't close temp file for rendered template: %w", err)
	}

	// determine file mode for the rendered file. defaults to 0600 so secret-bearing
	// renders stay locked down; users can set 0644 for plain configs.
	modeStr := ec.cmd.Template.Mode
	if modeStr == "" {
		modeStr = "0600"
	}
	modeVal, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return resp, ec.errorFmt("can't parse mode %q: %w", modeStr, err)
	}
	if err = os.Chmod(tmpName, os.FileMode(modeVal)); err != nil {
		return resp, ec.errorFmt("can't chmod temp template file to %s: %w", modeStr, err)
	}

	// set mtime from content hash so unchanged renders produce the same (size, mtime, mode)
	// tuple as the remote file, making upload idempotent under force: false
	sum := sha256.Sum256(rendered.Bytes())
	contentTime := time.Unix(int64(binary.BigEndian.Uint64(sum[:8])&(1<<63-1)), 0)
	if err = os.Chtimes(tmpName, contentTime, contentTime); err != nil {
		return resp, ec.errorFmt("can't set mtime on temp template file: %w", err)
	}

	// reuse copyPush for the actual upload, mapping template options onto a synthetic copy command.
	// if mode is explicitly set, the user controls exact permissions; chmod+x is only meaningful
	// with the default 0600 mode.
	chmodX := ec.cmd.Template.ChmodX && ec.cmd.Template.Mode == ""
	ecCopy := *ec
	ecCopy.cmd.Copy = config.CopyInternal{
		Source:    tmpName,
		Dest:      dst,
		Direction: "push",
		Mkdir:     ec.cmd.Template.Mkdir,
		Force:     ec.cmd.Template.Force,
		ChmodX:    chmodX,
	}
	pushResp, err := ecCopy.copyPush(ctx, tmpName, dst)
	if err != nil {
		return resp, err
	}
	resp = pushResp
	// rewrite details prefix and source path to reflect template command
	resp.details = strings.Replace(resp.details, " {copy:", " {template:", 1)
	resp.details = strings.Replace(resp.details, tmpName, src, 1)
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
		c = ec.wrapWithSudo(fmt.Sprintf("%s -c %q", ec.shell(), c))
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
	for l := range strings.SplitSeq(rdr.String(), "\n") {
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

// vars builds a map of all template variables: SPOT_* built-ins and environment variables.
// environment values with the __SQ__: marker (single-quoted) have the prefix stripped.
func (tm *templater) vars() map[string]string {
	host, port, _ := net.SplitHostPort(tm.hostAddr)
	if host == "" {
		host = tm.hostAddr
		port = "22"
	}

	vars := map[string]string{
		"SPOT_REMOTE_HOST": tm.hostAddr,
		"SPOT_REMOTE_NAME": tm.hostName,
		"SPOT_REMOTE_ADDR": host,
		"SPOT_REMOTE_PORT": port,
		"SPOT_REMOTE_USER": tm.task.User,
		"SPOT_COMMAND":     tm.command,
		"SPOT_TASK":        tm.task.Name,
	}
	if tm.err != nil {
		vars["SPOT_ERROR"] = tm.err.Error()
	} else {
		vars["SPOT_ERROR"] = ""
	}

	for k, v := range tm.env {
		if strings.HasPrefix(v, "__SQ__:") {
			vars[k] = v[7:]
		} else {
			vars[k] = v
		}
	}

	return vars
}

// apply applies templates to a string to replace predefined vars placeholders with actual values
// it also applies the task environment variables to strings
func (tm *templater) apply(inp string) string {
	apply := func(inp, from, to string) string {
		// replace ${VAR} format - braces delimit the variable name
		res := strings.ReplaceAll(inp, fmt.Sprintf("${%s}", from), to)

		// replace $VAR format with word-boundary check to avoid matching prefixes
		// e.g., $SUBNET should not match inside $SUBNET_ID
		re := regexp.MustCompile(`\$` + regexp.QuoteMeta(from) + `(?:[^a-zA-Z0-9_]|$)`)
		res = re.ReplaceAllStringFunc(res, func(match string) string {
			suffix := match[len("$"+from):] // preserve trailing character (or empty if at end)
			return to + suffix
		})

		// replace {VAR} format - braces delimit the variable name
		res = strings.ReplaceAll(res, fmt.Sprintf("{%s}", from), to)
		return res
	}

	res := inp
	for k, v := range tm.vars() {
		actualValue := v
		// for env vars that were single-quoted, escape $ to prevent further expansion
		if orig, ok := tm.env[k]; ok && strings.HasPrefix(orig, "__SQ__:") {
			actualValue = strings.ReplaceAll(v, "$", "\\$")
		}
		res = apply(res, k, actualValue)
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

// wrapWithSudo wraps a command with sudo, using password if available
func (ec *execCmd) wrapWithSudo(cmd string) string {
	if !ec.cmd.Options.Sudo {
		return cmd
	}

	if ec.cmd.Options.SudoPassword != "" && ec.cmd.Secrets != nil {
		password, ok := ec.cmd.Secrets[ec.cmd.Options.SudoPassword]
		if ok && password != "" {
			// escape single quotes in password to prevent shell injection
			// in bash, ' is escaped as '\'' inside single quotes
			escapedPassword := strings.ReplaceAll(password, "'", "'\\''")
			// use printf to pipe password to sudo -S
			// note: password will be briefly visible in process list on remote host
			return fmt.Sprintf("printf '%%s\\n' '%s' | sudo -S %s", escapedPassword, cmd)
		}
		if !ok {
			log.Printf("[WARN] sudo_password refers to missing secret key: %s", ec.cmd.Options.SudoPassword)
		}
	}

	// fallback to passwordless sudo
	return fmt.Sprintf("sudo %s", cmd)
}
