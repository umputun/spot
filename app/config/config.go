package config

import (
	"bytes"
	"fmt"
	"io"
	"log"
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
	User      string            `yaml:"user"`      // ssh user
	SSHKey    string            `yaml:"ssh_key"`   // ssh key
	Inventory string            `yaml:"inventory"` // inventory file or url
	Targets   map[string]Target `yaml:"targets"`   // list of targets/environments
	Tasks     []Task            `yaml:"tasks"`     // list of tasks

	inventory *InventoryData // loaded inventory
	overrides *Overrides     // overrides passed from cli
}

// Target defines hosts to run commands on
type Target struct {
	Name   string        `yaml:"name"`
	Hosts  []Destination `yaml:"hosts"`  // direct list of hosts to run commands on, no need to use inventory
	Groups []string      `yaml:"groups"` // list of groups to run commands on, matches to inventory
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
	User         string
	Inventory    string
	Environment  map[string]string
	AdHocCommand string
}

// InventoryData defines inventory data format
type InventoryData struct {
	Groups map[string][]Destination `yaml:"groups"`
	Hosts  []Destination            `yaml:"hosts"`
}

// New makes new config from yml
func New(fname string, overrides *Overrides) (res *PlayBook, err error) {
	res = &PlayBook{
		overrides: overrides,
		inventory: &InventoryData{Groups: make(map[string][]Destination)},
	}

	// load playbook
	data, err := os.ReadFile(fname) // nolint
	if err != nil {
		if overrides != nil && overrides.AdHocCommand != "" {
			// no config file but adhoc set, just return empty config with overrides
			if overrides.Inventory != "" { // load inventory if set in cli
				res.inventory, err = res.loadInventory(overrides.Inventory)
				if err != nil {
					return nil, fmt.Errorf("can't load inventory %s: %w", overrides.Inventory, err)
				}
			}
			return res, nil
		}
		return nil, fmt.Errorf("can't read config %s: %w", fname, err)
	}

	if err = yaml.Unmarshal(data, res); err != nil {
		return nil, fmt.Errorf("can't unmarshal config %s: %w", fname, err)
	}

	if err = res.checkConfig(); err != nil {
		return nil, fmt.Errorf("config %s is invalid: %w", fname, err)
	}

	log.Printf("[INFO] playbook loaded with %d tasks", len(res.Tasks))
	for _, tsk := range res.Tasks {
		for _, c := range tsk.Commands {
			log.Printf("[DEBUG] load task %s command %s", tsk.Name, c.Name)
		}
	}

	// load inventory if set
	inventoryLoc := os.Getenv("SPOT_INVENTORY") // default inventory location from env
	if res.Inventory != "" {
		inventoryLoc = res.Inventory // inventory set in playbook
	}
	if overrides != nil && overrides.Inventory != "" {
		inventoryLoc = overrides.Inventory // inventory set in cli overrides
	}
	if inventoryLoc != "" { // load inventory if set. if not set, assume direct hosts in targets are used
		res.inventory, err = res.loadInventory(inventoryLoc)
		if err != nil {
			return nil, fmt.Errorf("can't load inventory %s: %w", inventoryLoc, err)
		}
	}
	if len(res.inventory.Groups) > 0 { // even with hosts only it will make a group "all"
		log.Printf("[INFO] inventory loaded with %d hosts", len(res.inventory.Groups["all"]))
	}

	return res, nil
}

