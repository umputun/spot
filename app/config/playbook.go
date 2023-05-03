package config

import (
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

	"github.com/hashicorp/go-multierror"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/umputun/simplotask/app/config/deepcopy"
)

// PlayBook defines top-level config object
type PlayBook struct {
	User      string            `yaml:"user" toml:"user"`           // ssh user
	SSHKey    string            `yaml:"ssh_key" toml:"ssh_key"`     // ssh key
	Inventory string            `yaml:"inventory" toml:"inventory"` // inventory file or url
	Targets   map[string]Target `yaml:"targets" toml:"targets"`     // list of targets/environments
	Tasks     []Task            `yaml:"tasks" toml:"tasks"`         // list of tasks

	inventory *InventoryData // loaded inventory
	overrides *Overrides     // overrides passed from cli
}

// SimplePlayBook defines simplified top-level config
// It is used for unmarshalling only, and result used to make the usual PlayBook
type SimplePlayBook struct {
	User      string   `yaml:"user" toml:"user"`           // ssh user
	SSHKey    string   `yaml:"ssh_key" toml:"ssh_key"`     // ssh key
	Inventory string   `yaml:"inventory" toml:"inventory"` // inventory file or url
	Targets   []string `yaml:"targets" toml:"targets"`     // list of names
	Task      []Cmd    `yaml:"task" toml:"task"`           // single task is a list of commands
}

// Target defines hosts to run commands on
type Target struct {
	Name   string        `yaml:"name" toml:"name"`     // name of target
	Hosts  []Destination `yaml:"hosts" toml:"hosts"`   // direct list of hosts to run commands on, no need to use inventory
	Groups []string      `yaml:"groups" toml:"groups"` // list of groups to run commands on, matches to inventory
	Names  []string      `yaml:"names" toml:"names"`   // list of host names to run commands on, matches to inventory
	Tags   []string      `yaml:"tags" toml:"tags"`     // list of tags to run commands on, matches to inventory
}

// Task defines multiple commands runs together
type Task struct {
	Name     string `yaml:"name" toml:"name"` // name of target, set by config caller
	User     string `yaml:"user" toml:"user"`
	SSHKey   string `yaml:"ssh_key" toml:"ssh_key"`
	Commands []Cmd  `yaml:"commands" toml:"commands"`
	OnError  string `yaml:"on_error" toml:"on_error"`
}

// Destination defines destination info
type Destination struct {
	Name string   `yaml:"name" toml:"name"`
	Host string   `yaml:"host" toml:"host"`
	Port int      `yaml:"port" toml:"port"`
	User string   `yaml:"user" toml:"user"`
	Tags []string `yaml:"tags" toml:"tags"`
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
	Groups map[string][]Destination `yaml:"groups" toml:"groups"`
	Hosts  []Destination            `yaml:"hosts" toml:"hosts"`
}

const allHostsGrp = "all"

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
			inventoryLoc := os.Getenv("SPOT_INVENTORY") // default inventory location from env
			if overrides.Inventory != "" {
				inventoryLoc = overrides.Inventory // inventory set in cli overrides
			}
			if inventoryLoc != "" { // load inventory if set in cli or env
				res.inventory, err = res.loadInventory(inventoryLoc)
				if err != nil {
					return nil, fmt.Errorf("can't load inventory %s: %w", overrides.Inventory, err)
				}
			}
			return res, nil
		}
		return nil, fmt.Errorf("can't read config %s: %w", fname, err)
	}

	if err = unmarshalPlaybookFile(fname, data, res); err != nil {
		return nil, fmt.Errorf("can't unmarshal config: %w", err)
	}

	if err = res.checkConfig(); err != nil {
		return nil, fmt.Errorf("config %s is invalid: %w", fname, err)
	}

	// log loaded config info
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
		log.Printf("[INFO] inventory loaded with %d hosts", len(res.inventory.Groups[allHostsGrp]))
	}

	return res, nil
}

