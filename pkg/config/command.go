package config

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Cmd defines a single command. Yaml parsing is custom, because we want to allow "copy" to accept both single and multiple values
type Cmd struct {
	Name        string            `yaml:"name" toml:"name"`
	Copy        CopyInternal      `yaml:"copy" toml:"copy"`
	MCopy       []CopyInternal    `yaml:"mcopy" toml:"mcopy"`
	Sync        SyncInternal      `yaml:"sync" toml:"sync"`
	Delete      DeleteInternal    `yaml:"delete" toml:"delete"`
	Wait        WaitInternal      `yaml:"wait" toml:"wait"`
	Script      string            `yaml:"script" toml:"script,multiline"`
	Environment map[string]string `yaml:"env" toml:"env"`
	Options     CmdOptions        `yaml:"options" toml:"options,omitempty"`

	Secrets map[string]string `yaml:"-" toml:"-"` // loaded Secrets, filled by playbook
}

// CmdOptions defines options for a command
type CmdOptions struct {
	IgnoreErrors bool     `yaml:"ignore_errors" toml:"ignore_errors"` // ignore errors and continue
	NoAuto       bool     `yaml:"no_auto" toml:"no_auto"`             // don't run command automatically
	Local        bool     `yaml:"local" toml:"local"`                 // run command on localhost
	Sudo         bool     `yaml:"sudo" toml:"sudo"`                   // run command with sudo
	Secrets      []string `yaml:"secrets" toml:"secrets"`             // list of secrets (keys) to load
	OnlyOn       []string `yaml:"only_on" toml:"only_on"`             // only run on these hosts
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
func (cmd *Cmd) GetScript() (command string, rdr io.Reader) {
	if cmd.Script == "" {
		return "", nil
	}

	elems := strings.Split(cmd.Script, "\n")
	// export should be treated as multiline for env vars to be set
	if len(elems) > 1 || strings.Contains(cmd.Script, "export") {
		log.Printf("[DEBUG] command %q is multiline, using script file", cmd.Name)
		return "", cmd.scriptFile(cmd.Script)
	}

	log.Printf("[DEBUG] command %q is single line, using script string", cmd.Name)
	return cmd.scriptCommand(cmd.Script), nil
}

// GetWait returns a wait command as a string and an io.Reader based on whether the command is a single line or multiline
func (cmd *Cmd) GetWait() (command string, rdr io.Reader) {
	if cmd.Wait.Command == "" {
		return "", nil
	}

	elems := strings.Split(cmd.Wait.Command, "\n")
	if len(elems) > 1 {
		log.Printf("[DEBUG] wait command %q is multiline, using script file", cmd.Name)
		return "", cmd.scriptFile(cmd.Wait.Command)
	}

	log.Printf("[DEBUG] wait command %q is single line, using command string", cmd.Name)
	return cmd.scriptCommand(cmd.Wait.Command), nil
}

// scriptCommand concatenates all script line in commands into one a string to be executed by shell.
// Empty string is returned if no script is defined.
func (cmd *Cmd) scriptCommand(inp string) string {
	if inp == "" {
		return ""
	}

	// add environment variables
	envs := cmd.genEnv()
	res := "sh -c '"
	if len(envs) > 0 {
		res += strings.Join(envs, "; ") + "; "
	}

	// add secrets as environment variables
	secrets := cmd.getSecrets()
	if len(secrets) > 0 {
		res += strings.Join(secrets, "; ") + "; "
	}

	elems := strings.Split(inp, "\n")
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
	res += strings.Join(parts, "; ") + "'"
	return res
}

// scriptFile returns a reader for script file. All the line in the command used as a script, with hashbang,
// set -e and environment variables.
func (cmd *Cmd) scriptFile(inp string) (r io.Reader) {
	var buf bytes.Buffer

	buf.WriteString("#!/bin/sh\n") // add hashbang
	buf.WriteString("set -e\n")    // add 'set -e' to make the script exit on error

	envs := cmd.genEnv()
	envs = append(envs, cmd.getSecrets()...)
	// set environment variables for the script
	if len(envs) > 0 {
		for _, env := range envs {
			buf.WriteString(fmt.Sprintf("export %s\n", env))
		}
	}

	// process all the exported variables in the script
	exports := []string{} // we collect them all here to pass as setenv to the next command
	elems := strings.Split(inp, "\n")
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

		// if the line in the script is an export, add it to the list of exports
		// this is done to be able to print the variables set by the script to the console after the script is executed
		// those variables can be used by the caller to set environment variables for the next commands
		if strings.HasPrefix(c, "export") {
			expKey := strings.TrimPrefix(c, "export")
			expElems := strings.Split(expKey, "=")
			if len(expElems) != 2 {
				continue
			}
			expKey = strings.TrimSpace(expElems[0])
			if expKey == "" {
				continue // skip empty exports
			}
			exports = append(exports, expKey)
		}
	}

	// each exported variable is printed as a setvar command to be captured by the caller
	if len(exports) > 0 {
		for i := range exports {
			buf.WriteString(fmt.Sprintf("echo setvar %s=${%s}\n", exports[i], exports[i]))
		}
	}

	return &buf
}

