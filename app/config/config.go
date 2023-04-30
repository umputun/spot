package config

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/copier"
	"gopkg.in/yaml.v3"
)

// PlayBook defines top-level config yaml
type PlayBook struct {
	User    string            `yaml:"user"`
	SSHKey  string            `yaml:"ssh_key"`
	Targets map[string]Target `yaml:"targets"`
	Tasks   map[string]Task   `yaml:"tasks"`

	overrides *Overrides
}

// Target defines hosts to run commands on
type Target struct {
	Hosts         []Destination `yaml:"hosts"`
	InventoryFile string        `yaml:"inventory_file"`
	InventoryURL  string        `yaml:"inventory_url"`
}

// Task defines multiple commands runs together
type Task struct {
	Name     string // name of target, set by config caller
	User     string `yaml:"user"`
	SSHKey   string `yaml:"ssh_key"`
	Commands []Cmd  `yaml:"commands"`
	OnError  string `yaml:"on_error"`
}

// Cmd defines a single command
type Cmd struct {
	Name        string            `yaml:"name"`
	Copy        CopyInternal      `yaml:"copy"`
	Sync        SyncInternal      `yaml:"sync"`
	Delete      DeleteInternal    `yaml:"delete"`
	Wait        WaitInternal      `yaml:"wait"`
	Script      string            `yaml:"script"`
	Environment map[string]string `yaml:"env"`
	Options     struct {
		IgnoreErrors bool `yaml:"ignore_errors"`
		NoAuto       bool `yaml:"no_auto"`
		Local        bool `yaml:"local"`
	} `yaml:"options"`
}

// Destination defines destination info
type Destination struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
}

// CopyInternal defines copy command, implemented internally
type CopyInternal struct {
	Source string `yaml:"src"`
	Dest   string `yaml:"dst"`
	Mkdir  bool   `yaml:"mkdir"`
}

// SyncInternal defines sync command (recursive copy), implemented internally
type SyncInternal struct {
	Source string `yaml:"src"`
	Dest   string `yaml:"dst"`
	Delete bool   `yaml:"delete"`
}

// DeleteInternal defines delete command, implemented internally
type DeleteInternal struct {
	Location  string `yaml:"path"`
	Recursive bool   `yaml:"recur"`
}

// WaitInternal defines wait command, implemented internally
type WaitInternal struct {
	Timeout       time.Duration `yaml:"timeout"`
	CheckDuration time.Duration `yaml:"interval"`
	Command       string        `yaml:"cmd"`
}

// Overrides defines override for task passed from cli
type Overrides struct {
	User          string
	TargetHosts   []string
	InventoryFile string
	InventoryURL  string
	Environment   map[string]string
}

// New makes new config from yml
func New(fname string, overrides *Overrides) (*PlayBook, error) {
	res := &PlayBook{overrides: overrides}
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
	res := Task{}
	if t, ok := p.Tasks[name]; ok {
		if err := copier.Copy(&res, &t); err != nil { // deep copy to avoid side effects
			return nil, fmt.Errorf("can't copy task %s: %w", name, err)
		}
		res.Name = name
		if res.User == "" {
			res.User = p.User // if user not set in task, use default from playbook
		}
		// apply overrides of environment variables, to each script command
		if p.overrides != nil && p.overrides.Environment != nil {
			for envKey, envVal := range p.overrides.Environment {
				for cmdIdx := range res.Commands {
					if res.Commands[cmdIdx].Script == "" {
						continue
					}
					if res.Commands[cmdIdx].Environment == nil {
						res.Commands[cmdIdx].Environment = make(map[string]string)
					}
					res.Commands[cmdIdx].Environment[envKey] = envVal
				}
			}
		}

		return &res, nil
	}
	return nil, fmt.Errorf("task %s not found", name)
}