// unmarshalPlaybookFile is trying to parse playbook from the data bytes.
// It will try to guess format by file extension or use yaml as toml.
// First it will try to unmarshal to a complete PlayBook struct, if it fails,
// it will try to unmarshal to a SimplePlayBook struct and convert it to a complete PlayBook struct.
func unmarshalPlaybookFile(fname string, data []byte, res *PlayBook) (err error) {

	unmarshal := func(data []byte, v interface{}) error {
		// try to unmarshal yml first and then toml
		switch {
		case strings.HasSuffix(fname, ".yml") || strings.HasSuffix(fname, ".yaml") || !strings.Contains(fname, "."):
			if err = yaml.Unmarshal(data, v); err != nil {
				return fmt.Errorf("can't unmarshal config %s: %w", fname, err)
			}
		case strings.HasSuffix(fname, ".toml"):
			if err = toml.Unmarshal(data, v); err != nil {
				return fmt.Errorf("can't unmarshal config %s: %w", fname, err)
			}
		default:
			return fmt.Errorf("unknown config format %s", fname)
		}
		return nil
	}

	splitIPAddress := func(inp string) (string, int) {
		host, portStr, e := net.SplitHostPort(inp)
		if e != nil {
			return inp, 22
		}
		port, e := strconv.Atoi(portStr)
		if e != nil {
			return host, 22
		}
		return host, port
	}

	errors := new(multierror.Error)
	if err = unmarshal(data, res); err == nil {
		return nil // success, this is full PlayBook config
	}
	errors = multierror.Append(errors, err)

	simple := &SimplePlayBook{}
	if err = unmarshal(data, simple); err == nil && len(simple.Task) > 0 {
		// success, this is SimplePlayBook config, convert it to full PlayBook config
		res.Inventory = simple.Inventory
		res.Tasks = []Task{{Commands: simple.Task}} // simple playbook has just a list of commands as the task
		res.Tasks[0].Name = "default"               // we have only one task, set it as default
		target := Target{Names: simple.Targets}     // set as names to match inventory
		for _, t := range simple.Targets {
			ip, port := splitIPAddress(t)
			target.Hosts = append(target.Hosts, Destination{Host: ip, Port: port}) // also set as hosts
		}
		res.Targets = map[string]Target{"default": target}
		return nil
	}

	return multierror.Append(errors, err).Unwrap()
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
// After it gets destinations from targetHosts(name) it applies overrides of user, set default port 22 if needed
// and deduplicate results.
func (p *PlayBook) TargetHosts(name string) ([]Destination, error) {

	dedup := func(in []Destination) []Destination {
		var res []Destination
		seen := make(map[string]struct{})
		for _, d := range in {
			key := d.Host + ":" + strconv.Itoa(d.Port) + ":" + d.User
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				res = append(res, d)
			}
		}
		return res
	}

	userOverride := func(u string) string {
		// apply overrides of user
		if p.overrides != nil && p.overrides.User != "" {
			return p.overrides.User
		}
		// no overrides, use user from target if set
		if u != "" {
			return u
		}
		// no overrides, no user in target, use default from playbook
		return p.User
	}

	res, err := p.targetHosts(name)
	if err != nil {
		return nil, err
	}

	for i, h := range res {
		if h.Port == 0 {
			h.Port = 22 // default port is 22 if not set
		}
		h.User = userOverride(h.User)
		res[i] = h
	}

	return dedup(res), nil
}

// targetHosts returns target hosts for given target name.
// The result is not deduplicated and not modified with overrides.
func (p *PlayBook) targetHosts(name string) ([]Destination, error) {
	t, ok := p.Targets[name] // get target from playbook
	if ok {
		if len(t.Hosts) == 0 && len(t.Names) == 0 && len(t.Groups) == 0 && len(t.Tags) == 0 {
			return nil, fmt.Errorf("target %q has no hosts, names, tags or groups", name)
		}
		// we have found target in playbook, process hosts, names and group
		res := []Destination{}

		if len(t.Hosts) > 0 {
			// target has "hosts", use all of them as is
			res = append(res, t.Hosts...)
		}

		if len(t.Names) > 0 && p.inventory != nil {
			// target has "names", match them to "all" group in inventory by name
			for _, n := range t.Names {
				for _, h := range p.inventory.Groups[allHostsGrp] {
					if strings.EqualFold(h.Name, n) {
						res = append(res, h)
						break
					}
				}
			}
		}

		if len(t.Groups) > 0 && p.inventory != nil {
			// target has "groups", get all hosts from inventory for each group
			for _, g := range t.Groups {
				// we don't set default port and user here, as they are set in inventory already
				res = append(res, p.inventory.Groups[g]...)
			}
		}

		if len(t.Tags) > 0 && p.inventory != nil {
			// target has "tags", get all hosts from inventory for each tag
			for _, tag := range t.Tags {
				for _, h := range p.inventory.Groups[allHostsGrp] {
					if len(h.Tags) == 0 {
						continue
					}
					for _, t := range h.Tags {
						if strings.EqualFold(t, tag) {
							res = append(res, h)
						}
					}
				}
			}
		}

		if len(res) == 0 {
			return nil, fmt.Errorf("hosts for target %q not found", name)
		}

		return res, nil
	}

	// target not defined in playbook

	// try first as group in inventory
	hosts, ok := p.inventory.Groups[name]
	if ok {
		res := make([]Destination, len(hosts))
		copy(res, hosts)
		return res, nil
	}

	// try as a tag in inventory
	res := []Destination{}
	for _, h := range p.inventory.Groups[allHostsGrp] {
		if len(h.Tags) == 0 {
			continue
		}
		for _, t := range h.Tags {
			if strings.EqualFold(t, name) {
				res = append(res, h)
			}
		}
	}
	if len(res) > 0 {
		return res, nil
	}

	// try as single host name in inventory
	for _, h := range p.inventory.Groups[allHostsGrp] {
		if strings.EqualFold(h.Name, name) {
			return []Destination{h}, nil
		}
	}

	// try as a single host address in inventory
	for _, h := range p.inventory.Groups[allHostsGrp] {
		if strings.EqualFold(h.Host, name) {
			return []Destination{h}, nil
		}
	}

	// try as single host or host:port
	if strings.Contains(name, ":") {
		elems := strings.Split(name, ":")
		port, err := strconv.Atoi(elems[1])
		if err != nil {
			return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
		}
		return []Destination{{Host: elems[0], Port: port, User: p.User}}, nil
	}

	// finally we assume it is a host name, with default port 22
	return []Destination{{Host: name, Port: 22, User: p.User}}, nil
}

