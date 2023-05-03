package config

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Cmd defines a single command
type Cmd struct {
	Name        string            `yaml:"name" toml:"name"`
	Copy        CopyInternal      `yaml:"copy" toml:"copy"`
	Sync        SyncInternal      `yaml:"sync" toml:"sync"`
	Delete      DeleteInternal    `yaml:"delete" toml:"delete"`
	Wait        WaitInternal      `yaml:"wait" toml:"wait"`
	Script      string            `yaml:"script" toml:"script,multiline"`
	Environment map[string]string `yaml:"env" toml:"env"`
	Options     struct {
		IgnoreErrors bool `yaml:"ignore_errors" toml:"ignore_errors"`
		NoAuto       bool `yaml:"no_auto" toml:"no_auto"`
		Local        bool `yaml:"local" toml:"local"`
	} `yaml:"options" toml:"options,omitempty"`
}

// CopyInternal defines copy command, implemented internally
type CopyInternal struct {
	Source string `yaml:"src" toml:"src"`
	Dest   string `yaml:"dst" toml:"dst"`
	Mkdir  bool   `yaml:"mkdir" toml:"mkdir"`
}

// SyncInternal defines sync command (recursive copy), implemented internally
type SyncInternal struct {
	Source string `yaml:"src" toml:"src"`
	Dest   string `yaml:"dst" toml:"dst"`
	Delete bool   `yaml:"delete" toml:"delete"`
}

// DeleteInternal defines delete command, implemented internally
type DeleteInternal struct {
	Location  string `yaml:"path" toml:"path"`
	Recursive bool   `yaml:"recur" toml:"recur"`
}

// WaitInternal defines wait command, implemented internally
type WaitInternal struct {
	Timeout       time.Duration `yaml:"timeout" toml:"timeout"`
	CheckDuration time.Duration `yaml:"interval" toml:"interval"`
	Command       string        `yaml:"cmd" toml:"cmd,multiline"`
}

// GetScript returns a script string and an io.Reader based on the command being single line or multiline.
func (cmd *Cmd) GetScript() (string, io.Reader) {
	if cmd.Script == "" {
		return "", nil
	}

	elems := strings.Split(cmd.Script, "\n")
	if len(elems) > 1 {
		return "", cmd.getScriptFile()
	}

	return cmd.getScriptCommand(), nil
}

// GetScriptCommand concatenates all script line in commands into one a string to be executed by shell.
// Empty string is returned if no script is defined.
func (cmd *Cmd) getScriptCommand() string {
	if cmd.Script == "" {
		return ""
	}

	envs := cmd.genEnv()
	res := "sh -c \""
	if len(envs) > 0 {
		res += strings.Join(envs, " ") + " "
	}

	elems := strings.Split(cmd.Script, "\n")
	var parts []string // nolint
	for _, el := range elems {
		c := strings.TrimSpace(el)
		if len(c) < 2 {
			continue
		}
		if i := strings.Index(c, "#"); i > 0 {
			c = strings.TrimSpace(c[:i])
		}
		parts = append(parts, c)
	}
	res += strings.Join(parts, "; ") + "\""
	return res
}

// GetScriptFile returns a reader for script file. All the line in the command used as a script, with hashbang,
// set -e and environment variables.
func (cmd *Cmd) getScriptFile() io.Reader {
	var buf bytes.Buffer

	buf.WriteString("#!/bin/sh\n") // add hashbang
	buf.WriteString("set -e\n")    // add 'set -e' to make the script exit on error

	envs := cmd.genEnv()
	// set environment variables for the script
	if len(envs) > 0 {
		for _, env := range envs {
			buf.WriteString(fmt.Sprintf("export %s\n", env))
		}
	}

	elems := strings.Split(cmd.Script, "\n")
	for _, el := range elems {
		c := strings.TrimSpace(el)
		if len(c) < 2 {
			continue
		}
		if strings.HasPrefix(c, "#") {
			continue
		}
		if i := strings.Index(c, "#"); i > 0 {
			c = strings.TrimSpace(c[:i])
		}
		buf.WriteString(c)
		buf.WriteString("\n")
	}

	return &buf
}

func (cmd *Cmd) genEnv() []string {
	envs := make([]string, 0, len(cmd.Environment))
	for k, v := range cmd.Environment {
		envs = append(envs, fmt.Sprintf("%s='%s'", k, v))
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i] < envs[j] })
	return envs
}
