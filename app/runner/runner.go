package runner

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/go-pkgz/syncs"

	"github.com/umputun/simplotask/app/config"
	"github.com/umputun/simplotask/app/executor"
)

//go:generate moq -out mocks/connector.go -pkg mocks -skip-ensure -fmt goimports . Connector

// Process is a struct that holds the information needed to run a process.
// It responsible for running a task on a target hosts.
type Process struct {
	Concurrency int
	Connector   Connector
	Config      *config.PlayBook

	Skip []string
	Only []string
}

// Connector is an interface for connecting to a host, and returning remote executer.
type Connector interface {
	Connect(ctx context.Context, host string) (*executor.Remote, error)
	User() string
}

// ProcStats holds the information about processed commands and hosts.
type ProcStats struct {
	Commands int
	Hosts    int
}

// Run runs a task for a set of target hosts. Runs in parallel with limited concurrency, each host is processed in separate goroutine.
func (p *Process) Run(ctx context.Context, task, target string) (s ProcStats, err error) {
	tsk, err := p.Config.Task(task)
	if err != nil {
		return ProcStats{}, fmt.Errorf("can't get task %s: %w", task, err)
	}
	log.Printf("[DEBUG] task %s has %d commands", task, len(tsk.Commands))

	targetHosts, err := p.Config.TargetHosts(target)
	if err != nil {
		return ProcStats{}, fmt.Errorf("can't get target %s: %w", target, err)
	}
	log.Printf("[DEBUG] target hosts %v", targetHosts)

	wg := syncs.NewErrSizedGroup(p.Concurrency, syncs.Context(ctx), syncs.Preemptive)
	var commands int32
	for i, host := range targetHosts {
		i, host := i, host
		wg.Go(func() error {
			count, e := p.runTaskOnHost(ctx, tsk, host)
			if i == 0 {
				atomic.AddInt32(&commands, int32(count))
			}
			return e
		})
	}
	err = wg.Wait()

	// execute on-error command if any error occurred during task execution and on-error command is defined
	if err != nil && tsk.OnError != "" {
		onErrCmd := exec.CommandContext(ctx, "sh", "-c", tsk.OnError) //nolint we want to run shell here
		onErrCmd.Env = os.Environ()
		var stdoutBuf bytes.Buffer
		mwr := io.MultiWriter(executor.NewStdOutLogWriter(">", "DEBUG"), &stdoutBuf)
		onErrCmd.Stdout, onErrCmd.Stderr = mwr, executor.NewStdOutLogWriter("!", "WARN")
		if exErr := onErrCmd.Run(); exErr != nil {
			log.Printf("[WARN] can't run on-error command %q: %v", tsk.OnError, exErr)
		}
	}

	return ProcStats{Hosts: len(targetHosts), Commands: int(atomic.LoadInt32(&commands))}, err
}

// runTaskOnHost executes all commands of a task on a target host.
func (p *Process) runTaskOnHost(ctx context.Context, tsk *config.Task, host string) (int, error) {
	contains := func(list []string, s string) bool {
		for _, v := range list {
			if strings.EqualFold(v, s) {
				return true
			}
		}
		return false
	}

	remote, err := p.Connector.Connect(ctx, host)
	if err != nil {
		return 0, fmt.Errorf("can't connect to %s: %w", host, err)
	}
	defer remote.Close()

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

		log.Printf("[INFO] run command %q on host %s", cmd.Name, host)
		st := time.Now()
		params := execCmdParams{cmd: cmd, host: host, tsk: tsk, exec: remote}
		if cmd.Options.Local {
			params.exec = &executor.Local{}
		}
		details, err := p.execCommand(ctx, params)
		if err != nil {
			if !cmd.Options.IgnoreErrors {
				return count, fmt.Errorf("can't run command %q on host %s: %w", cmd.Name, host, err)
			}
			outLine := p.colorize(host)("[%s] failed %s%s (%v)\n",
				host, cmd.Name, details, time.Since(st).Truncate(time.Millisecond))
			_, _ = os.Stdout.WriteString(outLine)
			continue
		}

		outLine := p.colorize(host)("[%s] %s%s (%v)\n", host, cmd.Name, details, time.Since(st).Truncate(time.Millisecond))
		_, _ = os.Stdout.WriteString(outLine)
		count++
	}

	return count, nil
}

type execCmdParams struct {
	cmd  config.Cmd
	host string
	tsk  *config.Task
	exec executor.Interface
}