// Task returns task by name
func (p *PlayBook) Task(name string) (*Task, error) {
	searchTask := func(tsk []Task, name string) (*Task, error) {
		if name == "ad-hoc" && p.overrides.AdHocCommand != "" {
			// special case for ad-hoc command, make a fake task with a single command from overrides.AdHocCommand
			return &Task{Name: "ad-hoc", Commands: []Cmd{{Name: "ad-hoc", Script: p.overrides.AdHocCommand}}}, nil
		}
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

	userOverride := func(u string) string {
		if p.overrides != nil && p.overrides.User != "" {
			return p.overrides.User
		}
		if u != "" {
			return u
		}
		return p.User
	}

	t, ok := p.Targets[name] // get target from playbook
	if ok {
		// we have found target in playbook, check it is host or group
		if len(t.Hosts) > 0 {
			// target is hosts
			res := make([]Destination, len(t.Hosts))
			for i, h := range t.Hosts {
				if h.Port == 0 {
					h.Port = 22 // default port is 22 if not set
				}
				h.User = userOverride(h.User)
				res[i] = h
			}
			return res, nil
		}
		if len(t.Groups) > 0 {
			// target is group, get hosts from inventory
			if p.inventory == nil {
				return nil, fmt.Errorf("inventory is not loaded")
			}
			res := make([]Destination, 0)
			for _, g := range t.Groups {
				// we don't set default port and user here, as they are set in inventory already
				res = append(res, p.inventory.Groups[g]...)
			}
			return res, nil
		}
		return nil, fmt.Errorf("target %q has no hosts or groups", name)
	}

	// target not found in playbook

	// try first as group in inventory
	hosts, ok := p.inventory.Groups[name]
	if ok {
		res := make([]Destination, len(hosts))
		copy(res, hosts)
		for i, r := range res {
			r.User = userOverride(r.User)
			res[i] = r
		}
		return res, nil
	}

	// try as single host name in inventory
	for _, h := range p.inventory.Groups["all"] {
		if strings.EqualFold(h.Name, name) {
			res := []Destination{h}
			res[0].User = userOverride(h.User)
			return res, nil
		}
	}

	// try as a single host address in inventory
	for _, h := range p.inventory.Groups["all"] {
		if strings.EqualFold(h.Host, name) {
			res := []Destination{h}
			res[0].User = userOverride(h.User)
			return res, nil
		}
	}

	// try as single host or host:port
	if strings.Contains(name, ":") {
		elems := strings.Split(name, ":")
		port, err := strconv.Atoi(elems[1])
		if err != nil {
			return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
		}
		return []Destination{{Host: elems[0], Port: port, User: userOverride("")}}, nil
	}

	// we assume it is a host name, with default port 22
	return []Destination{{Host: name, Port: 22, User: userOverride("")}}, nil
}

// loadInventoryFile loads inventory from file and returns a struct with groups.
// Hosts, if presented, are loaded to the group "all". All the other groups are loaded to "all"
// as well and also to their own group.
func (p *PlayBook) loadInventory(loc string) (*InventoryData, error) {

	// get reader for inventory file or url
	var rdr io.Reader
	if strings.HasPrefix(loc, "http") { // location is a url
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(loc)
		if err != nil {
			return nil, fmt.Errorf("can't get inventory from http %s: %w", loc, err)
		}
		defer resp.Body.Close() // nolint
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("can't get inventory from http %s, status: %s", loc, resp.Status)
		}
		rdr = resp.Body
	} else { // location is a file
		f, err := os.Open(loc) // nolint
		if err != nil {
			return nil, fmt.Errorf("can't open inventory file %s: %w", loc, err)
		}
		defer f.Close() // nolint
		rdr = f
	}

	var data InventoryData
	if err := yaml.NewDecoder(rdr).Decode(&data); err != nil {
		return nil, fmt.Errorf("inventory decoder failed: %w", err)
	}

	if len(data.Groups) > 0 {
		// create group "all" with all hosts from all groups
		data.Groups["all"] = []Destination{}
		for key, g := range data.Groups {
			if key == "all" {
				continue
			}
			data.Groups["all"] = append(data.Groups["all"], g...)
		}
	}
	if len(data.Hosts) > 0 {
		// add hosts to group "all"
		if data.Groups == nil {
			data.Groups = make(map[string][]Destination)
		}
		if _, ok := data.Groups["all"]; !ok {
			data.Groups["all"] = []Destination{}
		}
		data.Groups["all"] = append(data.Groups["all"], data.Hosts...)
	}
	sort.Slice(data.Groups["all"], func(i, j int) bool {
		return data.Groups["all"][i].Host < data.Groups["all"][j].Host
	})

	// set default port and user if not set for inventory groups
	// note: we don't care about hosts anymore, they are used only for parsing and are not used in the playbook directly
	for _, gr := range data.Groups {
		for i := range gr {
			if gr[i].Port == 0 {
				gr[i].Port = 22 // default port is 22 if not set
			}
			if gr[i].User == "" {
				gr[i].User = p.User // default user is playbook's user or override, if not set
			}
		}
	}

	return &data, nil
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

// checkConfig checks validity of config
func (p *PlayBook) checkConfig() error {
	names := make(map[string]bool)
	for i, t := range p.Tasks {
		if t.Name == "" {
			log.Printf("[WARN] missing name for task #%d", i)
			return fmt.Errorf("task name is required")
		}
		if names[t.Name] {
			log.Printf("[WARN] duplicate task name %q", t.Name)
			return fmt.Errorf("duplicate task name %q", t.Name)
		}
		names[t.Name] = true
	}
	return nil
}
