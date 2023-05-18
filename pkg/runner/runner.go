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
	"sync"
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

// Connector is an interface for connecting to a host, and returning remote executer.
type Connector interface {
	Connect(ctx context.Context, hostAddr, hostName, user string) (*executor.Remote, error)
}

// ProcResp holds the information about processed commands and hosts.
type ProcResp struct {
	Vars     map[string]string
	Commands int
	Hosts    int
}

type vars map[string]string

// Run runs a task for a set of target hosts. Runs in parallel with limited concurrency,
// each host is processed in separate goroutine. Returns ProcResp with the information about processed commands and hosts
// plus vars from all thr commands.
func (p *Process) Run(ctx context.Context, task, target string) (s ProcResp, err error) {
	tsk, err := p.Config.Task(task)
	if err != nil {
		return ProcResp{}, fmt.Errorf("can't get task %s: %w", task, err)
	}
	log.Printf("[DEBUG] task %q has %d commands", task, len(tsk.Commands))

	allVars := make(map[string]string)
	targetHosts, err := p.Config.TargetHosts(target)
	if err != nil {
		return ProcResp{}, fmt.Errorf("can't get target %s: %w", target, err)
	}
	log.Printf("[DEBUG] target hosts (%d) %+v", len(targetHosts), targetHosts)

	p.secrets = p.Config.AllSecretValues()
	var commands int32
	lock := sync.Mutex{}

	wg := syncs.NewErrSizedGroup(p.Concurrency, syncs.Context(ctx), syncs.Preemptive)
	for i, host := range targetHosts {
		i, host := i, host
		wg.Go(func() error {
			count, vv, e := p.runTaskOnHost(ctx, tsk, fmt.Sprintf("%s:%d", host.Host, host.Port), host.Name, host.User)
			if i == 0 {
				atomic.AddInt32(&commands, int32(count))
			}

			lock.Lock()
			if e != nil {
				_, errLog := executor.MakeOutAndErrWriters(fmt.Sprintf("%s:%d", host.Host, host.Port), host.Name, p.Verbose, p.secrets)
				errLog.Write([]byte(e.Error())) // nolint
			}
			for k, v := range vv {
				allVars[k] = v
			}
			lock.Unlock()

			return e
		})
	}
	err = wg.Wait()

	// execute on-error command if any error occurred during task execution and on-error command is defined
	if err != nil && tsk.OnError != "" {
		p.onError(ctx, tsk)
	}

	return ProcResp{Hosts: len(targetHosts), Commands: int(atomic.LoadInt32(&commands)), Vars: allVars}, err
}

