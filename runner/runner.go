package runner

import (
	"context"
	"fmt"
	"log"

	"github.com/go-pkgz/syncs"

	"github.com/umputun/simplotask/config"
	"github.com/umputun/simplotask/remote"
)

// Process is a struct that holds the information needed to run a process.
type Process struct {
	Concurrency int
	Connector   *remote.Connector
	Config      *config.PlayBook
}

// Run runs a task for a target hosts. Runs in parallel with limited concurrency, each host is processed in separate goroutine.
func (p *Process) Run(ctx context.Context, task, target string) (err error) {
	tsk, err := p.Config.Task(task)
	if err != nil {
		return fmt.Errorf("can't get task %s: %w", task, err)
	}
	log.Printf("[DEBUG] task %s has %d commands", task, len(tsk.Commands))

	targetHosts, err := p.Config.TargetHosts(target)
	if err != nil {
		return fmt.Errorf("can't get target %s: %w", target, err)
	}
	log.Printf("[DEBUG] target hosts %v", targetHosts)

	wg := syncs.NewErrSizedGroup(p.Concurrency, syncs.Context(ctx), syncs.Preemptive)
	for _, host := range targetHosts {
		host := host
		wg.Go(func() error {
			return p.runTaskOnHost(ctx, tsk, host)
		})
	}
	return wg.Wait()
}

func (p *Process) runTaskOnHost(ctx context.Context, tsk *config.Task, host string) error {
	sess, err := p.Connector.Connect(ctx, host)
	if err != nil {
		return fmt.Errorf("can't connect to %s: %w", host, err)
	}
	defer sess.Close()

	for _, cmd := range tsk.Commands {
		log.Printf("[INFO] run command %q on host %s", cmd.Name, host)
		switch {
		case cmd.Script != "":
			log.Printf("[DEBUG] run script on %s", host)
			c := cmd.GetScript()
			if _, err := sess.Run(ctx, c); err != nil {
				return fmt.Errorf("can't run script on %s: %w", host, err)
			}
		case cmd.Copy.Source != "" && cmd.Copy.Dest != "":
			log.Printf("[DEBUG] copy file on %s", host)
			if err := sess.Upload(ctx, cmd.Copy.Source, cmd.Copy.Dest, cmd.Copy.Mkdir); err != nil {
				return fmt.Errorf("can't copy file on %s: %w", host, err)
			}
		case cmd.Sync.Source != "" && cmd.Sync.Dest != "":
			log.Printf("[DEBUG] sync files on %s", host)
			if _, err := sess.Sync(ctx, cmd.Sync.Source, cmd.Sync.Dest, cmd.Sync.Delete); err != nil {
				return fmt.Errorf("can't sync files on %s: %w", host, err)
			}
		case cmd.Delete.Location != "":
			log.Printf("[DEBUG] delete files on %s", host)
			if err := sess.Delete(ctx, cmd.Delete.Location, cmd.Delete.Recursive); err != nil {
				return fmt.Errorf("can't delete files on %s: %w", host, err)
			}
		}
	}
	return nil
}
