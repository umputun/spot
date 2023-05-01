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

	"gopkg.in/yaml.v3"

	"github.com/umputun/simplotask/app/config/deepcopy"
)

// PlayBook defines top-level config yaml
type PlayBook struct {
	User    string            `yaml:"user"`
	SSHKey  string            `yaml:"ssh_key"`
	Targets map[string]Target `yaml:"targets"`
	Tasks   []Task            `yaml:"tasks"`

	overrides *Overrides
}

// Target defines hosts to run commands on
type Target struct {
	Name          string        `yaml:"name"`
	Hosts         []Destination `yaml:"hosts"`
	InventoryFile Inventory     `yaml:"inventory_file"`
	InventoryURL  Inventory     `yaml:"inventory_url"`
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
	Name string `yaml:"name"`
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
	FilterHosts   []string
	InventoryFile string
	InventoryURL  string
	Environment   map[string]string
}

// Inventory defines external inventory file or url
type Inventory struct {
	Groups   []string `yaml:"groups"`
	Location string   `yaml:"location"`
}

// InventoryData defines inventory data format
type InventoryData struct {
	Groups map[string][]Destination `yaml:"groups"`
	Hosts  []Destination            `yaml:"hosts"`
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

	names := make(map[string]bool)
	for i, t := range res.Tasks {
		if t.Name == "" {
			log.Printf("[WARN] missing name for task #%d", i)
			return nil, fmt.Errorf("task name is required")
		}
		if names[t.Name] {
			log.Printf("[WARN] duplicate task name %q", t.Name)
			return nil, fmt.Errorf("duplicate task name %q", t.Name)
		}
		names[t.Name] = true
	}

	log.Printf("[INFO] playbook loaded with %d tasks", len(res.Tasks))
	for _, tsk := range res.Tasks {
		for _, c := range tsk.Commands {
			log.Printf("[DEBUG] load task %s command %s", tsk.Name, c.Name)
		}
	}
	return res, nil
}

// Task returns task by name
func (p *PlayBook) Task(name string) (*Task, error) {
	searchTask := func(tsk []Task, name string) (*Task, error) {
		for _, t := range tsk {
			if strings.EqualFold(t.Name, name) {
				return &t, nil
			}
		}
		return nil, fmt.Errorf("task %q not found", name)
	}

	t, err := searchTask(p.Tasks, name)
	if err != nil {
		return nil, err
	}

	cp := deepcopy.Copy(t) // deep copy to avoid side effects of overrides on original config
	res, ok := cp.(*Task)
	if !ok {
		return nil, fmt.Errorf("can't copy task %s", name)
	}
	res.Name = name
	if res.User == "" {
		res.User = p.User // if user not set in task, use default from playbook
	}

	// apply overrides of user
	if p.overrides != nil && p.overrides.User != "" {
		res.User = p.overrides.User
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

	return res, nil
}

// TargetHosts returns target hosts for given target name.
// It applies overrides if any set and also retrieves hosts from inventory file or url if any set.
func (p *PlayBook) TargetHosts(name string) ([]Destination, error) {

	loadInventoryFile := func(fname string, grs []string) ([]Destination, error) {
		fh, err := os.Open(fname) // nolint
		if err != nil {
			return nil, fmt.Errorf("can't open inventory file %s: %w", fname, err)
		}
		defer fh.Close() // nolint
		hosts, err := p.parseInventory(fh, grs)
		if err != nil {
			return nil, fmt.Errorf("can't parse inventory file %s: %w", fname, err)
		}
		return hosts, nil
	}

	loadInventoryURL := func(url string, grs []string) ([]Destination, error) {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("can't get inventory from http %s: %w", url, err)
		}
		defer resp.Body.Close() // nolint
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("can't get inventory from http %s, status: %s", url, resp.Status)
		}
		hosts, err := p.parseInventory(resp.Body, grs)
		if err != nil {
			return nil, fmt.Errorf("can't parse inventory from http %s: %w", url, err)
		}
		return hosts, nil
	}

	user := p.User // default user from playbook
	if p.overrides != nil && p.overrides.User != "" {
		user = p.overrides.User // override user if set
	}

	// check if we have overrides for inventory file, this is second priority
	if p.overrides != nil && p.overrides.InventoryFile != "" {
		return loadInventoryFile(p.overrides.InventoryFile, nil)
	}
	// check if we have overrides for inventory http, this is third priority
	if p.overrides != nil && p.overrides.InventoryURL != "" {
		return loadInventoryURL(p.overrides.InventoryURL, nil)
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
				return []Destination{{Host: name, Port: 22, User: user}}, nil // default port is 22 if not set
			}
			elems := strings.Split(name, ":")
			port, err := strconv.Atoi(elems[1])
			if err != nil {
				return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
			}
			return []Destination{{Host: elems[0], Port: port, User: user}}, nil // it is a host, sent as ip
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
				h.User = user // default user is playbook's user or override, if not set
			}
			res[i] = h
		}
		return p.filterHosts(res, p.overrides), nil
	}

	// target has no hosts, check if it has inventory file
	if t.InventoryFile.Location != "" {
		res, err := loadInventoryFile(t.InventoryFile.Location, t.InventoryFile.Groups)
		if err != nil {
			return nil, fmt.Errorf("can't load inventory file %s: %w", t.InventoryFile.Location, err)
		}
		return p.filterHosts(res, p.overrides), nil
	}

	// target has no hosts, check if it has inventory http
	if t.InventoryURL.Location != "" {
		res, err := loadInventoryURL(t.InventoryURL.Location, t.InventoryFile.Groups)
		if err != nil {
			return nil, fmt.Errorf("can't load inventory http %s: %w", t.InventoryURL.Location, err)
		}
		return p.filterHosts(res, p.overrides), nil
	}

	if t.Hosts == nil {
		return nil, fmt.Errorf("target %s has no hosts", name)
	}

	return p.filterHosts(t.Hosts, p.overrides), nil
}

