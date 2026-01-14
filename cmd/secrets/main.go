package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/fatih/color"
	"github.com/go-pkgz/lgr"
	"github.com/jessevdk/go-flags"
	"golang.org/x/term"

	"github.com/umputun/spot/pkg/secrets"
)

type options struct {
	Key  string `short:"k" long:"key" env:"SPOT_SECRETS_KEY" description:"key to use for encryption/decryption"`
	Conn string `short:"c" long:"conn" env:"SPOT_SECRETS_CONN" default:"spot.db" description:"connection string to use for the secrets database"`
	Dbg  bool   `long:"dbg" description:"debug mode"`

	SetCmd struct {
		PositionalArgs struct {
			Key   string `positional-arg-name:"key" description:"key to add"`
			Value string `positional-arg-name:"value" description:"value to add"`
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

	ListCmd struct {
		PositionalArgs struct {
			KeyPrefix string `positional-arg-name:"key-prefix" default:"*" description:"key prefix to list"`
		} `positional-args:"yes" positional-optional:"no"`
	} `command:"list" description:"list secrets keys"`
}

var revision = "latest"

var exitFunc = os.Exit

func main() {
	fmt.Printf("spot secrets %s\n", revision)

	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		exitFunc(1) // can be redefined in tests
	}

	setupLog(opts.Dbg) // always set up logging first

	if err := run(p, opts); err != nil {
		log.Printf("[WARN] %v", err)
	}
}

func run(p *flags.Parser, opts options) error {
	key := opts.Key

	// if key is not provided via flag or env, prompt for it
	if key == "" {
		promptedKey, err := readPasswordFromStdin("Enter secrets key: ")
		if err != nil {
			return fmt.Errorf("failed to read secrets key: %w", err)
		}
		key = promptedKey
	}

	sp, err := secrets.NewInternalProvider(opts.Conn, []byte(key))
	if err != nil {
		return fmt.Errorf("can't create secrets provider: %w", err)
	}

	// set secret
	if p.Active != nil && p.Find("set") == p.Active {
		log.Printf("[INFO] set command, key=%s", opts.SetCmd.PositionalArgs.Key)
		if opts.SetCmd.PositionalArgs.Value == "" {
			return fmt.Errorf("can't set empty secret for key %q", opts.SetCmd.PositionalArgs.Key)
		}
		if setErr := sp.Set(opts.SetCmd.PositionalArgs.Key, opts.SetCmd.PositionalArgs.Value); setErr != nil {
			return fmt.Errorf("can't set secret for key %q: %w", opts.SetCmd.PositionalArgs.Key, setErr)
		}
	}

	// get secret
	if p.Active != nil && p.Find("get") == p.Active {
		log.Printf("[INFO] get command, key=%s", opts.GetCmd.PositionalArgs.Key)
		val, getErr := sp.Get(opts.GetCmd.PositionalArgs.Key)
		if getErr != nil {
			return fmt.Errorf("can't get secret for key %q: %w", opts.GetCmd.PositionalArgs.Key, getErr)
		}
		// DO NOT mask the retrieved value - the user explicitly wants to see it!
		log.Printf("[INFO] key=%s, value=%s", opts.GetCmd.PositionalArgs.Key, val)
	}

	// delete secret
	if p.Active != nil && p.Find("del") == p.Active {
		log.Printf("[INFO] del command, key=%s", opts.DeleteCmd.PositionalArgs.Key)
		if delErr := sp.Delete(opts.DeleteCmd.PositionalArgs.Key); delErr != nil {
			return fmt.Errorf("can't delete secret: %w", delErr)
		}
		log.Printf("[INFO] key=%s deleted", opts.DeleteCmd.PositionalArgs.Key)
	}

	// list secrets
	if p.Active != nil && p.Find("list") == p.Active {
		log.Printf("[INFO] list command, key-prefix=%q", opts.ListCmd.PositionalArgs.KeyPrefix)
		keys, listErr := sp.List(opts.ListCmd.PositionalArgs.KeyPrefix)
		if listErr != nil {
			return fmt.Errorf("can't list secrets: %w", listErr)
		}
		for i, k := range keys {
			if i%4 == 0 && i != 0 {
				fmt.Println()
			}
			fmt.Printf("%s\t", k)
		}
		fmt.Println()
	}

	return nil
}

func setupLog(dbg bool) {
	logOpts := make([]lgr.Option, 0, 7) // max: 3 debug + 3 common + 1 colorizer
	if dbg {
		logOpts = append(logOpts, lgr.Debug, lgr.CallerFile, lgr.CallerFunc)
	}
	logOpts = append(logOpts, lgr.Msec, lgr.LevelBraces, lgr.StackTraceOnError)

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

// readPasswordFromStdin reads a password from stdin without echoing it
func readPasswordFromStdin(prompt string) (string, error) {
	// check if stdin is a terminal
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// not a terminal, read directly (for piped input)
		var password string
		if _, err := fmt.Scanln(&password); err != nil && err != io.EOF {
			return "", err
		}
		return password, nil
	}

	// terminal input, use secure password reading
	fmt.Fprint(os.Stderr, prompt)
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Fprintln(os.Stderr) // print newline after password input
	return string(passwordBytes), nil
}