// runTaskOnHost executes all commands of a task on a target host. hostAddr can be a remote host or localhost with port.
// returns number of executed commands, vars from all commands and error if any.
func (p *Process) runTaskOnHost(ctx context.Context, tsk *config.Task, hostAddr, hostName, user string) (int, vars, error) {
	report := func(hostAddr, hostName, f string, vals ...any) {
		fmt.Fprintf(p.ColorWriter.WithHost(hostAddr, hostName), f, vals...)
	}
	since := func(st time.Time) time.Duration { return time.Since(st).Truncate(time.Millisecond) }

	stTask := time.Now()

	var remote executor.Interface
	if p.anyRemoteCommand(tsk) {
		// make remote executor only if there is a remote command in the taks
		var err error
		remote, err = p.Connector.Connect(ctx, hostAddr, hostName, user)
		if err != nil {
			if hostName != "" {
				return 0, nil, fmt.Errorf("can't connect to %s: %w", hostName, err)
			}
			return 0, nil, err
		}
		defer remote.Close()
		remote.SetSecrets(p.secrets)
		report(hostAddr, hostName, "run task %q, commands: %d\n", tsk.Name, len(tsk.Commands))
	} else {
		report("localhost", "", "run task %q, commands: %d (local)\n", tsk.Name, len(tsk.Commands))
	}

	count := 0
	tskVars := vars{}
	for _, cmd := range tsk.Commands {
		if !p.shouldRunCmd(cmd, hostName, hostAddr) {
			continue
		}

		log.Printf("[INFO] %s", p.infoMessage(cmd, hostAddr, hostName))
		stCmd := time.Now()
		ec := execCmd{cmd: cmd, hostAddr: hostAddr, hostName: hostName, tsk: tsk, exec: remote, verbose: p.Verbose}
		ec = p.pickCmdExecutor(cmd, ec, hostAddr, hostName) // pick executor on dry run or local command

		details, vars, err := p.execCommand(ctx, ec)
		if err != nil {
			if !cmd.Options.IgnoreErrors {
				return count, nil, fmt.Errorf("failed command %q on host %s (%s): %w", cmd.Name, ec.hostAddr, ec.hostName, err)
			}
			report(ec.hostAddr, ec.hostName, "failed command %q%s (%v)", cmd.Name, details, since(stCmd))
			continue
		}

		p.updateVars(vars, cmd, tsk) // set variables from command output to all commands env in task
		report(ec.hostAddr, ec.hostName, "completed command %q%s (%v)", cmd.Name, details, since(stCmd))
		count++
		for k, v := range vars {
			tskVars[k] = v
		}
	}

	if p.anyRemoteCommand(tsk) {
		report(hostAddr, hostName, "completed task %q, commands: %d (%v)\n", tsk.Name, count, since(stTask))
	} else {
		report("localhost", "", "completed task %q, commands: %d (%v)\n", tsk.Name, count, since(stTask))
	}
	return count, tskVars, nil
}

// execCommand executes a single command on a target host.
// It detects the command type based on the fields what are set.
// Even if multiple fields for multiple commands are set, only one will be executed.
func (p *Process) execCommand(ctx context.Context, ec execCmd) (details string, vars map[string]string, err error) {
	switch {
	case ec.cmd.Script != "":
		log.Printf("[DEBUG] execute script %q on %s", ec.cmd.Name, ec.hostAddr)
		return ec.Script(ctx)
	case ec.cmd.Copy.Source != "" && ec.cmd.Copy.Dest != "":
		log.Printf("[DEBUG] copy file to %s", ec.hostAddr)
		return ec.Copy(ctx)
	case len(ec.cmd.MCopy) > 0:
		log.Printf("[DEBUG] copy multiple files to %s", ec.hostAddr)
		return ec.Mcopy(ctx)
	case ec.cmd.Sync.Source != "" && ec.cmd.Sync.Dest != "":
		log.Printf("[DEBUG] sync files to %s", ec.hostAddr)
		return ec.Sync(ctx)
	case len(ec.cmd.MSync) > 0:
		log.Printf("[DEBUG] sync multiple locations to %s", ec.hostAddr)
		return ec.Msync(ctx)
	case ec.cmd.Delete.Location != "":
		log.Printf("[DEBUG] delete files on %s", ec.hostAddr)
		return ec.Delete(ctx)
	case ec.cmd.Wait.Command != "":
		log.Printf("[DEBUG] wait for command on %s", ec.hostAddr)
		return ec.Wait(ctx)
	case ec.cmd.Echo != "":
		log.Printf("[DEBUG] echo on %s", ec.hostAddr)
		return ec.Echo(ctx)
	default:
		return "", nil, fmt.Errorf("unknown command %q", ec.cmd.Name)
	}
}

// pickCmdExecutor returns executor for dry run or local command, otherwise returns the default executor.
func (p *Process) pickCmdExecutor(cmd config.Cmd, ec execCmd, hostAddr, hostName string) execCmd {
	switch {
	case cmd.Options.Local:
		log.Printf("[DEBUG] run local command %q", cmd.Name)
		ec.exec = &executor.Local{}
		ec.exec.SetSecrets(p.secrets)
		ec.hostAddr = "localhost"
		ec.hostName = ""
		return ec
	case p.Dry:
		log.Printf("[DEBUG] run dry command %q", cmd.Name)
		ec.exec = executor.NewDry(hostAddr, hostName)
		ec.exec.SetSecrets(p.secrets)
		if cmd.Options.Local {
			ec.hostAddr = "localhost"
			ec.hostName = ""
		}
		return ec
	}
	return ec
}

