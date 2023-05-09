package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
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
		ec := execCmd{cmd: cmd, hostAddr: hostAddr, hostName: hostName, tsk: tsk, exec: remote, verbose: p.Verbose}
		if cmd.Options.Local {
			ec.exec = &executor.Local{}
			ec.exec.SetSecrets(p.secrets)
			ec.hostAddr = "localhost"
			ec.hostName, _ = os.Hostname() // nolint we don't care about error here
		}

		if p.Dry {
			ec.exec = executor.NewDry(hostAddr, hostName)
			ec.exec.SetSecrets(p.secrets)
			if cmd.Options.Local {
				ec.hostAddr = "localhost"
				ec.hostName, _ = os.Hostname() // nolint we don't care about error here
			}
		}

		details, vars, err := p.execCommand(ctx, ec)
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

// execCommand executes a single command on a target host. It detects command type based on the fields what are set.
// Even if multiple fields for multiple commands are set, only one will be executed.
func (p *Process) execCommand(ctx context.Context, ec execCmd) (details string, vars map[string]string, err error) {
	switch {
	case ec.cmd.Script != "":
		log.Printf("[DEBUG] execute script %q on %s", ec.cmd.Name, ec.hostAddr)
		return ec.script(ctx)
	case ec.cmd.Copy.Source != "" && ec.cmd.Copy.Dest != "":
		log.Printf("[DEBUG] copy file to %s", ec.hostAddr)
		return ec.copy(ctx)
	case len(ec.cmd.MCopy) > 0:
		log.Printf("[DEBUG] copy multiple files to %s", ec.hostAddr)
		return ec.mcopy(ctx)
	case ec.cmd.Sync.Source != "" && ec.cmd.Sync.Dest != "":
		log.Printf("[DEBUG] sync files on %s", ec.hostAddr)
		return ec.sync(ctx)
	case ec.cmd.Delete.Location != "":
		log.Printf("[DEBUG] delete files on %s", ec.hostAddr)
		return ec.delete(ctx)
	case ec.cmd.Wait.Command != "":
		log.Printf("[DEBUG] wait for command on %s", ec.hostAddr)
		return ec.wait(ctx)
	default:
		return "", nil, fmt.Errorf("unknown command %q", ec.cmd.Name)
	}
}
