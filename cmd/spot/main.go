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
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/go-pkgz/lgr"
	"github.com/hashicorp/go-multierror"
	"github.com/jessevdk/go-flags"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
	"github.com/umputun/spot/pkg/runner"
	"github.com/umputun/spot/pkg/secrets"
)

type options struct {
	PositionalArgs struct {
		AdHocCmd string `positional-arg-name:"command" description:"run ad-hoc command on target hosts"`
	} `positional-args:"yes" positional-optional:"yes"`

	PlaybookFile string        `short:"p" long:"playbook" env:"SPOT_PLAYBOOK" description:"playbook file" default:"spot.yml"`
	TaskName     string        `long:"task" description:"task name"`
	Targets      []string      `short:"t" long:"target" description:"target name" default:"default"`
	Concurrent   int           `short:"c" long:"concurrent" description:"concurrent tasks" default:"1"`
	SSHTimeout   time.Duration `long:"timeout" env:"SPOT_TIMEOUT" description:"ssh timeout" default:"30s"`

	// overrides
	Inventory string            `short:"i" long:"inventory" description:"inventory file or url [$SPOT_INVENTORY]"`
	SSHUser   string            `short:"u" long:"user" description:"ssh user"`
	SSHKey    string            `short:"k" long:"key" description:"ssh key"`
	Env       map[string]string `short:"e" long:"env" description:"environment variables for all commands"`

	// commands filter
	Skip []string `long:"skip" description:"skip commands"`
	Only []string `long:"only" description:"run only commands"`

	// secrets
	SecretsProvider SecretsProvider `group:"secrets" namespace:"secrets" env-namespace:"SPOT_SECRETS"`

	Version bool `long:"version" description:"show version"`

	Dry     bool `long:"dry" description:"dry run"`
	Verbose bool `short:"v" long:"verbose" description:"verbose mode"`
	Dbg     bool `long:"dbg" description:"debug mode"`
}

// SecretsProvider defines secrets provider options, for all supported providers
type SecretsProvider struct {
	Provider string `long:"provider" env:"PROVIDER" description:"secret provider type" choice:"none" choice:"spot" choice:"vault" choice:"aws" default:"none"`

	Key  string `long:"key" env:"KEY" description:"secure key for spot secrets provider"`
	Conn string `long:"conn" env:"CONN" description:"connection string for spot secrets provider" default:"spot.db"`

	Vault struct {
		Token string `long:"token" env:"TOKEN" description:"vault token"`
		Path  string `long:"path"  env:"PATH" description:"vault path"`
		URL   string `long:"url" env:"URL" description:"vault url"`
	} `group:"vault" namespace:"vault" env-namespace:"VAULT"`

	Aws struct {
		Region    string `long:"region" env:"REGION" description:"aws region"`
		AccessKey string `long:"access-key" env:"ACCESS_KEY" description:"aws access key"`
		SecretKey string `long:"secret-key" env:"SECRET_KEY" description:"aws secret key"`
	} `group:"aws" namespace:"aws" env-namespace:"AWS"`
}

var revision = "latest"

func main() {
	fmt.Printf("spot %s\n", revision)

	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		os.Exit(1)
	}
	if opts.Version {
		os.Exit(0) // already printed
	}
	setupLog(opts.Dbg)

	if err := run(opts); err != nil {
		if opts.Dbg {
			log.Panicf("[ERROR] %v", err)
		}
		fmt.Printf("failed, %v\n", formatErrorString(err.Error()))
		os.Exit(1)
	}
}

