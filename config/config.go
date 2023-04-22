package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlayBook defines top-level config yaml
type PlayBook struct {
	User  string          `yaml:"user"`
	Tasks map[string]Task `yaml:"tasks"`
}

// Task defines multiple commands runs together
type Task struct {
	Before   string `yaml:"before"`
	After    string `yaml:"after"`
	OnError  string `yaml:"onerror"`
	Commands []Cmd  `yaml:"commands"`
}

// Cmd defines a single command
type Cmd struct {
	Name    string       `yaml:"name"`
	Log     string       `yaml:"log"`
	Copy    CopyInternal `yaml:"copy"`
	Sync    SyncInternal `yaml:"sync"`
	Script  string       `yaml:"script"`
	Before  string       `yaml:"before"`
	After   string       `yaml:"after"`
	OnError string       `yaml:"onerror"`
	Options struct {
		IgnoreErrors bool `yaml:"ignore_errors"`
		NoAuto       bool `yaml:"no_auto"`
		Local        bool `yaml:"local"`
	} `yaml:"options"`
}

// CopyInternal defines copy command, implemented internally
type CopyInternal struct {
	Source string `yaml:"src"`
	Dest   string `yaml:"dest"`
	Mkdir  bool   `yaml:"mkdir"`
}

// SyncInternal defines sync command (recursive copy), implemented internally
type SyncInternal struct {
	Source string `yaml:"src"`
	Dest   string `yaml:"dest"`
	Delete bool   `yaml:"delete"`
}

// New makes new config from yml
func New(fname string) (*PlayBook, error) {
	res := &PlayBook{}
	data, err := os.ReadFile(fname) // nolint
	if err != nil {
		return nil, fmt.Errorf("can't read config %s: %w", fname, err)
	}

	if err = yaml.Unmarshal(data, res); err != nil {
		return nil, fmt.Errorf("can't unmarshal config %s: %w", fname, err)
	}

	log.Printf("[INFO] playbook loaded with %d tasks", len(res.Tasks))
	for tnm, tsk := range res.Tasks {
		for _, c := range tsk.Commands {
			log.Printf("[DEBUG] load task %s command %s", tnm, c.Name)
		}
	}
	return res, nil
}

// Task returns task by name
func (p *PlayBook) Task(name string) (*Task, error) {
	if t, ok := p.Tasks[name]; ok {
		return &t, nil
	}
	return nil, fmt.Errorf("task %s not found", name)
}

// GetScript concatenates all script line in commands into one a string to be executed by shell.
// Empty string is returned if no script is defined.
func (cmd *Cmd) GetScript() string {
	if cmd.Script == "" {
		return ""
	}
	elems := strings.Split(cmd.Script, "\n")
	res := "sh -c \""
	var parts []string //nolint
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
	res += strings.Join(parts, "; ")
	return res
}
