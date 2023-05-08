package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-pkgz/syncs"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
)

//go:generate moq -out mocks/connector.go -pkg mocks -skip-ensure -fmt goimports . Connector

// Process is a struct that holds the information needed to run a process.
// It responsible for running a task on a target hosts.
type Process struct {
	Concurrency int
	Connector   Connector
	Config      *config.PlayBook
	ColorWriter *executor.ColorizedWriter
	Verbose     bool
	Dry         bool

	Skip []string
	Only []string

	secrets []string
}

const tmpRemoteDir = "/tmp/.spot" // this is a directory on remote host to store temporary files

// Connector is an interface for connecting to a host, and returning remote executer.
type Connector interface {
	Connect(ctx context.Context, hostAddr, hostName, user string) (*executor.Remote, error)
}

// ProcStats holds the information about processed commands and hosts.
type ProcStats struct {
	Commands int
	Hosts    int
}

// Run runs a task for a set of target hosts. Runs in parallel with limited concurrency,
// each host is processed in separate goroutine.
func (p *Process) Run(ctx context.Context, task, target string) (s ProcStats, err error) {
	tsk, err := p.Config.Task(task)
	if err != nil {
		return ProcStats{}, fmt.Errorf("can't get task %s: %w", task, err)
	}
	log.Printf("[DEBUG] task %q has %d commands", task, len(tsk.Commands))

	targetHosts, err := p.Config.TargetHosts(target)
	if err != nil {
		return ProcStats{}, fmt.Errorf("can't get target %s: %w", target, err)
	}
	log.Printf("[DEBUG] target hosts (%d) %+v", len(targetHosts), targetHosts)

	p.secrets = p.Config.AllSecretValues()

	wg := syncs.NewErrSizedGroup(p.Concurrency, syncs.Context(ctx), syncs.Preemptive)
	var commands int32
	for i, host := range targetHosts {
		i, host := i, host
		wg.Go(func() error {
			count, e := p.runTaskOnHost(ctx, tsk, fmt.Sprintf("%s:%d", host.Host, host.Port), host.Name, host.User)
			if i == 0 {
				atomic.AddInt32(&commands, int32(count))
			}
			if e != nil {
				_, errLog := executor.MakeOutAndErrWriters(fmt.Sprintf("%s:%d", host.Host, host.Port), host.Name, p.Verbose, p.secrets...)
				errLog.Write([]byte(e.Error())) // nolint
			}
			return e
		})
	}
	err = wg.Wait()

	// execute on-error command if any error occurred during task execution and on-error command is defined
	if err != nil && tsk.OnError != "" {
		onErrCmd := exec.CommandContext(ctx, "sh", "-c", tsk.OnError) // nolint we want to run shell here
		onErrCmd.Env = os.Environ()

		outLog, errLog := executor.MakeOutAndErrWriters("localhost", "", p.Verbose, p.secrets...)
		outLog.Write([]byte(tsk.OnError)) // nolint

		var stdoutBuf bytes.Buffer
		mwr := io.MultiWriter(outLog, &stdoutBuf)
		onErrCmd.Stdout, onErrCmd.Stderr = mwr, errLog
		onErrCmd.Stdout, onErrCmd.Stderr = mwr, executor.NewStdoutLogWriter("!", "WARN")
		if exErr := onErrCmd.Run(); exErr != nil {
			log.Printf("[WARN] can't run on-error command %q: %v", tsk.OnError, exErr)
		}
	}

	return ProcStats{Hosts: len(targetHosts), Commands: int(atomic.LoadInt32(&commands))}, err
}

