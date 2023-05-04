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
		log.Printf("[DEBUG] command %q is multiline, using script file", cmd.Name)
		return "", cmd.getScriptFile()
	}

	log.Printf("[DEBUG] command %q is single line, using script string", cmd.Name)
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

// UnmarshalYAML implements yaml.Unmarshaler interface
// It allows to unmarshal a "copy" from a single field or a slice
// All other fields are unmarshalled as usual. Limited to string, in, struct, slice or map
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

		// skip copy, processed separately
		if fieldName == "copy" {
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

func (cmd *Cmd) validate() error {
	flds := []string{}
	if cmd.Script != "" {
		flds = append(flds, "script")
	}
	if cmd.Copy.Source != "" && cmd.Copy.Dest != "" {
		flds = append(flds, "copy")
	}
	if len(cmd.MCopy) > 0 {
		flds = append(flds, "mcopy")
	}
	if cmd.Delete.Location != "" {
		flds = append(flds, "delete")
	}
	if cmd.Sync.Source != "" && cmd.Sync.Dest != "" {
		flds = append(flds, "sync")
	}
	if cmd.Wait.Command != "" {
		flds = append(flds, "wait")
	}
	if len(flds) > 1 {
		return fmt.Errorf("only one of [%s] is allowed", strings.Join(flds, ", "))
	}

	if len(flds) == 0 {
		return fmt.Errorf("one of [%s] must be set",
			strings.Join([]string{"script", "copy", "mcopy", "delete", "sync", "wait"}, ", "))
	}
	return nil
}
