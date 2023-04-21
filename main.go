package main

import (
	"fmt"
	"log"
	"os"

	"github.com/fatih/color"
	"github.com/go-pkgz/lgr"
	"github.com/jessevdk/go-flags"
)

type options struct {
	TaskFile      string   `short:"f" long:"file" description:"task file" default:"stask.yml"`
	InventoryFile string   `short:"i" long:"inventory" description:"inventory file" default:"inventory.yml"`
	DestHost      []string `short:"h" long:"host" description:"destination host"`

	Dbg bool `long:"dbg" description:"debug mode"`
}

var revision = "latest"

func main() {
	fmt.Printf("simplotask %s\n", revision)

	var opts options
	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		if err.(*flags.Error).Type != flags.ErrHelp {
			fmt.Printf("%v", err)
		}
		os.Exit(1)
	}

	setupLog(opts.Dbg)
	if err := run(opts); err != nil {
		log.Panicf("[ERROR] %v", err)
	}
}

func run(opts options) error { //nolint
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
