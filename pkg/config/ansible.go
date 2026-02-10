package config

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type ansiblePlay struct {
	Hosts    any             `yaml:"hosts"`
	Become   bool            `yaml:"become"`
	Vars     map[string]any  `yaml:"vars"`
	Tasks    []map[string]any `yaml:"tasks"`
	Handlers []map[string]any `yaml:"handlers"`
}

// NewAnsible creates a new PlayBook instance by loading an Ansible-like playbook.
// Supported subset: hosts, become, tasks, when, register, and a few builtin modules.
func NewAnsible(fname string, overrides *Overrides, secProvider SecretsProvider) (res *PlayBook, err error) {
	log.Printf("[DEBUG] request to load ansible playbook %q", fname)
	res = &PlayBook{
		overrides:       overrides,
		secretsProvider: secProvider,
		inventory:       &InventoryData{Groups: make(map[string][]Destination)},
	}

	data, err := os.ReadFile(fname) // nolint
	if err != nil {
		return nil, err
	}

	var plays []ansiblePlay
	if err = yaml.Unmarshal(data, &plays); err != nil {
		return nil, fmt.Errorf("can't unmarshal ansible yaml playbook %s: %w", fname, err)
	}

	// fill playbook-level overrides
	if overrides != nil {
		res.User = overrides.User
		res.SSHShell = overrides.SSHShell
		res.SSHTempDir = overrides.SSHTempDir
		res.Inventory = overrides.Inventory
	}

	// translate plays to spot tasks
	nameSeen := map[string]int{}
	for pIdx, p := range plays {
		hosts := parseAnsibleHosts(p.Hosts)
		if len(hosts) == 0 {
			return nil, fmt.Errorf("play %d has empty hosts", pIdx+1)
		}

		for tIdx, task := range p.Tasks {
			cmd, tname, err := translateAnsibleTask(task)
			if err != nil {
				return nil, fmt.Errorf("play %d task %d: %w", pIdx+1, tIdx+1, err)
			}

			// ensure unique task names
			if tname == "" {
				tname = fmt.Sprintf("play-%d-task-%d", pIdx+1, tIdx+1)
			}
			if c := nameSeen[tname]; c > 0 {
				nameSeen[tname] = c + 1
				tname = fmt.Sprintf("%s#%d", tname, c+1)
			} else {
				nameSeen[tname] = 1
			}

			// apply play-level become
			if p.Become {
				cmd.Options.Sudo = true
			}

			tsk := Task{
				Name:     tname,
				Commands: []Cmd{cmd},
				Targets:  hosts,
			}

			res.Tasks = append(res.Tasks, tsk)
		}
	}

	if err = res.checkConfig(); err != nil {
		return nil, fmt.Errorf("config %s is invalid: %w", fname, err)
	}

	log.Printf("[INFO] ansible playbook loaded with %d tasks", len(res.Tasks))

	for i, tsk := range res.Tasks {
		for j := range tsk.Commands {
			res.Tasks[i].Commands[j].SSHShell = res.remoteShell()
			res.Tasks[i].Commands[j].SSHTempDir = res.sshTempDir()
			res.Tasks[i].Commands[j].LocalShell = res.localShell()
		}
	}

	// load inventory if set
	inventoryLoc := os.Getenv(inventoryEnv)
	if res.Inventory != "" {
		inventoryLoc = res.Inventory
	}
	if overrides != nil && overrides.Inventory != "" {
		inventoryLoc = overrides.Inventory
	}
	if inventoryLoc != "" {
		log.Printf("[DEBUG] inventory location %q", inventoryLoc)
		res.inventory, err = res.loadInventory(inventoryLoc)
		if err != nil {
			return nil, fmt.Errorf("can't load inventory %s: %w", inventoryLoc, err)
		}
	}
	if len(res.inventory.Groups) > 0 {
		log.Printf("[INFO] inventory loaded with %d hosts", len(res.inventory.Groups[allHostsGrp]))
	} else {
		log.Printf("[INFO] no inventory loaded")
	}

	return res, nil
}

func parseAnsibleHosts(h any) []string {
	if h == nil {
		return nil
	}
	switch v := h.(type) {
	case string:
		parts := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
		res := []string{}
		for _, p := range parts {
			if p == "" {
				continue
			}
			res = append(res, p)
		}
		return res
	case []any:
		res := []string{}
		for _, it := range v {
			if s, ok := it.(string); ok && s != "" {
				res = append(res, s)
			}
		}
		return res
	default:
		return nil
	}
}

