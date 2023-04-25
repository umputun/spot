package runner

import (
	"context"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/go-pkgz/syncs"

	"github.com/umputun/simplotask/app/config"
	"github.com/umputun/simplotask/app/remote"
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

// Connector is an interface for connecting to a host, and returning an Executer.
type Connector interface {
	Connect(ctx context.Context, host string) (*remote.Executer, error)
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
		onErrCmd.Stdout = os.Stdout
		onErrCmd.Stderr = os.Stderr
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

	sess, err := p.Connector.Connect(ctx, host)
	if err != nil {
		return 0, fmt.Errorf("can't connect to %s: %w", host, err)
	}
	defer sess.Close()

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
		details := ""
		switch {
		case cmd.Script != "":
			log.Printf("[DEBUG] run script on %s", host)
			c := p.applyTemplates(cmd.GetScript(), templateData{host: host, task: tsk, command: cmd.Name})
			if _, err := sess.Run(ctx, c); err != nil {
				return 0, fmt.Errorf("can't run script on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {script: %s}", c)
		case cmd.Copy.Source != "" && cmd.Copy.Dest != "":
			log.Printf("[DEBUG] copy file on %s", host)
			src := p.applyTemplates(cmd.Copy.Source, templateData{host: host, task: tsk, command: cmd.Name})
			dst := p.applyTemplates(cmd.Copy.Dest, templateData{host: host, task: tsk, command: cmd.Name})
			if err := sess.Upload(ctx, src, dst, cmd.Copy.Mkdir); err != nil {
				return 0, fmt.Errorf("can't copy file on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {copy: %s -> %s}", src, dst)
		case cmd.Sync.Source != "" && cmd.Sync.Dest != "":
			log.Printf("[DEBUG] sync files on %s", host)
			src := p.applyTemplates(cmd.Sync.Source, templateData{host: host, task: tsk, command: cmd.Name})
			dst := p.applyTemplates(cmd.Sync.Dest, templateData{host: host, task: tsk, command: cmd.Name})
			if _, err := sess.Sync(ctx, src, dst, cmd.Sync.Delete); err != nil {
				return 0, fmt.Errorf("can't sync files on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {sync: %s -> %s}", src, dst)
		case cmd.Delete.Location != "":
			log.Printf("[DEBUG] delete files on %s", host)
			loc := p.applyTemplates(cmd.Delete.Location, templateData{host: host, task: tsk, command: cmd.Name})
			if err := sess.Delete(ctx, loc, cmd.Delete.Recursive); err != nil {
				return 0, fmt.Errorf("can't delete files on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {delete: %s, recursive: %v}", loc, cmd.Delete.Recursive)
		case cmd.Wait.Command != "":
			log.Printf("[DEBUG] wait for command on %s", host)
			c := p.applyTemplates(cmd.Wait.Command, templateData{host: host, task: tsk, command: cmd.Name})
			params := config.WaitInternal{Command: c, Timeout: cmd.Wait.Timeout, CheckDuration: cmd.Wait.CheckDuration}
			if err := p.wait(ctx, sess, params); err != nil {
				return 0, fmt.Errorf("wait failed on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {wait: %s, timeout: %v, duration: %v}",
				c, cmd.Wait.Timeout.Truncate(100*time.Millisecond), cmd.Wait.CheckDuration.Truncate(100*time.Millisecond))
		}

		outLine := p.colorize(host)("[%s] %s%s (%v)\n", host, cmd.Name, details, time.Since(st).Truncate(time.Millisecond))
		_, _ = os.Stdout.WriteString(outLine)
		count++
	}

	return count, nil
}

// wait waits for a command to complete on a target host. It runs the command in a loop with a check duration
// until the command succeeds or the timeout is exceeded.
func (p *Process) wait(ctx context.Context, sess *remote.Executer, params config.WaitInternal) error {
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
