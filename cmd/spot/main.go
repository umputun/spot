package main

import (
	"context"
	"errors"
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
	"gopkg.in/yaml.v3"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
	"github.com/umputun/spot/pkg/runner"
	"github.com/umputun/spot/pkg/secrets"
)

type options struct {
	PositionalArgs struct {
		AdHocCmd string `positional-arg-name:"command" description:"run ad-hoc command on target hosts"`
	} `positional-args:"yes" positional-optional:"yes"`

	PlaybookFile    string        `short:"p" long:"playbook" env:"SPOT_PLAYBOOK" description:"playbook file" default:"spot.yml"`
	TaskNames       []string      `short:"n" long:"task" description:"task name"`
	Targets         []string      `short:"t" long:"target" description:"target name" default:"default"`
	Concurrent      int           `short:"c" long:"concurrent" description:"concurrent tasks" default:"1"`
	SSHTimeout      time.Duration `long:"timeout" env:"SPOT_TIMEOUT" description:"ssh timeout" default:"30s"`
	SSHAgent        bool          `long:"ssh-agent" env:"SPOT_SSH_AGENT" description:"use ssh-agent"`
	ForwardSSHAgent bool          `long:"forward-ssh-agent" env:"SPOT_FORWARD_SSH_AGENT" description:"use forward-ssh-agent"`
	SSHShell        string        `long:"shell" env:"SPOT_SHELL" description:"enforce non-default shell to use for ssh" default:""`
	SSHTempDir      string        `long:"temp" env:"SPOT_TEMP" description:"temporary directory for ssh" default:""`

	// overrides
	Inventory string            `short:"i" long:"inventory" description:"inventory file or url [$SPOT_INVENTORY]"`
	SSHUser   string            `short:"u" long:"user" description:"ssh user"`
	SSHKey    string            `short:"k" long:"key" description:"ssh key"`
	Env       map[string]string `short:"e" long:"env" description:"environment variables for all commands"`
	EnvFile   string            `short:"E" long:"env-file" env:"SPOT_ENV_FILE" description:"environment variables from file" default:"env.yml"`

	// commands filter
	Skip []string `long:"skip" description:"skip commands"`
	Only []string `long:"only" description:"run only commands"`

	// secrets
	SecretsProvider SecretsProvider `group:"secrets" namespace:"secrets" env-namespace:"SPOT_SECRETS"`

	// generate inventory destinations from template
	GenEnable   bool   `long:"gen" description:"generate inventory destinations from template"`
	GenTemplate string `long:"gen.template" description:"template file" default:"json"`
	GenOutput   string `long:"gen.output" description:"output file" default:"stdout"`

	Version bool `short:"V" long:"version" description:"show version"`

	NoColor bool   `long:"no-color" env:"SPOT_NO_COLOR" description:"disable color output"`
	Dry     bool   `long:"dry" description:"dry run"`
	Verbose []bool `short:"v" long:"verbose" description:"verbosity level"`
	Dbg     bool   `long:"dbg" description:"debug mode"`
}

// SecretsProvider defines secrets provider options, for all supported providers
type SecretsProvider struct {
	Provider string `long:"provider" env:"PROVIDER" description:"secret provider type" choice:"none" choice:"spot" choice:"vault" choice:"aws" choice:"ansible-vault" default:"none"`

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

	AnsibleVault struct {
		VaultPath   string `long:"path" env:"PATH" description:"path to the ansible-vault file"`
		VaultSecret string `long:"secret" env:"SECRET" description:"secret string for decrypting ansible-vault file"`
	} `group:"ansible-vault" namespace:"ansible" env-namespace:"ANSIBLE"`
}

var revision = "latest"