func translateAnsibleTask(task map[string]any) (Cmd, string, error) {
	name, _ := task["name"].(string)
	when, _ := task["when"].(string)
	register, _ := task["register"].(string)

	cmd := Cmd{Name: name}
	if when != "" {
		cmd.Condition = translateWhen(when)
	}

	// find module
	var modName string
	var modVal any
	for k, v := range task {
		if strings.Contains(k, "ansible.builtin.") {
			modName = k
			modVal = v
			break
		}
	}
	if modName == "" {
		// allow short module names without prefix
		for k, v := range task {
			switch k {
			case "apt", "stat", "reboot", "dpkg_selections":
				modName = "ansible.builtin." + k
				modVal = v
			}
		}
	}
	if modName == "" {
		return cmd, name, fmt.Errorf("unsupported ansible module in task %q", name)
	}

	switch modName {
	case "ansible.builtin.apt":
		scr := buildAptScript(modVal)
		cmd.Script = scr
	case "ansible.builtin.stat":
		scr, regVar, err := buildStatScript(modVal, register)
		if err != nil {
			return cmd, name, err
		}
		cmd.Script = scr
		if regVar != "" {
			cmd.Register = []string{regVar}
		}
	case "ansible.builtin.reboot":
		cmd.Script = buildRebootScript(modVal)
	case "ansible.builtin.dpkg_selections":
		scr, err := buildDpkgSelectionsScript(modVal)
		if err != nil {
			return cmd, name, err
		}
		cmd.Script = scr
	default:
		return cmd, name, fmt.Errorf("unsupported ansible module %q", modName)
	}

	return cmd, name, nil
}

func buildAptScript(v any) string {
	params := map[string]any{}
	if m, ok := v.(map[string]any); ok {
		params = m
	}
	var buf bytes.Buffer
	if toBool(params["update_cache"]) {
		buf.WriteString("apt-get update\n")
	}
	if up, ok := params["upgrade"].(string); ok && up != "" {
		// use dist-upgrade for 'dist'
		if up == "dist" {
			buf.WriteString("DEBIAN_FRONTEND=noninteractive apt-get -y dist-upgrade\n")
		} else {
			buf.WriteString("DEBIAN_FRONTEND=noninteractive apt-get -y upgrade\n")
		}
	}
	if toBool(params["autoremove"]) {
		buf.WriteString("apt-get -y autoremove\n")
	}
	res := strings.TrimSpace(buf.String())
	if res == "" {
		// no-op, keep command valid
		res = "true"
	}
	return res
}

func buildStatScript(v any, register string) (string, string, error) {
	params, ok := v.(map[string]any)
	if !ok {
		return "", "", fmt.Errorf("stat params invalid")
	}
	path, _ := params["path"].(string)
	if path == "" {
		return "", "", fmt.Errorf("stat.path is required")
	}
	regVar := ""
	if register != "" {
		regVar = register + "_stat_exists"
	}
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("if [ -e %q ]; then\n", path))
	if regVar != "" {
		buf.WriteString(fmt.Sprintf("export %s=true\n", regVar))
	}
	buf.WriteString("else\n")
	if regVar != "" {
		buf.WriteString(fmt.Sprintf("export %s=false\n", regVar))
	}
	buf.WriteString("fi\n")
	return strings.TrimSpace(buf.String()), regVar, nil
}

func buildRebootScript(v any) string {
	_ = v // ignore for now
	return "shutdown -r now"
}

func buildDpkgSelectionsScript(v any) (string, error) {
	params, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("dpkg_selections params invalid")
	}
	name, _ := params["name"].(string)
	selection, _ := params["selection"].(string)
	if name == "" || selection == "" {
		return "", fmt.Errorf("dpkg_selections.name and selection are required")
	}
	return fmt.Sprintf("echo %q | dpkg --set-selections", name+" "+selection), nil
}

func translateWhen(expr string) string {
	expr = strings.TrimSpace(expr)
	// inventory_hostname == 'name'
	reInv := regexp.MustCompile(`^inventory_hostname\s*([!=]=)\s*['\"]([^'\"]+)['\"]$`)
	if m := reInv.FindStringSubmatch(expr); len(m) == 3 {
		op := m[1]
		val := m[2]
		return fmt.Sprintf("[ \"$SPOT_REMOTE_NAME\" %s \"%s\" ]", op, val)
	}

	// <var>.stat.exists [== true|false]
	reStat := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\.stat\.exists(?:\s*==\s*(true|false))?$`)
	if m := reStat.FindStringSubmatch(expr); len(m) >= 2 {
		v := m[1] + "_stat_exists"
		want := "true"
		if len(m) == 3 && m[2] != "" {
			want = m[2]
		}
		return fmt.Sprintf("[ \"$%s\" = \"%s\" ]", v, want)
	}

	// fallback: pass through
	return expr
}

func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true") || t == "1" || strings.EqualFold(t, "yes")
	default:
		return false
	}
}