func run(opts options) error {
	if opts.Dry {
		if opts.Dbg {
			log.Printf("[WARN] dry run, no changes will be made and no commands will be executed")
		} else {
			msg := color.New(color.FgHiRed).SprintfFunc()("dry run - no changes will be made and no commands will be executed\n")
			fmt.Print(msg)
		}
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

	secretsProvider, err := makeSecretsProvider(opts.SecretsProvider)
	if err != nil {
		return fmt.Errorf("can't make secrets provider: %w", err)
	}

	conf, err := config.New(exPlaybookFile, &overrides, secretsProvider)
	if err != nil {
		return fmt.Errorf("can't load playbook %q: %w", exPlaybookFile, err)
	}

	lgr.Setup(lgr.Secret(conf.AllSecretValues()...)) // mask secrets in logs

	if conf.User, err = sshUser(opts, conf, &defaultUserInfoProvider{}); err != nil {
		return fmt.Errorf("can't get ssh user: %w", err)
	}

	if opts.PositionalArgs.AdHocCmd != "" {
		if err = adHocConf(opts, conf, &defaultUserInfoProvider{}); err != nil {
			return fmt.Errorf("can't setup ad-hoc config: %w", err)
		}
	}

	sshKey, err := sshKey(opts, conf, &defaultUserInfoProvider{})
	if err != nil {
		return fmt.Errorf("can't get ssh key: %w", err)
	}
	log.Printf("[INFO] ssh key: %s", sshKey)

	connector, err := executor.NewConnector(sshKey, opts.SSHTimeout)
	if err != nil {
		return fmt.Errorf("can't create connector: %w", err)
	}
	r := runner.Process{
		Concurrency: opts.Concurrent,
		Connector:   connector,
		Config:      conf,
		Only:        opts.Only,
		Skip:        opts.Skip,
		ColorWriter: executor.NewColorizedWriter(os.Stdout, "", "", "", nil),
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

	if opts.TaskName != "" { // run a single task
		for _, targetName := range targetsForTask(opts, opts.TaskName, conf) {
			if err := runTaskForTarget(ctx, r, opts.TaskName, targetName); err != nil {
				return err
			}
		}
		return nil
	}

	// run all tasks
	for _, task := range conf.Tasks {
		for _, targetName := range targetsForTask(opts, task.Name, conf) {
			if err := runTaskForTarget(ctx, r, task.Name, targetName); err != nil {
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

// get the list of targets for the task. Usually this is just a list of all targets from the command line,
// however, if the task has targets defined AND cli has the default target, then only those targets will be used.
func targetsForTask(opts options, taskName string, conf *config.PlayBook) []string {
	if len(opts.Targets) > 1 || (len(opts.Targets) == 1 && opts.Targets[0] != "default") {
		// non-default target specified on command line
		return opts.Targets
	}

	lookupTask := func(name string) (tsk config.Task) {
		// get task by name
		for _, t := range conf.Tasks {
			if t.Name == taskName {
				tsk = t
				return tsk
			}
		}
		return tsk
	}

	tsk := lookupTask(taskName)
	if tsk.Name == "" {
		// this should never happen, task name is validated on playbook level
		return opts.Targets
	}

	if len(tsk.Targets) == 0 {
		// no targets defined for task
		return opts.Targets
	}

	log.Printf("[INFO] task %q has %d targets [%s] pre-defined", taskName, len(tsk.Targets), strings.Join(tsk.Targets, ", "))
	return tsk.Targets
}

// makeSecretsProvider creates secrets provider based on options
func makeSecretsProvider(sopts SecretsProvider) (config.SecretsProvider, error) {
	switch sopts.Provider {
	case "none":
		return &secrets.NoOpProvider{}, nil
	case "spot":
		return secrets.NewInternalProvider(sopts.Conn, []byte(sopts.Key))
	case "vault":
		return secrets.NewHashiVaultProvider(sopts.Vault.URL, sopts.Vault.Path, sopts.Vault.Token)
	case "aws":
		return secrets.NewAWSSecretsProvider(sopts.Aws.AccessKey, sopts.Aws.SecretKey, sopts.Aws.Region)
	}
	log.Printf("[WARN] unknown secrets provider %q", sopts.Provider)
	return &secrets.NoOpProvider{}, nil
}

// get ssh key from cli or playbook. if no key is provided, use default ~/.ssh/id_rsa
func sshKey(opts options, conf *config.PlayBook, provider userInfoProvider) (key string, err error) {
	sshKey := opts.SSHKey
	if sshKey == "" && (conf == nil || conf.SSHKey != "") { // no key provided in cli
		sshKey = conf.SSHKey // use playbook's ssh_key
	}
	if p, err := expandPath(sshKey); err == nil {
		sshKey = p
	}

	if sshKey == "" { // no key provided in cli or playbook
		u, err := provider.Current()
		if err != nil {
			return "", fmt.Errorf("can't get current user: %w", err)
		}
		sshKey = filepath.Join(u.HomeDir, ".ssh", "id_rsa")
	}
	return sshKey, nil
}

// get ssh user from cli or playbook. if no user is provided, use current user from os
func sshUser(opts options, conf *config.PlayBook, provider userInfoProvider) (res string, err error) {
	sshUser := opts.SSHUser
	if sshUser == "" && (conf == nil || conf.User != "") { // no user provided in cli
		sshUser = conf.User // use playbook's user
	}
	if sshUser == "" { // no user provided in cli or playbook
		u, err := provider.Current()
		if err != nil {
			return "", fmt.Errorf("can't get current user: %w", err)
		}
		sshUser = u.Username
	}
	return sshUser, nil
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

func formatErrorString(input string) string {
	headerRe := regexp.MustCompile(`(.*: \d+ error\(s\) occurred:)`)
	headerMatch := headerRe.FindStringSubmatch(input)

	if len(headerMatch) == 0 {
		return input
	}

	errorsRe := regexp.MustCompile(`\[\d+] {([^}]+)}`)
	errorsMatches := errorsRe.FindAllStringSubmatch(input, -1)

	formattedErrors := make([]string, 0, len(errorsMatches))
	for _, match := range errorsMatches {
		formattedErrors = append(formattedErrors, strings.TrimSpace(match[1]))
	}

	formattedString := fmt.Sprintf("%s\n", strings.TrimSpace(headerMatch[1]))
	for i, err := range formattedErrors {
		formattedString += fmt.Sprintf("   [%d] %s\n", i, err)
	}

	return formattedString
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