// runTaskOnHost executes all commands of a task on a target host. hostAddr can be a remote host or localhost with port.
func (p *Process) runTaskOnHost(ctx context.Context, tsk *config.Task, hostAddr, hostName, user string) (int, error) {
	contains := func(list []string, s string) bool {
		for _, v := range list {
			if strings.EqualFold(v, s) {
				return true
			}
		}
		return false
	}
	stTask := time.Now()
	remote, err := p.Connector.Connect(ctx, hostAddr, hostName, user)
	if err != nil {
		if hostName != "" {
			return 0, fmt.Errorf("can't connect to %s: %w", hostName, err)
		}
		return 0, err
	}
	defer remote.Close()
	remote.SetSecrets(p.secrets)

	fmt.Fprintf(p.ColorWriter.WithHost(hostAddr, hostName), "run task %q, commands: %d\n", tsk.Name, len(tsk.Commands))
	count := 0
	for _, cmd := range tsk.Commands {
		if len(p.Only) > 0 && !contains(p.Only, cmd.Name) {
			continue
		}
		if len(p.Skip) > 0 && contains(p.Skip, cmd.Name) {
			continue
		}
		if cmd.Options.NoAuto && (len(p.Only) == 0 || !contains(p.Only, cmd.Name)) {
			// skip command if it has NoAuto option and not in Only list
			continue
		}

		log.Printf("[INFO] run command %q on host %q (%s)", cmd.Name, hostAddr, hostName)
		stCmd := time.Now()
		params := execCmdParams{cmd: cmd, hostAddr: hostAddr, hostName: hostName, tsk: tsk, exec: remote}
		if cmd.Options.Local {
			params.exec = &executor.Local{}
			params.exec.SetSecrets(p.secrets)
			params.hostAddr = "localhost"
			params.hostName, _ = os.Hostname() // nolint we don't care about error here
		}

		if p.Dry {
			params.exec = executor.NewDry(hostAddr, hostName)
			params.exec.SetSecrets(p.secrets)
			if cmd.Options.Local {
				params.hostAddr = "localhost"
				params.hostName, _ = os.Hostname() // nolint we don't care about error here
			}
		}

		details, vars, err := p.execCommand(ctx, params)
		if err != nil {
			if !cmd.Options.IgnoreErrors {
				return count, fmt.Errorf("failed command %q on host %s (%s): %w", cmd.Name, hostAddr, hostName, err)
			}

			fmt.Fprintf(p.ColorWriter.WithHost(hostAddr, hostName), "failed command %s%s (%v)",
				cmd.Name, details, time.Since(stCmd).Truncate(time.Millisecond))
			continue
		}

		// set variables from command output, if any
		// this variables will be available for next commands in the same task via environment
		if len(vars) > 0 {
			log.Printf("[DEBUG] set %d variables from command %q: %+v", len(vars), cmd.Name, vars)
			for k, v := range vars {
				for i, c := range tsk.Commands {
					env := c.Environment
					if env == nil {
						env = make(map[string]string)
					}
					if _, ok := env[k]; ok { // don't allow override variables
						continue
					}
					env[k] = v
					tsk.Commands[i].Environment = env
				}
			}
		}

		fmt.Fprintf(p.ColorWriter.WithHost(hostAddr, hostName),
			"completed command %q%s (%v)", cmd.Name, details, time.Since(stCmd).Truncate(time.Millisecond))
		count++
	}

	fmt.Fprintf(p.ColorWriter.WithHost(hostAddr, hostName),
		"completed task %q, commands: %d (%v)\n", tsk.Name, count, time.Since(stTask).Truncate(time.Millisecond))

	return count, nil
}

type execCmdParams struct {
	cmd      config.Cmd
	hostAddr string
	hostName string
	tsk      *config.Task
	exec     executor.Interface
}

// execCommand executes a single command on a target host. It detects command type based on the fields what are set.
// Even if multiple fields for multiple commands are set, only one will be executed.
func (p *Process) execCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	switch {
	case ep.cmd.Script != "":
		log.Printf("[DEBUG] execute script %q on %s", ep.cmd.Name, ep.hostAddr)
		return p.execScriptCommand(ctx, ep)
	case ep.cmd.Copy.Source != "" && ep.cmd.Copy.Dest != "":
		log.Printf("[DEBUG] copy file to %s", ep.hostAddr)
		return p.execCopyCommand(ctx, ep)
	case len(ep.cmd.MCopy) > 0:
		log.Printf("[DEBUG] copy multiple files to %s", ep.hostAddr)
		return p.execMCopyCommand(ctx, ep)
	case ep.cmd.Sync.Source != "" && ep.cmd.Sync.Dest != "":
		log.Printf("[DEBUG] sync files on %s", ep.hostAddr)
		return p.execSyncCommand(ctx, ep)
	case ep.cmd.Delete.Location != "":
		log.Printf("[DEBUG] delete files on %s", ep.hostAddr)
		return p.execDeleteCommand(ctx, ep)
	case ep.cmd.Wait.Command != "":
		log.Printf("[DEBUG] wait for command on %s", ep.hostAddr)
		return p.execWaitCommand(ctx, ep)
	default:
		return "", nil, fmt.Errorf("unknown command %q", ep.cmd.Name)
	}
}

// execScriptCommand executes a script command on a target host. It can be a single line or multiline script,
// this part is translated by the prepScript function.
// If sudo option is set, it will execute the script with sudo.
// If output contains variables as "setvar foo=bar", it will return the variables as map.
func (p *Process) execScriptCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	single, multiRdr := ep.cmd.GetScript()
	c, teardown, err := p.prepScript(ctx, single, multiRdr, ep)
	if err != nil {
		return details, nil, fmt.Errorf("can't prepare script on %s: %w", ep.hostAddr, err)
	}
	defer func() {
		if teardown == nil {
			return
		}
		if err = teardown(); err != nil {
			log.Printf("[WARN] can't teardown script on %s: %v", ep.hostAddr, err)
		}
	}()
	details = fmt.Sprintf(" {script: %s}", c)
	if ep.cmd.Options.Sudo {
		details = fmt.Sprintf(" {script: %s, sudo: true}", c)
		c = fmt.Sprintf("sudo sh -c %q", c)
	}
	out, err := ep.exec.Run(ctx, c, p.Verbose)
	if err != nil {
		return details, nil, fmt.Errorf("can't run script on %s: %w", ep.hostAddr, err)
	}

	// collect setenv output and set it to the environment. This is needed for the next commands.
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