// filterHosts filters hosts by host name first and if not matched, by address.
func (p *PlayBook) filterHosts(inp []Destination, overrides *Overrides) []Destination {
	if overrides == nil || len(overrides.FilterHosts) == 0 { // no filter, return all
		return inp
	}
	filter := overrides.FilterHosts
	res := []Destination{}
	matchedNames := map[string]bool{} // map of matched names

	// first filter by name
	for _, h := range inp {
		for _, f := range filter {
			if h.Name == f {
				res = append(res, h)
				matchedNames[h.Name] = true
			}
		}
	}

	// then filter by address
	for _, h := range inp {
		if matchedNames[h.Name] {
			continue // already matched by name, skip
		}
		for _, f := range filter {
			if h.Host == f {
				res = append(res, h)
			}
		}
	}

	return res
}

// parseAddress parses address in format host:port and returns host and port.
func (p *PlayBook) parseAddress(addr string) (host string, port int, err error) {
	if !strings.Contains(addr, ":") {
		return addr, 22, nil // default port is 22 if not set
	}
	elems := strings.Split(addr, ":")
	port, err = strconv.Atoi(elems[1])
	if err != nil {
		return "", 0, fmt.Errorf("can't parse port %s: %w", elems[1], err)
	}
	return elems[0], port, nil
}

// parseInventory parses inventory yml file or url and returns a list of hosts for the specified group.
// user is optional, if not set, it is assumed to be defined in playbook. name is optional too.
func (p *PlayBook) parseInventory(r io.Reader, groups []string) ([]Destination, error) {
	contains := func(s []string, e string) bool {
		if len(s) == 0 {
			return true
		}
		for _, a := range s {
			if a == e {
				return true
			}
		}
		return false
	}

	var data InventoryData
	if err := yaml.NewDecoder(r).Decode(&data); err != nil {
		return nil, fmt.Errorf("inventory decoder failed: %w", err)
	}

	if len(data.Hosts) == 0 && len(data.Groups) == 0 {
		return nil, fmt.Errorf("no hosts or groups defined in inventory")
	}

	res := []Destination{}
	if len(data.Hosts) > 0 { // hosts defined directly, use them
		res = append(res, data.Hosts...)
	}

	if len(data.Hosts) == 0 { // no hosts defined directly, use groups
		for grName, hosts := range data.Groups {
			if !contains(groups, grName) {
				continue
			}
			res = append(res, hosts...)
		}
	}

	sort.Slice(res, func(i, j int) bool { return res[i].Host < res[j].Host })
	for i, h := range res {
		if h.Port == 0 {
			res[i].Port = 22 // default port is 22 if not set
		}
		if h.User == "" {
			res[i].User = p.User // default user is playbook's user or override, if not set in inventory
		}
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
