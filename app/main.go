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
	"github.com/jessevdk/go-flags"

	"github.com/umputun/simplotask/app/config"
	"github.com/umputun/simplotask/app/executor"
	"github.com/umputun/simplotask/app/runner"
)

type options struct {
	PlaybookFile string `short:"f" long:"file" env:"SPT_FILE" description:"playbook file" default:"spt.yml"`
	TaskName     string `short:"t" long:"task" description:"task name"`
	TargetName   string `short:"d" long:"target" description:"target name" default:"default"`
	Concurrent   int    `short:"c" long:"concurrent" description:"concurrent tasks" default:"1"`

	// target overrides
	TargetHosts   []string `short:"h" long:"host" description:"destination host"`
	InventoryFile string   `long:"inventory-file" description:"inventory file"`
	InventoryURL  string   `long:"inventory-url" description:"inventory http url"`

	// connection overrides
	SSHUser string `short:"u" long:"user" description:"ssh user"`
	SSHKey  string `short:"k" long:"key" description:"ssh key"`

	Env map[string]string `short:"e" long:"env" description:"environment variables for all commands"`

	// commands filter
	Skip []string `short:"s" long:"skip" description:"skip commands"`
	Only []string `short:"o" long:"only" description:"run only commands"`

	Dbg bool `long:"dbg" description:"debug mode"`
}

var revision = "latest"

func main() {
	fmt.Printf("simplotask %s\n", revision)

	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		if err.(*flags.Error).Type != flags.ErrHelp {
			os.Exit(1)
		}
		os.Exit(2)
	}
	setupLog(opts.Dbg)

	if err := run(opts); err != nil {
		if opts.Dbg {
			log.Panicf("[ERROR] %v", err)
		}
		fmt.Printf("failed: %v\n", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	st := time.Now()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	overrides := config.Overrides{
		TargetHosts:   opts.TargetHosts,
		InventoryFile: opts.InventoryFile,
		InventoryURL:  opts.InventoryURL,
		Environment:   opts.Env,
	}

	conf, err := config.New(opts.PlaybookFile, &overrides)
	if err != nil {
		return fmt.Errorf("can't read config: %w", err)
	}

	connector, err := executor.NewConnector(sshUserAndKey(opts, conf))
	if err != nil {
		return fmt.Errorf("can't create connector: %w", err)
	}
	r := runner.Process{
		Concurrency: opts.Concurrent,
		Connector:   connector,
		Config:      conf,
		Only:        opts.Only,
		Skip:        opts.Skip,
	}

	if opts.TaskName != "" { // run single task
		stats, err := r.Run(ctx, opts.TaskName, opts.TargetName)
		if err != nil {
			return err
		}
		fmt.Printf("completed: hosts:%d, commands:%d in %v\n",
			stats.Hosts, stats.Commands, time.Since(st).Truncate(100*time.Millisecond))
		return nil
	}

	// run all tasks
	for taskName := range conf.Tasks {
		if _, err := r.Run(ctx, taskName, opts.TargetName); err != nil {
			return err
		}
	}
	return nil
}

func sshUserAndKey(opts options, conf *config.PlayBook) (uname, key string) {
	expandPath := func(path string) (string, error) {
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

	sshUser := conf.User // default to global config user
	if tsk, ok := conf.Tasks[opts.TaskName]; ok && tsk.User != "" {
		sshUser = tsk.User // override with task config
	}
	if opts.SSHUser != "" { // override with command line
		sshUser = opts.SSHUser
	}

	sshKey := conf.SSHKey  // default to global config key
	if opts.SSHKey != "" { // override with command line
		sshKey = opts.SSHKey
	}

	if p, err := expandPath(sshKey); err == nil {
		sshKey = p
	}

	return sshUser, sshKey
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
