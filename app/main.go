package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/go-pkgz/lgr"
	"github.com/hashicorp/go-multierror"
	"github.com/jessevdk/go-flags"

	"github.com/umputun/simplotask/app/config"
	"github.com/umputun/simplotask/app/executor"
	"github.com/umputun/simplotask/app/runner"
)

type options struct {
	PositionalArgs struct {
		AdHocCmd string `positional-arg-name:"command" description:"run ad-hoc command on target hosts"`
	} `positional-args:"yes" positional-optional:"yes"`

	PlaybookFile string        `short:"p" long:"playbook" env:"SPOT_PLAYBOOK" description:"playbook file" default:"spot.yml"`
	TaskName     string        `short:"t" long:"task" description:"task name"`
	Targets      []string      `short:"d" long:"target" description:"target name" default:"default"`
	Concurrent   int           `short:"c" long:"concurrent" description:"concurrent tasks" default:"1"`
	SSHTimeout   time.Duration `long:"timeout" description:"ssh timeout" default:"30s"`

	// overrides
	Inventory string            `short:"i" long:"inventory" description:"inventory file or url"`
	SSHUser   string            `short:"u" long:"user" description:"ssh user"`
	SSHKey    string            `short:"k" long:"key" description:"ssh key"`
	Env       map[string]string `short:"e" long:"env" description:"environment variables for all commands"`

	// commands filter
	Skip []string `long:"skip" description:"skip commands"`
	Only []string `long:"only" description:"run only commands"`

	Dry     bool `long:"dry" description:"dry run"`
	Verbose bool `short:"v" long:"verbose" description:"verbose mode"`
	Dbg     bool `long:"dbg" description:"debug mode"`
	Help    bool `short:"h" long:"help" description:"show help"`
}

var revision = "latest"