// onError executes on-error command if any error occurred during task execution and on-error command is defined
func (p *Process) onError(ctx context.Context, tsk *config.Task) {
	onErrCmd := exec.CommandContext(ctx, "sh", "-c", tsk.OnError) // nolint we want to run shell here
	onErrCmd.Env = os.Environ()

	outLog, errLog := executor.MakeOutAndErrWriters("localhost", "", p.Verbose, p.secrets)
	outLog.Write([]byte(tsk.OnError)) // nolint

	var stdoutBuf bytes.Buffer
	mwr := io.MultiWriter(outLog, &stdoutBuf)
	onErrCmd.Stdout, onErrCmd.Stderr = mwr, errLog
	onErrCmd.Stdout, onErrCmd.Stderr = mwr, executor.NewStdoutLogWriter("!", "WARN", p.secrets)
	if exErr := onErrCmd.Run(); exErr != nil {
		log.Printf("[WARN] can't run on-error command %q: %v", tsk.OnError, exErr)
	}
}

func (p *Process) anyRemoteCommand(tsk *config.Task) bool {
	for _, cmd := range tsk.Commands {
		if !cmd.Options.Local {
			return true
		}
	}
	return false
}

func (p *Process) infoMessage(cmd config.Cmd, hostAddr, hostName string) string {
	infoMsg := fmt.Sprintf("run command %q on host %q (%s)", cmd.Name, hostAddr, hostName)
	if hostName == "" {
		infoMsg = fmt.Sprintf("run command %q on host %q", cmd.Name, hostAddr)
	}
	if cmd.Options.Local {
		infoMsg = fmt.Sprintf("run command %q on local host", cmd.Name)
	}
	return infoMsg
}

// updateVars sets variables from command output to all commands environment in the same task.
func (p *Process) updateVars(vars map[string]string, cmd config.Cmd, tsk *config.Task) {
	if len(vars) == 0 {
		return
	}

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

// shouldRunCmd checks if the command should be executed on the host. If the command has no restrictions
// (onlyOn field), it will be executed on all hosts. If the command has restrictions, it will be executed
// only on the hosts that match the restrictions.
// The onlyOn field can contain hostnames or IP addresses. If the hostname starts with "!", it will be
// excluded from the list of hosts. If the hostname doesn't start with "!", it will be included in the list
// of hosts. If the onlyOn field is empty, the command will be executed on all hosts.
// It also checks if the command is in the 'only' or 'skip' list, and considers the 'NoAuto' option.
func (p *Process) shouldRunCmd(cmd config.Cmd, hostName, hostAddr string) bool {
	contains := func(list []string, s string) bool {
		for _, v := range list {
			if strings.EqualFold(v, s) {
				return true
			}
		}
		return false
	}

	if len(p.Only) > 0 && !contains(p.Only, cmd.Name) {
		log.Printf("[DEBUG] skip command %q, not in only list", cmd.Name)
		return false
	}
	if len(p.Skip) > 0 && contains(p.Skip, cmd.Name) {
		log.Printf("[DEBUG] skip command %q, in skip list", cmd.Name)
		return false
	}
	if cmd.Options.NoAuto && (len(p.Only) == 0 || !contains(p.Only, cmd.Name)) {
		log.Printf("[DEBUG] skip command %q, has noauto option", cmd.Name)
		return false
	}

	if len(cmd.Options.OnlyOn) == 0 {
		return true
	}

	for _, host := range cmd.Options.OnlyOn {
		if strings.HasPrefix(host, "!") { // exclude host
			if hostName == host[1:] || hostAddr == host[1:] {
				log.Printf("[DEBUG] skip command %q, excluded host %q", cmd.Name, host[1:])
				return false
			}
			continue
		}
		if hostName == host || hostAddr == host { // include host
			return true
		}
	}

	log.Printf("[DEBUG] skip command %q, not in only_on list", cmd.Name)
	return false
}
