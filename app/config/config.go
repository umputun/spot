package config

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

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
	Hosts         []string `yaml:"hosts"`
	InventoryFile string   `yaml:"inventory_file"`
	InventoryURL  string   `yaml:"inventory_url"`
}

// Task defines multiple commands runs together
type Task struct {
	User     string `yaml:"user"`
	SSHKey   string `yaml:"ssh_key"`
	Commands []Cmd  `yaml:"commands"`
}

// Cmd defines a single command
type Cmd struct {
	Name        string            `yaml:"name"`
	Copy        CopyInternal      `yaml:"copy"`
	Sync        SyncInternal      `yaml:"sync"`
	Delete      DeleteInternal    `yaml:"delete"`
	Script      string            `yaml:"script"`
	Environment map[string]string `yaml:"env"`
	Options     struct {
		IgnoreErrors bool `yaml:"ignore_errors"`
		NoAuto       bool `yaml:"no_auto"`
		Local        bool `yaml:"local"`
	} `yaml:"options"`
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
	Location  string `yaml:"loc"`
	Recursive bool   `yaml:"recur"`
}

// Overrides defines override for task passed from cli
type Overrides struct {
	TargetHosts   []string
	InventoryFile string
	InventoryURL  string
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
	if t, ok := p.Tasks[name]; ok {
		return &t, nil
	}
	return nil, fmt.Errorf("task %s not found", name)
}

// TargetHosts returns target hosts for given target name.
// It applies overrides if any set and also retrieves hosts from inventory file or url if any set.
func (p *PlayBook) TargetHosts(name string) ([]string, error) {

	loadInventoryFile := func(fname string) ([]string, error) {
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

	loadInventoryURL := func(url string) ([]string, error) {
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

	// check if we have overrides for target hosts, this is the highest priority
	if p.overrides != nil && len(p.overrides.TargetHosts) > 0 {
		return p.overrides.TargetHosts, nil
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
		if ip := net.ParseIP(name); ip != nil {
			if !strings.Contains(name, ":") {
				name += ":22"
			}
			return []string{name}, nil // it is a host, sent as ip
		}
		if strings.Contains(name, ".") || strings.Contains(name, "localhost") {
			if !strings.Contains(name, ":") {
				name += ":22"
			}
			return []string{name}, nil // is a valid FQDN
		}
		return nil, fmt.Errorf("target %s not found", name)
	}

	// target found, check if it has hosts
	if len(t.Hosts) > 0 {
		res := make([]string, len(t.Hosts))
		for i, h := range t.Hosts {
			if !strings.Contains(h, ":") {
				h += ":22"
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

	return t.Hosts, nil
}

func (p *PlayBook) parseInventory(r io.Reader) (res []string, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("inventory reader failed: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, ":") {
			line += ":22"
		}
		res = append(res, line)
	}
	return res, nil
}

// GetScript concatenates all script line in commands into one a string to be executed by shell.
// Empty string is returned if no script is defined.
func (cmd *Cmd) GetScript() string {
	if cmd.Script == "" {
		return ""
	}

	envs := make([]string, 0, len(cmd.Environment))
	for k, v := range cmd.Environment {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i] < envs[j] })

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