func main() {
	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		if !errors.Is(err.(*flags.Error).Type, flags.ErrHelp) {
			// cli error, not help
			fmt.Printf("%v", err)
			os.Exit(1)
		}
		os.Exit(0) // help printed
	}

	if opts.Version {
		fmt.Printf("spot %s\n", revision)
		os.Exit(0)
	}

	setupLog(opts.Dbg) // set initial log, will be updated later with secrets

	if !opts.GenEnable || opts.GenOutput != "stdout" {
		fmt.Printf("spot %s\n", revision) // print version only if not generating inventory to stdout
	}

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
		printDryRunWarn(opts.Dbg)
	}

	st := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	inventoryFile, err := inventoryFile(opts.Inventory)
	if err != nil {
		return fmt.Errorf("can't get inentory %q: %w", opts.Inventory, err)
	}

	pbook, err := makePlaybook(opts, inventoryFile)
	if err != nil {
		return fmt.Errorf("can't get playbook %q: %w", opts.PlaybookFile, err)
	}

	// secrets are known only after playbook is loaded
	setupLog(opts.Dbg, pbook.AllSecretValues()...) // mask secrets in logs

	r, err := makeRunner(opts, pbook)
	if err != nil {
		return fmt.Errorf("can't make runner: %w", err)
	}

	if opts.PositionalArgs.AdHocCmd != "" { // run ad-hoc command
		if r.Playbook, err = setAdHocSSH(opts, pbook); err != nil {
			return fmt.Errorf("can't setup ad-hoc ssh params: %w", err)
		}
		return runAdHoc(ctx, opts.Targets, r)
	}

	if opts.GenEnable {
		// generate a list of destination from inventory targets
		return runGen(opts, r)
	}

	if err := runTasks(ctx, opts.TaskNames, opts.Targets, r); err != nil {
		return err
	}

	log.Printf("[INFO] completed all %d targets in %v", len(opts.Targets), time.Since(st).Truncate(100*time.Millisecond))
	return nil
}