// execCopyCommand upload a single file or multiple files (if wildcard is used) to a target host.
// if sudo option is set, it will make a temporary directory and upload the files there,
// then move it to the final destination with sudo script execution.
func (p *Process) execCopyCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	src := p.applyTemplates(ep.cmd.Copy.Source,
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})
	dst := p.applyTemplates(ep.cmd.Copy.Dest,
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})

	if !ep.cmd.Options.Sudo {
		// if sudo is not set, we can use the original destination and upload the file directly
		details = fmt.Sprintf(" {copy: %s -> %s}", src, dst)
		if err := ep.exec.Upload(ctx, src, dst, ep.cmd.Copy.Mkdir); err != nil {
			return details, nil, fmt.Errorf("can't copy file to %s: %w", ep.hostAddr, err)
		}
		return details, nil, nil
	}

	if ep.cmd.Options.Sudo {
		// if sudo is set, we need to upload the file to a temporary directory and move it to the final destination
		details = fmt.Sprintf(" {copy: %s -> %s, sudo: true}", src, dst)
		tmpDest := filepath.Join(tmpRemoteDir, filepath.Base(dst))
		if err := ep.exec.Upload(ctx, src, tmpDest, true); err != nil { // upload to a temporary directory with mkdir
			return details, nil, fmt.Errorf("can't copy file to %s: %w", ep.hostAddr, err)
		}

		mvCmd := fmt.Sprintf("mv -f %s %s", tmpDest, dst) // move a single file
		if strings.Contains(src, "*") && !strings.HasSuffix(tmpDest, "/") {
			mvCmd = fmt.Sprintf("mv -f %s/* %s", tmpDest, dst) // move multiple files, if wildcard is used
			defer func() {
				// remove temporary directory we created under /tmp/.spot for multiple files
				if _, err := ep.exec.Run(ctx, fmt.Sprintf("rm -rf %s", tmpDest), p.Verbose); err != nil {
					log.Printf("[WARN] can't remove temporary directory on %s: %v", ep.hostAddr, err)
				}
			}()
		}
		c, _, err := p.prepScript(ctx, mvCmd, nil, ep)
		if err != nil {
			return details, nil, fmt.Errorf("can't prepare sudo moving command on %s: %w", ep.hostAddr, err)
		}

		sudoMove := fmt.Sprintf("sudo %s", c)
		if _, err := ep.exec.Run(ctx, sudoMove, p.Verbose); err != nil {
			return details, nil, fmt.Errorf("can't move file to %s: %w", ep.hostAddr, err)
		}
	}

	return details, nil, nil
}

func (p *Process) execMCopyCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	msgs := []string{}
	for _, c := range ep.cmd.MCopy {
		src := p.applyTemplates(c.Source,
			templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})
		dst := p.applyTemplates(c.Dest,
			templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})
		msgs = append(msgs, fmt.Sprintf("%s -> %s", src, dst))
		epSingle := ep
		epSingle.cmd.Copy = config.CopyInternal{Source: src, Dest: dst, Mkdir: c.Mkdir}
		if _, _, err := p.execCopyCommand(ctx, epSingle); err != nil {
			return details, nil, fmt.Errorf("can't copy file to %s: %w", ep.hostAddr, err)
		}
	}
	details = fmt.Sprintf(" {copy: %s}", strings.Join(msgs, ", "))
	return details, nil, nil
}

func (p *Process) execSyncCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	src := p.applyTemplates(ep.cmd.Sync.Source,
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})
	dst := p.applyTemplates(ep.cmd.Sync.Dest,
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})
	details = fmt.Sprintf(" {sync: %s -> %s}", src, dst)
	if _, err := ep.exec.Sync(ctx, src, dst, ep.cmd.Sync.Delete); err != nil {
		return details, nil, fmt.Errorf("can't sync files on %s: %w", ep.hostAddr, err)
	}
	return details, nil, nil
}