// TargetHosts returns target hosts for given target name.
// It applies overrides if any set and also retrieves hosts from inventory file or url if any set.
func (p *PlayBook) TargetHosts(name string) ([]Destination, error) {

	loadInventoryFile := func(fname string) ([]Destination, error) {
		fh, err := os.Open(fname) // nolint
		if err != nil {
			return nil, fmt.Errorf("can't open inventory file %s: %w", fname, err)
		}
		defer fh.Close() // nolint
		hosts, err := p.parseInventory(fh)
		if err != nil {
			return nil, fmt.Errorf("can't parse inventory file %s: %w", fname, err)
		}
		return hosts, nil
	}

	loadInventoryURL := func(url string) ([]Destination, error) {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("can't get inventory from http %s: %w", url, err)
		}
		defer resp.Body.Close() // nolint
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("can't get inventory from http %s, status: %s", url, resp.Status)
		}
		hosts, err := p.parseInventory(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("can't parse inventory from http %s: %w", url, err)
		}
		return hosts, nil
	}

	// check if we have override for user, this is the highest priority
	if p.overrides != nil && p.overrides.User != "" {
		p.User = p.overrides.User
	}

	// check if we have overrides for target hosts, this is the highest priority
	if p.overrides != nil && len(p.overrides.TargetHosts) > 0 {
		res := make([]Destination, 0, len(p.overrides.TargetHosts))
		for i := range p.overrides.TargetHosts {
			elems := strings.Split(p.overrides.TargetHosts[i], ":") // get host and port (optional)
			if len(elems) == 1 {
				res = append(res, Destination{Host: elems[0], Port: 22, User: p.User})
			} else {
				port, err := strconv.Atoi(elems[1])
				if err != nil {
					return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
				}
				res = append(res, Destination{Host: elems[0], Port: port, User: p.User})
			}
		}
		return res, nil
	}

	// check if we have overrides for inventory file, this is second priority
	if p.overrides != nil && p.overrides.InventoryFile != "" {
		return loadInventoryFile(p.overrides.InventoryFile)
	}
	// check if we have overrides for inventory http, this is third priority
	if p.overrides != nil && p.overrides.InventoryURL != "" {
		return loadInventoryURL(p.overrides.InventoryURL)
	}

	// no overrides, check if we have target in config
	t, ok := p.Targets[name]
	if !ok {
		// no target, check if it is a host and if so return it as a single host target
		isValidTarget := func(name string) bool {
			if ip := net.ParseIP(name); ip != nil {
				return true
			}
			if strings.Contains(name, ".") || strings.HasPrefix(name, "localhost") {
				return true
			}
			return false
		}(name)

		if isValidTarget {
			if !strings.Contains(name, ":") {
				return []Destination{{Host: name, Port: 22, User: p.User}}, nil // default port is 22 if not set
			}
			elems := strings.Split(name, ":")
			port, err := strconv.Atoi(elems[1])
			if err != nil {
				return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
			}
			return []Destination{{Host: elems[0], Port: port, User: p.User}}, nil // it is a host, sent as ip
		}
		return nil, fmt.Errorf("target %s not found", name)
	}

	// target found, check if it has hosts
	if len(t.Hosts) > 0 {
		res := make([]Destination, len(t.Hosts))
		for i, h := range t.Hosts {
			if h.Port == 0 {
				h.Port = 22 // default port is 22 if not set
			}
			if h.User == "" {
				h.User = p.User // default user is playbook user if not set
			}
			res[i] = h
		}
		return res, nil
	}

	// target has no hosts, check if it has inventory file
	if t.InventoryFile != "" {
		return loadInventoryFile(t.InventoryFile)
	}

	// target has no hosts, check if it has inventory http
	if t.InventoryURL != "" {
		return loadInventoryURL(t.InventoryURL)
	}

	if t.Hosts == nil {
		return nil, fmt.Errorf("target %s has no hosts", name)
	}

	return t.Hosts, nil
}

// parseInventory parses inventory file or url and returns a list of hosts.
// inventory file format is: host1:port1<\n>host2:port2 user<\n>...
// user is optional, if not set, it is assumed to defined in playbook.
func (p *PlayBook) parseInventory(r io.Reader) (res []Destination, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("inventory reader failed: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") { // skip empty lines and comments
			continue
		}
		dest := Destination{User: p.User}
		elems := strings.Split(line, " ")
		if !strings.Contains(elems[0], ":") { // no port defined, use default 22
			dest.Host = elems[0]
			dest.Port = 22
		} else {
			elems := strings.Split(elems[0], ":")
			dest.Host = elems[0]
			port, err := strconv.Atoi(elems[1])
			if err != nil {
				return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
			}
			dest.Port = port
		}
		if len(elems) > 1 {
			dest.User = elems[1]
		}
		res = append(res, dest)
	}
	return res, nil
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