// genEnv returns a sorted list of environment variables from the Environment map (part of the command)
func (cmd *Cmd) genEnv() []string {
	envs := make([]string, 0, len(cmd.Environment))
	for k, v := range cmd.Environment {
		envs = append(envs, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i] < envs[j] })
	return envs
}

// getSecrets returns a sorted list of secrets key from the secrets slice (part of the command)
func (cmd *Cmd) getSecrets() []string {
	secrets := []string{}
	for _, k := range cmd.Options.Secrets {
		if v := cmd.Secrets[k]; v != "" {
			secrets = append(secrets, fmt.Sprintf("%s=%q", k, v))
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i] < secrets[j] })
	return secrets
}

// UnmarshalYAML implements yaml.Unmarshaler interface
// It allows to unmarshal a "copy" from a single field or a slice
// All other fields are unmarshalled as usual. Limited to string, int, struct, slice or map
func (cmd *Cmd) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var asMap map[string]interface{}
	if err := unmarshal(&asMap); err != nil {
		return err
	}

	// helper function to unmarshal a field into a given target
	unmarshalField := func(fieldName string, target interface{}) error {
		fieldValue, exists := asMap[fieldName]
		if !exists {
			return nil
		}

		switch typedValue := fieldValue.(type) {
		case string:
			strTarget, ok := target.(*string)
			if !ok {
				return fmt.Errorf("expected string target for field '%s'", fieldName)
			}
			*strTarget = typedValue
		case int:
			intTarget, ok := target.(*int)
			if !ok {
				return fmt.Errorf("expected int target for field '%s'", fieldName)
			}
			*intTarget = typedValue
		case map[string]interface{}:
			fieldBytes, err := yaml.Marshal(typedValue)
			if err != nil {
				return err
			}
			if err := yaml.Unmarshal(fieldBytes, target); err != nil {
				return err
			}
		case []interface{}:
			fieldBytes, err := yaml.Marshal(typedValue)
			if err != nil {
				return err
			}
			if err := yaml.Unmarshal(fieldBytes, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported field type for '%s'", fieldName)
		}

		return nil
	}

	// iterate over all fields in the struct and unmarshal them
	structType := reflect.TypeOf(*cmd)
	structValue := reflect.ValueOf(cmd).Elem()
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		fieldName := field.Tag.Get("yaml")

		// skip copy, processed separately. fields without yaml tag or with "-" are skipped too
		if fieldName == "copy" || fieldName == "" || fieldName == "-" {
			continue
		}

		fieldPtr := structValue.Field(i).Addr().Interface()
		if err := unmarshalField(fieldName, fieldPtr); err != nil {
			return err
		}
	}

	// copy is a special case, as it can be either a struct or a list of structs
	if err := unmarshalField("copy", &cmd.Copy); err != nil {
		if err := unmarshalField("copy", &cmd.MCopy); err != nil {
			return err
		}
	}
	return nil
}

// validate checks if a Cmd has exactly one command type set (script, copy, mcopy, delete, sync, or wait)
// and returns an error if there are either multiple command types set or none set.
func (cmd *Cmd) validate() error {
	cmdTypes := []struct {
		name  string
		check func() bool
	}{
		{"script", func() bool { return cmd.Script != "" }},
		{"copy", func() bool { return cmd.Copy.Source != "" && cmd.Copy.Dest != "" }},
		{"mcopy", func() bool { return len(cmd.MCopy) > 0 }},
		{"delete", func() bool { return cmd.Delete.Location != "" }},
		{"sync", func() bool { return cmd.Sync.Source != "" && cmd.Sync.Dest != "" }},
		{"wait", func() bool { return cmd.Wait.Command != "" }},
	}

	setCmds, names := []string{}, []string{}
	for _, cmdType := range cmdTypes {
		names = append(names, cmdType.name)
		if cmdType.check() {
			setCmds = append(setCmds, cmdType.name)
		}
	}

	if len(setCmds) > 1 {
		return fmt.Errorf("only one of [%s] is allowed", strings.Join(setCmds, ", "))
	}

	if len(setCmds) == 0 {
		return fmt.Errorf("one of [%s] must be set", strings.Join(names, ", "))
	}
	return nil
}
