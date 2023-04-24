package runner

import (
	"context"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/go-pkgz/syncs"

	"github.com/umputun/simplotask/app/config"
	"github.com/umputun/simplotask/app/remote"
)

// Process is a struct that holds the information needed to run a process.
type Process struct {
	Concurrency int
	Connector   *remote.Connector
	Config      *config.PlayBook

	Skip []string
	Only []string
}

// ProcStats holds the information about processed commands and hosts.
type ProcStats struct {
	Commands int
	Hosts    int
}

// Run runs a task for a target hosts. Runs in parallel with limited concurrency, each host is processed in separate goroutine.
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
	return ProcStats{Hosts: len(targetHosts), Commands: int(atomic.LoadInt32(&commands))}, err
}

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

		log.Printf("[INFO] run command %q on host %s", cmd.Name, host)
		st := time.Now()
		details := ""
		switch {
		case cmd.Script != "":
			log.Printf("[DEBUG] run script on %s", host)
			c := cmd.GetScript()
			if _, err := sess.Run(ctx, c); err != nil {
				return 0, fmt.Errorf("can't run script on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {script: %s}", cmd.GetScript())
		case cmd.Copy.Source != "" && cmd.Copy.Dest != "":
			log.Printf("[DEBUG] copy file on %s", host)
			if err := sess.Upload(ctx, cmd.Copy.Source, cmd.Copy.Dest, cmd.Copy.Mkdir); err != nil {
				return 0, fmt.Errorf("can't copy file on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {copy: %s -> %s}", cmd.Copy.Source, cmd.Copy.Dest)
		case cmd.Sync.Source != "" && cmd.Sync.Dest != "":
			log.Printf("[DEBUG] sync files on %s", host)
			if _, err := sess.Sync(ctx, cmd.Sync.Source, cmd.Sync.Dest, cmd.Sync.Delete); err != nil {
				return 0, fmt.Errorf("can't sync files on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {sync: %s -> %s}", cmd.Sync.Source, cmd.Sync.Dest)
		case cmd.Delete.Location != "":
			log.Printf("[DEBUG] delete files on %s", host)
			if err := sess.Delete(ctx, cmd.Delete.Location, cmd.Delete.Recursive); err != nil {
				return 0, fmt.Errorf("can't delete files on %s: %w", host, err)
			}
			details = fmt.Sprintf(" {delete: %s, recursive: %v}", cmd.Delete.Location, cmd.Delete.Recursive)
		}
		outLine := p.colorize(host)("[%s] %s%s (%v)\n", host, cmd.Name, details, time.Since(st).Truncate(time.Millisecond))
		_, _ = os.Stdout.WriteString(outLine)
		count++
	}

	return count, nil
}

func (p *Process) colorize(host string) func(format string, a ...interface{}) string {
	colors := []color.Attribute{color.FgHiRed, color.FgHiGreen, color.FgHiYellow,
		color.FgHiBlue, color.FgHiMagenta, color.FgHiCyan, color.FgCyan, color.FgMagenta,
		color.FgBlue, color.FgYellow, color.FgGreen, color.FgRed}
	i := crc32.ChecksumIEEE([]byte(host)) % uint32(len(colors))
	return color.New(colors[i]).SprintfFunc()
}