// runTasks runs all tasks in playbook by default or a single task if specified in command line
func runTasks(ctx context.Context, taskNames, targets []string, r *runner.Process) error {
	// run specified tasks if there is any
	if len(taskNames) > 0 {
		for _, taskName := range taskNames {
			for _, targetName := range targetsForTask(targets, taskName, r.Playbook) {
				if err := runTaskForTarget(ctx, r, taskName, targetName); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// run all tasks in playbook if no task specified
	for _, task := range r.Playbook.AllTasks() {
		for _, targetName := range targetsForTask(targets, task.Name, r.Playbook) {
			if err := runTaskForTarget(ctx, r, task.Name, targetName); err != nil {
				return err
			}
		}
	}
	return nil
}

func runAdHoc(ctx context.Context, targets []string, r *runner.Process) error {
	errs := new(multierror.Error)
	r.Verbose = true // always verbose for ad-hoc
	for _, targetName := range targets {
		if err := runTaskForTarget(ctx, r, "ad-hoc", targetName); err != nil {
			errs = multierror.Append(errs, err)
		}
	}
	return errs.ErrorOrNil()
}

// runGen generates a destination report for the tasks' targets
func runGen(opts options, r *runner.Process) (err error) {
	var targets []string
	uniqueTargets := make(map[string]bool)
	for _, taskName := range opts.TaskNames {
		for _, target := range targetsForTask(opts.Targets, taskName, r.Playbook) {
			// ensure there is no duplicates in the targets list
			if _, exists := uniqueTargets[target]; !exists {
				uniqueTargets[target] = true
				targets = append(targets, target)
			}
		}
	}

	var fh io.ReadCloser
	if opts.GenTemplate != "" && opts.GenTemplate != "json" {
		fh, err = os.Open(opts.GenTemplate)
		if err != nil {
			return fmt.Errorf("can't open template file %q: %w", opts.GenTemplate, err)
		}
		defer fh.Close() // nolint this is read-only
	}

	wr := os.Stdout
	if opts.GenOutput != "" && opts.GenOutput != "stdout" {
		log.Printf("[INFO] writing report to %q", opts.GenOutput)
		wr, err = os.Create(opts.GenOutput)
		if err != nil {
			return fmt.Errorf("can't open output file %q: %w", opts.GenOutput, err)
		}
		defer wr.Close() // nolint this happens after sync
	}

	err = r.Gen(targets, fh, wr)
	if err != nil {
		return fmt.Errorf("can't generate report: %w", err)
	}
	if err = wr.Sync(); err != nil {
		return fmt.Errorf("can't sync report: %w", err)
	}
	return nil
}

func printDryRunWarn(dbg bool) {
	if dbg {
		log.Printf("[WARN] dry run, no changes will be made and no commands will be executed")
		return
	}
	msg := color.New(color.FgHiRed).SprintfFunc()("dry run - no changes will be made and no commands will be executed\n")
	fmt.Print(msg)
}

func inventoryFile(inventory string) (string, error) {
	exInventory, err := expandPath(inventory)
	if err != nil {
		return "", fmt.Errorf("can't expand inventory path %q: %w", exInventory, err)
	}
	return exInventory, nil
}

func makePlaybook(opts options, inventory string) (*config.PlayBook, error) {
	// makeSecretProvider creates secret provider based on options
	makeSecretProvider := func(sopts SecretsProvider) (config.SecretsProvider, error) {
		switch sopts.Provider {
		case "none":
			return &secrets.NoOpProvider{}, nil
		case "spot":
			return secrets.NewInternalProvider(sopts.Conn, []byte(sopts.Key))
		case "vault":
			return secrets.NewHashiVaultProvider(sopts.Vault.URL, sopts.Vault.Path, sopts.Vault.Token)
		case "aws":
			return secrets.NewAWSSecretsProvider(sopts.Aws.AccessKey, sopts.Aws.SecretKey, sopts.Aws.Region)
		case "ansible-vault":
			return secrets.NewAnsibleVaultProvider(sopts.AnsibleVault.VaultPath, sopts.AnsibleVault.VaultSecret)
		}
		log.Printf("[WARN] unknown secrets provider %q", sopts.Provider)
		return &secrets.NoOpProvider{}, nil
	}

	env, err := envVars(opts.Env, opts.EnvFile)
	if err != nil {
		return nil, fmt.Errorf("can't read environment variables: %w", err)
	}

	overrides := config.Overrides{
		Inventory:    inventory,
		Environment:  env,
		User:         opts.SSHUser,
		AdHocCommand: opts.PositionalArgs.AdHocCmd,
		SSHShell:     opts.SSHShell,
	}

	exPlaybookFile, err := expandPath(opts.PlaybookFile)
	if err != nil {
		return nil, fmt.Errorf("can't expand playbook path %q: %w", opts.PlaybookFile, err)
	}

	secretsProvider, err := makeSecretProvider(opts.SecretsProvider)
	if err != nil {
		return nil, fmt.Errorf("can't make secrets provider: %w", err)
	}

	pbook, err := config.New(exPlaybookFile, &overrides, secretsProvider)
	if err != nil {
		return nil, fmt.Errorf("can't load playbook %q: %w", exPlaybookFile, err)
	}

	if pbook.User, err = sshUser(opts.SSHUser, pbook); err != nil {
		return nil, fmt.Errorf("can't get ssh user: %w", err)
	}

	return pbook, nil
}

func makeRunner(opts options, pbook *config.PlayBook) (*runner.Process, error) {
	sshKey, err := sshKey(opts.SSHAgent, opts.SSHKey, pbook)
	if err != nil {
		return nil, fmt.Errorf("can't get ssh key: %w", err)
	}
	logs := executor.MakeLogs(len(opts.Verbose) > 0, opts.NoColor, pbook.AllSecretValues())
	connector, err := executor.NewConnector(sshKey, opts.SSHTimeout, logs)
	if err != nil {
		return nil, fmt.Errorf("can't create connector: %w", err)
	}
	if opts.SSHAgent {
		connector = connector.WithAgent()
	}

	if opts.ForwardSSHAgent {
		connector = connector.WithAgentForwarding()
	}

	r := runner.Process{
		Concurrency: opts.Concurrent,
		Connector:   connector,
		Playbook:    pbook,
		Only:        opts.Only,
		Skip:        opts.Skip,
		Logs:        logs,
		Verbose:     len(opts.Verbose) > 0,
		Verbose2:    len(opts.Verbose) > 1,
		Dry:         opts.Dry,
		SSHShell:    opts.SSHShell,
		SSHTempDir:  opts.SSHTempDir,
	}
	log.Printf("[DEBUG] runner created: concurrency:%d, connector: %s, ssh_shell:%q, verbose:%v, dry:%v, only:%v, skip:%v",
		r.Concurrency, r.Connector, r.SSHShell, r.Verbose, r.Dry, r.Only, r.Skip)

	return &r, nil
}

func runTaskForTarget(ctx context.Context, r *runner.Process, taskName, targetName string) error {
	st := time.Now()
	res, err := r.Run(ctx, taskName, targetName)
	if err != nil {
		return fmt.Errorf("can't run task %q for target %q: %w", taskName, targetName, err)
	}
	log.Printf("[INFO] completed: hosts:%d, commands:%d in %v\n",
		res.Hosts, res.Commands, time.Since(st).Truncate(100*time.Millisecond))
	r.Playbook.UpdateTasksTargets(res.Vars)         // for dynamic targets
	r.Playbook.UpdateRegisteredVars(res.Registered) // for registered vars, cross-task
	return nil
}

// get the list of targets for the task. Usually this is just a list of all targets from the command line,
// however, if the task has targets defined AND cli has the default target, then only those targets will be used.
func targetsForTask(targets []string, taskName string, pbook runner.Playbook) []string {
	if len(targets) > 1 || (len(targets) == 1 && targets[0] != "default") {
		// non-default target specified on command line
		return targets
	}

	tsk, err := pbook.Task(taskName)
	if err != nil {
		// this should never happen, task name is validated on playbook level
		return targets
	}

	if len(tsk.Targets) == 0 {
		// no targets defined for task
		return targets
	}

	log.Printf("[INFO] task %q has %d targets [%s] pre-defined", taskName, len(tsk.Targets), strings.Join(tsk.Targets, ", "))
	return tsk.Targets
}

// get ssh key from cli or playbook. if no key is provided, use default ~/.ssh/id_rsa
func sshKey(sshAgent bool, sshKey string, pbook *config.PlayBook) (key string, err error) {
	if sshKey == "" && !sshAgent && pbook != nil && pbook.SSHKey != "" { // no key provided in cli and not using agent
		sshKey = pbook.SSHKey // use playbook's ssh_key
	}
	if p, err := expandPath(sshKey); err == nil {
		sshKey = p
	}

	if sshKey == "" { // no key provided in cli or playbook
		u, err := userProvider.Current()
		if err != nil {
			return "", fmt.Errorf("can't get current user: %w", err)
		}
		if !sshAgent {
			sshKey = filepath.Join(u.HomeDir, ".ssh", "id_rsa")
		}
	}

	log.Printf("[INFO] ssh key: %s", sshKey)
	return sshKey, nil
}

// get ssh user from cli or playbook. if no user is provided, use current user from os
func sshUser(sshUser string, pbook *config.PlayBook) (string, error) {
	if sshUser == "" && (pbook == nil || pbook.User != "") { // no user provided in cli
		sshUser = pbook.User // use playbook's user
	}
	if sshUser == "" { // no user provided in cli or playbook
		u, err := userProvider.Current()
		if err != nil {
			return "", fmt.Errorf("can't get current user: %w", err)
		}
		sshUser = u.Username
	}
	return sshUser, nil
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		usr, err := userProvider.Current()
		if err != nil {
			return "", err
		}
		home := usr.HomeDir
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

// setAdHocSSH updates playbook with ssh user and key from cli the os
func setAdHocSSH(opts options, pbook *config.PlayBook) (*config.PlayBook, error) {
	if opts.SSHUser == "" {
		u, err := userProvider.Current()
		if err != nil {
			return nil, fmt.Errorf("can't get current user: %w", err)
		}
		pbook.User = u.Username
	}
	if opts.SSHKey == "" {
		u, err := userProvider.Current()
		if err != nil {
			return nil, fmt.Errorf("can't get current user: %w", err)
		}
		pbook.SSHKey = filepath.Join(u.HomeDir, ".ssh", "id_rsa")
	}
	return pbook, nil
}

// envVars returns a map of environment variables from the cli, env file, and system env.
// cli vars override env file vars, which in turn override system env vars if duplicated.
func envVars(vars map[string]string, envFile string) (map[string]string, error) {
	res := make(map[string]string)

	expandEnv := func(value string) string {
		// expand environment variables denoted by $var or ${var}
		return os.Expand(value, os.Getenv)
	}

	// load env file vars
	envFileData := struct {
		Vars map[string]string `yaml:"vars"`
	}{}
	fh, err := os.Open(envFile) //nolint:gosec // file inclusion from cli is intentional
	if err == nil {
		defer fh.Close()
		if err := yaml.NewDecoder(fh).Decode(&envFileData); err != nil {
			log.Printf("[WARN] can't parse env file %q: %v", envFile, err)
		}
		for k, v := range envFileData.Vars {
			res[k] = expandEnv(v)
		}
	}

	// cli vars override env file vars
	for k, v := range vars {
		res[k] = expandEnv(v)
	}

	return res, nil
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

func setupLog(dbg bool, secs ...string) {
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
	if len(secs) > 0 {
		logOpts = append(logOpts, lgr.Secret(secs...))
	}
	lgr.SetupStdLogger(logOpts...)
	lgr.Setup(logOpts...)
}

// userProvider is used to get current user. It's a var so it can be mocked in tests
var userProvider userInfoProvider = &defaultUserInfoProvider{}

type userInfoProvider interface {
	Current() (*user.User, error)
}

type defaultUserInfoProvider struct{}

func (p *defaultUserInfoProvider) Current() (*user.User, error) {
	return user.Current()
}