// loadInventoryFile loads inventory from file or url and returns a struct with groups.
// Hosts, if presented, are loaded to the group "all". All the other groups are loaded to "all"
// as well and also to their own group.
func (p *PlayBook) loadInventory(loc string) (*InventoryData, error) {

	reader := func(loc string) (r io.ReadCloser, err error) {
		// get reader for inventory file or url
		switch {
		case strings.HasPrefix(loc, "http"): // location is a url
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(loc)
			if err != nil {
				return nil, fmt.Errorf("can't get inventory from http %s: %w", loc, err)
			}
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("can't get inventory from http %s, status: %s", loc, resp.Status)
			}
			return resp.Body, nil
		default: // location is a file
			f, err := os.Open(loc) // nolint
			if err != nil {
				return nil, fmt.Errorf("can't open inventory file %s: %w", loc, err)
			}
			return f, nil
		}
	}

	rdr, err := reader(loc) // inventory ReadCloser, has to be closed
	if err != nil {
		return nil, err
	}
	defer rdr.Close() // nolint

	var data InventoryData
	if !strings.HasSuffix(loc, ".toml") {
		// we assume it is yaml. Can't do strict check, as we can have urls without any extension
		if err = yaml.NewDecoder(rdr).Decode(&data); err != nil {
			return nil, fmt.Errorf("can't parse inventory %s: %w", loc, err)
		}
	} else {
		if err = toml.NewDecoder(rdr).Decode(&data); err != nil {
			return nil, fmt.Errorf("can't parse inventory %s: %w", loc, err)
		}
	}

	if len(data.Groups[allHostsGrp]) > 0 {
		return nil, fmt.Errorf("group %q is reserved for all hosts", allHostsGrp)
	}

	if len(data.Groups) > 0 {
		// create group "all" with all hosts from all groups
		data.Groups[allHostsGrp] = []Destination{}
		for key, g := range data.Groups {
			if key == "all" {
				continue
			}
			data.Groups[allHostsGrp] = append(data.Groups[allHostsGrp], g...)
		}
	}
	if len(data.Hosts) > 0 {
		// add hosts to group "all"
		if data.Groups == nil {
			data.Groups = make(map[string][]Destination)
		}
		if _, ok := data.Groups[allHostsGrp]; !ok {
			data.Groups[allHostsGrp] = []Destination{}
		}
		data.Groups[allHostsGrp] = append(data.Groups[allHostsGrp], data.Hosts...)
	}
	// sort hosts in group "all" by host name, for predictable order in tests and in the processing
	sort.Slice(data.Groups[allHostsGrp], func(i, j int) bool {
		return data.Groups[allHostsGrp][i].Host < data.Groups[allHostsGrp][j].Host
	})

	// set default port and user if not set for all inventory groups
	for _, gr := range data.Groups {
		for i := range gr {
			if gr[i].Port == 0 {
				gr[i].Port = 22 // default port is 22 if not set
			}
			if gr[i].User == "" {
				gr[i].User = p.User // default user is playbook's user or override, if not set by inventory
			}
		}
	}

	return &data, nil
}

// checkConfig checks validity of config
func (p *PlayBook) checkConfig() error {
	names := make(map[string]bool)
	for i, t := range p.Tasks {
		if t.Name == "" { // task name is required
			log.Printf("[WARN] missing name for task #%d", i)
			return fmt.Errorf("task name is required")
		}
		if names[t.Name] { // task name must be unique
			log.Printf("[WARN] duplicate task name %q", t.Name)
			return fmt.Errorf("duplicate task name %q", t.Name)
		}
		names[t.Name] = true
	}
	return nil
}