func (p *Process) execCommand(ctx context.Context, ep execCmdParams) (details string, err error) {
	switch {
	case ep.cmd.Script != "":
		log.Printf("[DEBUG] run script on %s", ep.host)
		single, multiRdr := ep.cmd.GetScript()
		c, teardown, err := p.prepScript(ctx, single, multiRdr, ep)
		if err != nil {
			return details, fmt.Errorf("can't prepare script on %s: %w", ep.host, err)
		}
		defer func() {
			if teardown == nil {
				return
			}
			if err := teardown(); err != nil {
				log.Printf("[WARN] can't teardown script on %s: %v", ep.host, err)
			}
		}()
		details = fmt.Sprintf(" {script: %s}", c)
		if _, err := ep.exec.Run(ctx, c); err != nil {
			return details, fmt.Errorf("can't run script on %s: %w", ep.host, err)
		}
	case ep.cmd.Copy.Source != "" && ep.cmd.Copy.Dest != "":
		log.Printf("[DEBUG] copy file on %s", ep.host)
		src := p.applyTemplates(ep.cmd.Copy.Source, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		dst := p.applyTemplates(ep.cmd.Copy.Dest, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		details = fmt.Sprintf(" {copy: %s -> %s}", src, dst)
		if err := ep.exec.Upload(ctx, src, dst, ep.cmd.Copy.Mkdir); err != nil {
			return details, fmt.Errorf("can't copy file on %s: %w", ep.host, err)
		}
	case ep.cmd.Sync.Source != "" && ep.cmd.Sync.Dest != "":
		log.Printf("[DEBUG] sync files on %s", ep.host)
		src := p.applyTemplates(ep.cmd.Sync.Source, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		dst := p.applyTemplates(ep.cmd.Sync.Dest, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		details = fmt.Sprintf(" {sync: %s -> %s}", src, dst)
		if _, err := ep.exec.Sync(ctx, src, dst, ep.cmd.Sync.Delete); err != nil {
			return details, fmt.Errorf("can't sync files on %s: %w", ep.host, err)
		}
	case ep.cmd.Delete.Location != "":
		log.Printf("[DEBUG] delete files on %s", ep.host)
		loc := p.applyTemplates(ep.cmd.Delete.Location, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		details = fmt.Sprintf(" {delete: %s, recursive: %v}", loc, ep.cmd.Delete.Recursive)
		if err := ep.exec.Delete(ctx, loc, ep.cmd.Delete.Recursive); err != nil {
			return details, fmt.Errorf("can't delete files on %s: %w", ep.host, err)
		}
	case ep.cmd.Wait.Command != "":
		log.Printf("[DEBUG] wait for command on %s", ep.host)
		c := p.applyTemplates(ep.cmd.Wait.Command, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		params := config.WaitInternal{Command: c, Timeout: ep.cmd.Wait.Timeout, CheckDuration: ep.cmd.Wait.CheckDuration}
		details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v}",
			c, ep.cmd.Wait.Timeout.Truncate(100*time.Millisecond), ep.cmd.Wait.CheckDuration.Truncate(100*time.Millisecond))
		if err := p.wait(ctx, ep.exec, params); err != nil {
			return details, fmt.Errorf("wait failed on %s: %w", ep.host, err)
		}
	default:
		return "", fmt.Errorf("unknown command %q", ep.cmd.Name)
	}
	return details, nil
}

// wait waits for a command to complete on a target host. It runs the command in a loop with a check duration
// until the command succeeds or the timeout is exceeded.
func (p *Process) wait(ctx context.Context, sess executor.Interface, params config.WaitInternal) error {
	if params.Timeout == 0 {
		return nil
	}
	duration := params.CheckDuration
	if params.CheckDuration == 0 {
		duration = 5 * time.Second // default check duration if not set
	}
	checkTk := time.NewTicker(duration)
	defer checkTk.Stop()
	timeoutTk := time.NewTicker(params.Timeout)
	defer timeoutTk.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutTk.C:
			return fmt.Errorf("timeout exceeded")
		case <-checkTk.C:
			if _, err := sess.Run(ctx, params.Command); err == nil {
				return nil
			}
		}
	}
}

// colorize returns a function that formats a string with a color based on the host name.
func (p *Process) colorize(host string) func(format string, a ...interface{}) string {
	colors := []color.Attribute{color.FgHiRed, color.FgHiGreen, color.FgHiYellow,
		color.FgHiBlue, color.FgHiMagenta, color.FgHiCyan, color.FgCyan, color.FgMagenta,
		color.FgBlue, color.FgYellow, color.FgGreen, color.FgRed}
	i := crc32.ChecksumIEEE([]byte(host)) % uint32(len(colors))
	return color.New(colors[i]).SprintfFunc()
}

type templateData struct {
	host    string
	command string
	task    *config.Task
	err     error
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
	res = apply(res, "SPOT_REMOTE_HOST", tdata.host)
	res = apply(res, "SPOT_COMMAND", tdata.command)
	res = apply(res, "SPOT_REMOTE_USER", p.Connector.User())
	res = apply(res, "SPOT_TASK", tdata.task.Name)
	if tdata.err != nil {
		res = apply(res, "SPOT_ERROR", tdata.err.Error())
	} else {
		res = apply(res, "SPOT_ERROR", "")
	}

	return res
}

type tdFn func() error // tdFn is a type for teardown functions, should be called after the command execution

func (p *Process) prepScript(ctx context.Context, s string, r io.Reader, ep execCmdParams) (cmd string, td tdFn, err error) {
	if s != "" { // single command, nothing to do just apply templates
		s = p.applyTemplates(s, templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})
		return s, nil, nil
	}

	// multiple commands, create a temporary script

	// read the script from the reader and apply templates
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, r); err != nil {
		return "", nil, fmt.Errorf("can't read script: %w", err)
	}
	rdr := bytes.NewBuffer([]byte(p.applyTemplates(buf.String(), templateData{host: ep.host, task: ep.tsk, command: ep.cmd.Name})))

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
	if err = os.Chmod(tmp.Name(), 0o700); err != nil { //nolint
		return "", nil, fmt.Errorf("can't chmod temporary file: %w", err)
	}

	// get temp file name for remote host
	dst := filepath.Join("/tmp", filepath.Base(tmp.Name())) //nolint

	// upload the script to the remote host
	if err = ep.exec.Upload(ctx, tmp.Name(), dst, false); err != nil {
		return "", nil, fmt.Errorf("can't upload script to %s: %w", ep.host, err)
	}
	remoteCmd := fmt.Sprintf("sh -c %s", dst)

	teardown := func() error {
		// remove the script from the remote host, should be invoked by the caller after the command is executed
		if err := ep.exec.Delete(ctx, dst, false); err != nil {
			return fmt.Errorf("can't remove temporary remote script %s (%s): %w", dst, ep.host, err)
		}
		return nil
	}

	return remoteCmd, teardown, nil
}
