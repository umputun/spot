package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/go-pkgz/lgr"
	"github.com/jessevdk/go-flags"

	"github.com/umputun/spot/pkg/secrets"
)

type options struct {
	Key  string `short:"k" long:"key" env:"SPOT_SECRETS_INTERNAL_KEY" description:"key to use for encryption/decryption"`
	Conn string `short:"c" long:"conn" env:"SPOT_SECRETS_INTERNAL_CONN" description:"connection string to use for the secrets database"`
	Dbg  bool   `long:"dbg" description:"debug mode"`

	SetCmd struct {
		PositionalArgs struct {
			KV string `positional-arg-name:"key-val" description:"key=value pair to add"`
		} `positional-args:"yes" positional-optional:"no"`
	} `command:"set" description:"add a new secret"`

	GetCmd struct {
		PositionalArgs struct {
			Key string `positional-arg-name:"key" description:"key to retrieve"`
		} `positional-args:"yes" positional-optional:"no"`
	} `command:"get" description:"retrieve a secret"`

	DeleteCmd struct {
		PositionalArgs struct {
			Key string `positional-arg-name:"key" description:"key to delete"`
		} `positional-args:"yes" positional-optional:"no"`
	} `command:"del" description:"delete a secret"`
}

var revision = "latest"

func main() {
	fmt.Printf("spot secrets %s\n", revision)

	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		os.Exit(1)
	}
	setupLog(opts.Dbg)

	if err := run(p, opts); err != nil {
		log.Printf("[WARN] %v", err)
	}
}

func run(p *flags.Parser, opts options) error {
	sp, err := secrets.NewInternalProvider(opts.Conn, []byte(opts.Key))
	if err != nil {
		return fmt.Errorf("can't create secrets provider: %w", err)
	}

	// set secret
	if p.Active != nil && p.Command.Find("set") == p.Active {
		elems := strings.SplitN(opts.SetCmd.PositionalArgs.KV, "=", 2)
		if len(elems) != 2 {
			return fmt.Errorf("key=val pair is required")
		}
		log.Printf("[INFO] set command, key=%s", elems[0])
		if setErr := sp.Set(elems[0], elems[1]); setErr != nil {
			return fmt.Errorf("can't set secret for key %q: %w", elems[0], setErr)
		}
	}

	// get secret
	if p.Active != nil && p.Command.Find("get") == p.Active {
		log.Printf("[INFO] get command, key=%s", opts.GetCmd.PositionalArgs.Key)
		val, getErr := sp.Get(opts.GetCmd.PositionalArgs.Key)
		if getErr != nil {
			return fmt.Errorf("can't get secret for key %q: %w", opts.GetCmd.PositionalArgs.Key, getErr)
		}
		log.Printf("[INFO] key=%s, value=%s", opts.GetCmd.PositionalArgs.Key, val)
	}

	// delete secret
	if p.Active != nil && p.Command.Find("del") == p.Active {
		log.Printf("[INFO] del command, key=%s", opts.DeleteCmd.PositionalArgs.Key)
		if delErr := sp.Delete(opts.DeleteCmd.PositionalArgs.Key); delErr != nil {
			return fmt.Errorf("can't delete secret: %w", delErr)
		}
		log.Printf("[INFO] key=%s deleted", opts.DeleteCmd.PositionalArgs.Key)
	}

	return nil
}

func setupLog(dbg bool) {
	logOpts := []lgr.Option{lgr.Msec, lgr.LevelBraces, lgr.StackTraceOnError}
	if dbg {
		logOpts = []lgr.Option{lgr.Debug, lgr.CallerFile, lgr.CallerFunc, lgr.Msec, lgr.LevelBraces, lgr.StackTraceOnError}
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