func main() {
	fmt.Printf("spot %s\n", revision)

	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash)
	if _, err := p.Parse(); err != nil {
		os.Exit(1)
	}

	if opts.Help {
		p.WriteHelp(os.Stdout)
		os.Exit(2)
	}

	setupLog(opts.Dbg)

	if opts.Dry {
		msg := color.New(color.FgHiRed).SprintfFunc()("dry run - no changes will be made and no commands will be executed\n")
		fmt.Print(msg)
	}

	if err := run(opts); err != nil {
		if opts.Dbg {
			log.Panicf("[ERROR] %v", err)
		}
		fmt.Printf("failed, %v", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	if opts.Dry {
		log.Printf("[WARN] dry run, no changes will be made and no commands will be executed")
	}

	st := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	exInventory, err := expandPath(opts.Inventory)
	if err != nil {
		return fmt.Errorf("can't expand inventory path %q: %w", exInventory, err)
	}

	overrides := config.Overrides{
		Inventory:    exInventory,
		Environment:  opts.Env,
		User:         opts.SSHUser,
		AdHocCommand: opts.PositionalArgs.AdHocCmd,
	}

	exPlaybookFile, err := expandPath(opts.PlaybookFile)
	if err != nil {
		return fmt.Errorf("can't expand playbook path %q: %w", opts.PlaybookFile, err)
	}

	conf, err := config.New(exPlaybookFile, &overrides)
	if err != nil {
		return fmt.Errorf("can't read config %s: %w", exPlaybookFile, err)
	}

	if opts.PositionalArgs.AdHocCmd != "" {
		if err = adHocConf(opts, conf, &defaultUserInfoProvider{}); err != nil {
			return fmt.Errorf("can't setup ad-hoc config: %w", err)
		}
	}

	connector, err := executor.NewConnector(sshKey(opts, conf), opts.SSHTimeout)
	if err != nil {
		return fmt.Errorf("can't create connector: %w", err)
	}
	r := runner.Process{
		Concurrency: opts.Concurrent,
		Connector:   connector,
		Config:      conf,
		Only:        opts.Only,
		Skip:        opts.Skip,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", ""),
		Verbose:     opts.Verbose,
		Dry:         opts.Dry,
	}

	errs := new(multierror.Error)
	if opts.PositionalArgs.AdHocCmd != "" { // run ad-hoc command
		r.Verbose = true // always verbose for ad-hoc
		for _, targetName := range opts.Targets {
			if err := runTaskForTarget(ctx, r, "ad-hoc", targetName); err != nil {
				errs = multierror.Append(errs, err)
			}
		}
		return errs.ErrorOrNil()
	}

	if opts.TaskName != "" { // run single task
		for _, targetName := range opts.Targets {
			if err := runTaskForTarget(ctx, r, opts.TaskName, targetName); err != nil {
				return err
			}
		}
		return nil
	}

	// run all tasks
	for _, taskName := range conf.Tasks {
		for _, targetName := range opts.Targets {
			if err := runTaskForTarget(ctx, r, taskName.Name, targetName); err != nil {
				return err
			}
		}
	}
	log.Printf("[INFO] completed all %d targets in %v", len(opts.Targets), time.Since(st).Truncate(100*time.Millisecond))
	return nil
}

func runTaskForTarget(ctx context.Context, r runner.Process, taskName, targetName string) error {
	st := time.Now()
	stats, err := r.Run(ctx, taskName, targetName)
	if err != nil {
		return fmt.Errorf("can't run task %q for target %q: %w", taskName, targetName, err)
	}
	log.Printf("[INFO] completed: hosts:%d, commands:%d in %v\n",
		stats.Hosts, stats.Commands, time.Since(st).Truncate(100*time.Millisecond))
	return nil
}

func sshKey(opts options, conf *config.PlayBook) (key string) {
	sshKey := opts.SSHKey
	if sshKey == "" && (conf == nil || conf.SSHKey != "") {
		sshKey = conf.SSHKey // default to config key
	}
	if p, err := expandPath(sshKey); err == nil {
		sshKey = p
	}
	return sshKey
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		usr, err := user.Current()
		if err != nil {
			return "", err
		}
		home := usr.HomeDir
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

type userInfoProvider interface {
	Current() (*user.User, error)
}

type defaultUserInfoProvider struct{}

func (p *defaultUserInfoProvider) Current() (*user.User, error) {
	return user.Current()
}

// adHocConf prepares config for ad-hoc command
func adHocConf(opts options, conf *config.PlayBook, provider userInfoProvider) error {
	if opts.SSHUser == "" {
		u, err := provider.Current()
		if err != nil {
			return fmt.Errorf("can't get current user: %w", err)
		}
		conf.User = u.Username
	}
	if opts.SSHKey == "" {
		u, err := provider.Current()
		if err != nil {
			return fmt.Errorf("can't get current user: %w", err)
		}
		conf.SSHKey = filepath.Join(u.HomeDir, ".ssh", "id_rsa")
	}
	return nil
}

func setupLog(dbg bool) {
	logOpts := []lgr.Option{lgr.Out(io.Discard), lgr.Err(io.Discard)} // default to discard
	if dbg {
		logOpts = []lgr.Option{lgr.Debug, lgr.Msec, lgr.LevelBraces, lgr.StackTraceOnError}
	}

	colorizer := lgr.Mapper{
		ErrorFunc:  func(s string) string { return color.New(color.FgHiRed).Sprint(s) },
		WarnFunc:   func(s string) string { return color.New(color.FgRed).Sprint(s) },
		InfoFunc:   func(s string) string { return color.New(color.FgYellow).Sprint(s) },
		DebugFunc:  func(s string) string { return color.New(color.FgWhite).Sprint(s) },
		CallerFunc: func(s string) string { return color.New(color.FgBlue).Sprint(s) },
		TimeFunc:   func(s string) string { return color.New(color.FgCyan).Sprint(s) },
	}
	logOpts = append(logOpts, lgr.Map(colorizer))

	lgr.SetupStdLogger(logOpts...)
	lgr.Setup(logOpts...)
}