func (p *Process) execDeleteCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	loc := p.applyTemplates(ep.cmd.Delete.Location,
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})

	if !ep.cmd.Options.Sudo {
		// if sudo is not set, we can delete the file directly
		if err := ep.exec.Delete(ctx, loc, ep.cmd.Delete.Recursive); err != nil {
			return details, nil, fmt.Errorf("can't delete files on %s: %w", ep.hostAddr, err)
		}
		details = fmt.Sprintf(" {delete: %s, recursive: %v}", loc, ep.cmd.Delete.Recursive)
	}

	if ep.cmd.Options.Sudo {
		// if sudo is set, we need to delete the file using sudo by ssh-ing into the host and running the command
		cmd := fmt.Sprintf("sudo rm -f %s", loc)
		if ep.cmd.Delete.Recursive {
			cmd = fmt.Sprintf("sudo rm -rf %s", loc)
		}
		if _, err := ep.exec.Run(ctx, cmd, p.Verbose); err != nil {
			return details, nil, fmt.Errorf("can't delete file(s) on %s: %w", ep.hostAddr, err)
		}
		details = fmt.Sprintf(" {delete: %s, recursive: %v, sudo: true}", loc, ep.cmd.Delete.Recursive)
	}

	return details, nil, nil
}

// execWaitCommand waits for a command to complete on a target hostAddr. It runs the command in a loop with a check duration
// until the command succeeds or the timeout is exceeded.
func (p *Process) execWaitCommand(ctx context.Context, ep execCmdParams) (details string, vars map[string]string, err error) {
	c := p.applyTemplates(ep.cmd.Wait.Command,
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})

	timeout, duration := ep.cmd.Wait.Timeout, ep.cmd.Wait.CheckDuration
	if duration == 0 {
		duration = 5 * time.Second // default check duration if not set
	}
	if timeout == 0 {
		timeout = time.Hour * 24 // default timeout if not set, wait practically forever
	}

	details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v}",
		c, timeout.Truncate(100*time.Millisecond), duration.Truncate(100*time.Millisecond))

	waitCmd := fmt.Sprintf("sh -c %q", c) // run wait command in a shell
	if ep.cmd.Options.Sudo {
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
			if _, err := ep.exec.Run(ctx, waitCmd, false); err == nil {
				return details, nil, nil // command succeeded
			}
		}
	}
}

type tdFn func() error // tdFn is a type for teardown functions, should be called after the command execution

// prepScript prepares a script for execution. Script can be either a single command or a multiline script.
// In case of a single command, it just applies templates to it. In case of a multiline script, it creates
// a temporary file with the script chmod as +x and uploads to remote host to /tmp.
// it also  returns a teardown function to remove the temporary file after the command execution.
func (p *Process) prepScript(ctx context.Context, s string, r io.Reader, ep execCmdParams) (cmd string, td tdFn, err error) {
	if s != "" { // single command, nothing to do just apply templates
		s = p.applyTemplates(s, templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})
		return s, nil, nil
	}

	// multiple commands, create a temporary script

	// read the script from the reader and apply templates
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, r); err != nil {
		return "", nil, fmt.Errorf("can't read script: %w", err)
	}
	rdr := bytes.NewBuffer([]byte(p.applyTemplates(buf.String(),
		templateData{hostAddr: ep.hostAddr, hostName: ep.hostName, task: ep.tsk, command: ep.cmd.Name})))

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
	if err = ep.exec.Upload(ctx, tmp.Name(), dst, true); err != nil {
		return "", nil, fmt.Errorf("can't upload script to %s: %w", ep.hostAddr, err)
	}
	remoteCmd := fmt.Sprintf("sh -c %s", dst)

	teardown := func() error {
		// remove the script from the remote hostAddr, should be invoked by the caller after the command is executed
		if err := ep.exec.Delete(ctx, dst, false); err != nil {
			return fmt.Errorf("can't remove temporary remote script %s (%s): %w", dst, ep.hostAddr, err)
		}
		return nil
	}

	return remoteCmd, teardown, nil
}

type templateData struct {
	hostAddr string
	hostName string
	command  string
	task     *config.Task
	err      error
}

func (p *Process) applyTemplates(inp string, tdata templateData) string {
	apply := func(inp, from, to string) string {
		// replace either {SPOT_REMOTE_HOST} ${SPOT_REMOTE_HOST} or $SPOT_REMOTE_HOST format
		res := strings.ReplaceAll(inp, fmt.Sprintf("${%s}", from), to)
		res = strings.ReplaceAll(res, fmt.Sprintf("$%s", from), to)
		res = strings.ReplaceAll(res, fmt.Sprintf("{%s}", from), to)
		return res
	}

	res := inp
	res = apply(res, "SPOT_REMOTE_HOST", tdata.hostAddr)
	res = apply(res, "SPOT_REMOTE_NAME", tdata.hostName)
	res = apply(res, "SPOT_COMMAND", tdata.command)
	res = apply(res, "SPOT_REMOTE_USER", tdata.task.User)
	res = apply(res, "SPOT_TASK", tdata.task.Name)
	if tdata.err != nil {
		res = apply(res, "SPOT_ERROR", tdata.err.Error())
	} else {
		res = apply(res, "SPOT_ERROR", "")
	}

	return res
}
