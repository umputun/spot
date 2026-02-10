package config

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type ansiblePlay struct {
	Hosts       any              `yaml:"hosts"`
	Become      bool             `yaml:"become"`
	Vars        map[string]any   `yaml:"vars"`
	Tasks       []map[string]any `yaml:"tasks"`
	Handlers    []map[string]any `yaml:"handlers"`
	GatherFacts bool             `yaml:"gather_facts"`
}

// NewAnsible creates a new PlayBook instance by loading an Ansible-like playbook.
// Supported subset: hosts, become, tasks, when, register, vars, handlers and a set of builtin modules.
func NewAnsible(fname string, overrides *Overrides, secProvider SecretsProvider) (res *PlayBook, err error) {
	log.Printf("[DEBUG] request to load ansible playbook %q", fname)
	res = &PlayBook{
		overrides:       overrides,
		secretsProvider: secProvider,
		inventory:       &InventoryData{Groups: make(map[string][]Destination)},
	}
	res.Ansible = true

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

	// load ansible inventory (ini format)
	invPath := ""
	if overrides != nil && overrides.Inventory != "" {
		invPath = overrides.Inventory
	}
	if invPath == "" {
		invPath = os.Getenv(inventoryEnv)
	}
	ansInv, invHosts, invErr := loadAnsibleInventory(invPath)
	if invErr == nil && ansInv != nil {
		res.inventory = ansInv
	}

	// load host_vars from playbook directory
	baseDir := filepath.Dir(fname)
	hostVars := loadHostVars(filepath.Join(baseDir, "host_vars"))

	// translate plays to spot tasks
	nameSeen := map[string]int{}
	for pIdx, p := range plays {
		hostRefs := parseAnsibleHosts(p.Hosts)
		if len(hostRefs) == 0 {
			return nil, fmt.Errorf("play %d has empty hosts", pIdx+1)
		}

		// resolve host refs to actual hostnames
		expandedHosts := resolveHosts(hostRefs, invHosts)
		if len(expandedHosts) == 0 {
			// fallback to raw refs
			expandedHosts = hostRefs
		}

		for _, hostName := range expandedHosts {
			vars := mergeVars(p.Vars, hostVars[hostName])
			vars["inventory_hostname"] = hostName
			vars["__playbook_dir"] = baseDir

			for tIdx, task := range p.Tasks {
				cmds, tname, err := translateAnsibleTask(task, vars)
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
					for i := range cmds {
						cmds[i].Options.Sudo = true
					}
				}

				tsk := Task{
					Name:     tname,
					Commands: cmds,
					Targets:  []string{hostName},
				}

				res.Tasks = append(res.Tasks, tsk)
			}
		}

		// handlers for this play
		handlerCmds := map[string]Cmd{}
		for hIdx, h := range p.Handlers {
			cmds, hname, err := translateAnsibleTask(h, p.Vars)
			if err != nil {
				return nil, fmt.Errorf("play %d handler %d: %w", pIdx+1, hIdx+1, err)
			}
			if hname == "" {
				hname = fmt.Sprintf("play-%d-handler-%d", pIdx+1, hIdx+1)
			}
			if len(cmds) > 0 {
				handlerCmds[hname] = cmds[0]
			}
		}
		for hname, hcmd := range handlerCmds {
			if p.Become {
				hcmd.Options.Sudo = true
			}
			hcmd.Condition = notifyCondition(hname)
			res.Tasks = append(res.Tasks, Task{
				Name:     "handler:" + hname,
				Commands: []Cmd{hcmd},
				Targets:  hostRefs,
			})
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

	if invErr == nil && ansInv != nil {
		log.Printf("[INFO] inventory loaded with %d hosts", len(res.inventory.Groups[allHostsGrp]))
	} else if invPath != "" && invErr != nil {
		log.Printf("[WARN] can't load ansible inventory %s: %v", invPath, invErr)
	}

	return res, nil
}

// ----- inventory -----

func loadAnsibleInventory(path string) (*InventoryData, map[string][]string, error) {
	if path == "" {
		return nil, nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	inv := &InventoryData{Groups: make(map[string][]Destination)}
	inv.Groups[allHostsGrp] = []Destination{}
	hostsByGroup := map[string][]string{}
	var currentGroup string

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentGroup = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		// host line
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		host := parts[0]
		user := ""
		port := 22
		for _, p := range parts[1:] {
			if strings.HasPrefix(p, "ansible_user=") {
				user = strings.TrimPrefix(p, "ansible_user=")
			}
			if strings.HasPrefix(p, "ansible_port=") {
				fmt.Sscanf(strings.TrimPrefix(p, "ansible_port="), "%d", &port)
			}
		}
		d := Destination{Name: host, Host: host, Port: port, User: user}
		if currentGroup != "" {
			inv.Groups[currentGroup] = append(inv.Groups[currentGroup], d)
			hostsByGroup[currentGroup] = append(hostsByGroup[currentGroup], host)
		}
		inv.Groups[allHostsGrp] = append(inv.Groups[allHostsGrp], d)
		if _, ok := hostsByGroup[allHostsGrp]; !ok {
			hostsByGroup[allHostsGrp] = []string{}
		}
		hostsByGroup[allHostsGrp] = append(hostsByGroup[allHostsGrp], host)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return inv, hostsByGroup, nil
}

func loadHostVars(dir string) map[string]map[string]any {
	res := map[string]map[string]any{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !(strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		vars := map[string]any{}
		if err := yaml.Unmarshal(data, &vars); err != nil {
			continue
		}
		host := strings.TrimSuffix(strings.TrimSuffix(name, ".yml"), ".yaml")
		res[host] = vars
	}
	return res
}

func resolveHosts(refs []string, inv map[string][]string) []string {
	res := []string{}
	for _, r := range refs {
		if inv != nil {
			if hs, ok := inv[r]; ok {
				res = append(res, hs...)
				continue
			}
		}
		res = append(res, r)
	}
	return dedup(res)
}

func dedup(in []string) []string {
	m := map[string]struct{}{}
	out := []string{}
	for _, s := range in {
		if _, ok := m[s]; ok {
			continue
		}
		m[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// ----- task translation -----

func translateAnsibleTask(task map[string]any, vars map[string]any) ([]Cmd, string, error) {
	name, _ := task["name"].(string)
	when, _ := task["when"].(string)
	register, _ := task["register"].(string)
	changedWhen, _ := task["changed_when"].(string)
	notify := parseNotify(task["notify"])
	loopItems, loopVar := parseLoop(task, vars)
	envVars := parseEnvironment(task["environment"], vars)
	baseEnv := varsToEnv(vars)

	if len(loopItems) == 0 {
		loopItems = []any{nil}
	}
	if loopVar == "" {
		loopVar = "item"
	}

	cmds := []Cmd{}
	for _, it := range loopItems {
		localVars := mergeVars(vars, map[string]any{})
		if it != nil {
			localVars[loopVar] = it
		}
		cmd, err := translateModule(task, localVars, register, changedWhen, notify)
		if err != nil {
			return nil, name, err
		}
		cmd.Name = name
		if when != "" {
			cmd.Condition = translateWhen(when)
		}
		if len(baseEnv) > 0 || len(envVars) > 0 {
			cmd.Environment = mergeEnv(baseEnv, envVars)
		}
		cmds = append(cmds, cmd)
		// for non-script commands, add a notify marker command
		if cmd.Script == "" && len(notify) > 0 {
			nc := Cmd{
				Name:        name + ":notify",
				Script:      notifyScript(notify),
				Condition:   cmd.Condition,
				Options:     cmd.Options,
				Environment: cmd.Environment,
			}
			cmds = append(cmds, nc)
		}
	}
	return cmds, name, nil
}

func translateModule(task map[string]any, vars map[string]any, register, changedWhen string, notify []string) (Cmd, error) {
	cmd := Cmd{}
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
		for k, v := range task {
			switch k {
			case "apt", "stat", "reboot", "dpkg_selections", "shell", "command", "file", "copy", "template", "unarchive", "user", "systemd", "service", "iptables":
				modName = "ansible.builtin." + k
				modVal = v
			}
		}
	}
	if modName == "" {
		return cmd, fmt.Errorf("unsupported ansible module")
	}

	switch modName {
	case "ansible.builtin.apt":
		cmd.Script = buildAptScript(modVal, vars)
	case "ansible.builtin.stat":
		scr, regVar, err := buildStatScript(modVal, register)
		if err != nil {
			return cmd, err
		}
		cmd.Script = scr
		if regVar != "" {
			cmd.Register = []string{regVar}
		}
	case "ansible.builtin.reboot":
		cmd.Script = buildRebootScript(modVal)
	case "ansible.builtin.dpkg_selections":
		scr, err := buildDpkgSelectionsScript(modVal, vars)
		if err != nil {
			return cmd, err
		}
		cmd.Script = scr
	case "ansible.builtin.shell":
		cmd.Script = renderString(anyToString(modVal), vars)
	case "ansible.builtin.command":
		cmd.Script = renderString(commandString(modVal), vars)
	case "ansible.builtin.file":
		cmd.Script = buildFileScript(modVal, vars)
	case "ansible.builtin.copy":
		return buildCopyCmd(modVal, vars)
	case "ansible.builtin.template":
		return buildTemplateCmd(modVal, vars)
	case "ansible.builtin.unarchive":
		cmd.Script = buildUnarchiveScript(modVal, vars)
	case "ansible.builtin.user":
		cmd.Script = buildUserScript(modVal, vars)
	case "ansible.builtin.systemd", "ansible.builtin.service":
		cmd.Script = buildServiceScript(modVal, vars)
	case "ansible.builtin.iptables":
		cmd.Script = buildIptablesScript(modVal, vars)
	default:
		return cmd, fmt.Errorf("unsupported ansible module %q", modName)
	}

	cmd.Script = injectNotify(cmd.Script, notify, changedWhen, register)
	return cmd, nil
}

// ----- helpers -----

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

func mergeVars(a, b map[string]any) map[string]any {
	res := map[string]any{}
	for k, v := range a {
		res[k] = v
	}
	for k, v := range b {
		res[k] = v
	}
	return res
}

func parseNotify(v any) []string {
	res := []string{}
	switch t := v.(type) {
	case string:
		res = append(res, t)
	case []any:
		for _, it := range t {
			if s, ok := it.(string); ok {
				res = append(res, s)
			}
		}
	}
	return res
}

func parseLoop(task map[string]any, vars map[string]any) ([]any, string) {
	loopVal, ok := task["loop"]
	if !ok {
		return nil, ""
	}
	loopVar := "item"
	if lc, ok := task["loop_control"].(map[string]any); ok {
		if v, ok := lc["loop_var"].(string); ok && v != "" {
			loopVar = v
		}
	}

	switch v := loopVal.(type) {
	case []any:
		return v, loopVar
	case string:
		// allow {{ var }} that points to a list
		re := regexp.MustCompile(`^\{\{\s*([a-zA-Z0-9_]+)\s*\}\}$`)
		if m := re.FindStringSubmatch(v); len(m) == 2 {
			if lst, ok := vars[m[1]]; ok {
				if res, ok := toAnySlice(lst); ok {
					return res, loopVar
				}
			}
		}
		return []any{v}, loopVar
	default:
		return nil, loopVar
	}
}

func parseEnvironment(v any, vars map[string]any) map[string]string {
	res := map[string]string{}
	m, ok := v.(map[string]any)
	if !ok {
		return res
	}
	for k, val := range m {
		res[k] = renderString(anyToString(val), vars)
	}
	return res
}

func varsToEnv(vars map[string]any) map[string]string {
	res := map[string]string{}
	for k, v := range vars {
		switch t := v.(type) {
		case string:
			res[k] = t
		case bool:
			if t {
				res[k] = "true"
			} else {
				res[k] = "false"
			}
		case int, int64, float64:
			res[k] = fmt.Sprintf("%v", t)
		}
	}
	return res
}

func mergeEnv(a, b map[string]string) map[string]string {
	res := map[string]string{}
	for k, v := range a {
		res[k] = v
	}
	for k, v := range b {
		res[k] = v
	}
	return res
}

func toAnySlice(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, 0, len(t))
		for _, s := range t {
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

func renderString(s string, vars map[string]any) string {
	re := regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}"))
		if v, ok := vars[key]; ok {
			return anyToString(v)
		}
		return m
	})
}

func commandString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]any); ok {
		if c, ok := m["cmd"].(string); ok {
			return c
		}
	}
	return ""
}

func notifyVarName(h string) string {
	s := strings.ToUpper(h)
	s = regexp.MustCompile(`[^A-Z0-9]+`).ReplaceAllString(s, "_")
	return "SPOT_NOTIFY_" + s + "_{SPOT_REMOTE_NAME_SAN}"
}

func notifyCondition(h string) string {
	return fmt.Sprintf("[ \"$%s\" = \"true\" ]", notifyVarName(h))
}

func notifyScript(notify []string) string {
	var buf bytes.Buffer
	for _, h := range notify {
		buf.WriteString(fmt.Sprintf("export %s=true\n", notifyVarName(h)))
	}
	return strings.TrimSpace(buf.String())
}

func injectNotify(script string, notify []string, changedWhen, register string) string {
	if len(notify) == 0 {
		return script
	}
	if script == "" {
		return script
	}
	// only support simple changed_when: '<text>' in <register>.stdout
	if changedWhen != "" && register != "" {
		re := regexp.MustCompile(`^['\"](.+?)['\"]\s+in\s+` + regexp.QuoteMeta(register) + `\.stdout$`)
		if m := re.FindStringSubmatch(changedWhen); len(m) == 2 {
			needle := m[1]
			var buf bytes.Buffer
			buf.WriteString("out=$(" + script + ")\n")
			buf.WriteString("echo \"$out\"\n")
			buf.WriteString(fmt.Sprintf("echo \"$out\" | grep -Fq %q && ", needle))
			for i, h := range notify {
				if i > 0 {
					buf.WriteString("; ")
				}
				buf.WriteString(fmt.Sprintf("export %s=true", notifyVarName(h)))
			}
			buf.WriteString("\n")
			return buf.String()
		}
	}
	// default: always notify after script
	var buf bytes.Buffer
	buf.WriteString(script)
	buf.WriteString("\n")
	for _, h := range notify {
		buf.WriteString(fmt.Sprintf("export %s=true\n", notifyVarName(h)))
	}
	return buf.String()
}

// ----- module builders -----

func buildAptScript(v any, vars map[string]any) string {
	params := map[string]any{}
	if m, ok := v.(map[string]any); ok {
		params = m
	}
	var buf bytes.Buffer
	if toBool(params["update_cache"]) {
		buf.WriteString("apt-get update\n")
	}
	if name, ok := params["name"].(string); ok && name != "" {
		name = renderString(name, vars)
		state, _ := params["state"].(string)
		if state == "absent" {
			buf.WriteString("DEBIAN_FRONTEND=noninteractive apt-get -y remove " + name + "\n")
		} else {
			buf.WriteString("DEBIAN_FRONTEND=noninteractive apt-get -y install " + name + "\n")
		}
	}
	if up, ok := params["upgrade"].(string); ok && up != "" {
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
	_ = v
	return "shutdown -r now"
}

func buildDpkgSelectionsScript(v any, vars map[string]any) (string, error) {
	params, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("dpkg_selections params invalid")
	}
	name, _ := params["name"].(string)
	selection, _ := params["selection"].(string)
	if name == "" || selection == "" {
		return "", fmt.Errorf("dpkg_selections.name and selection are required")
	}
	name = renderString(name, vars)
	selection = renderString(selection, vars)
	return fmt.Sprintf("echo %q | dpkg --set-selections", name+" "+selection), nil
}

func buildFileScript(v any, vars map[string]any) string {
	params, _ := v.(map[string]any)
	path := renderString(anyToString(params["path"]), vars)
	state, _ := params["state"].(string)
	owner := anyToString(params["owner"])
	group := anyToString(params["group"])
	mode := anyToString(params["mode"])
	var buf bytes.Buffer
	if state == "directory" {
		buf.WriteString(fmt.Sprintf("mkdir -p %q\n", path))
	}
	if owner != "" || group != "" {
		buf.WriteString(fmt.Sprintf("chown %s:%s %q\n", owner, group, path))
	}
	if mode != "" {
		buf.WriteString(fmt.Sprintf("chmod %s %q\n", mode, path))
	}
	return strings.TrimSpace(buf.String())
}

func buildCopyCmd(v any, vars map[string]any) (Cmd, error) {
	params, _ := v.(map[string]any)
	src := renderString(anyToString(params["src"]), vars)
	dst := renderString(anyToString(params["dest"]), vars)
	remoteSrc := toBool(params["remote_src"])
	owner := anyToString(params["owner"])
	group := anyToString(params["group"])
	mode := anyToString(params["mode"])
	if remoteSrc {
		var buf bytes.Buffer
		buf.WriteString(fmt.Sprintf("cp %q %q\n", src, dst))
		if owner != "" || group != "" {
			buf.WriteString(fmt.Sprintf("chown %s:%s %q\n", owner, group, dst))
		}
		if mode != "" {
			buf.WriteString(fmt.Sprintf("chmod %s %q\n", mode, dst))
		}
		return Cmd{Script: strings.TrimSpace(buf.String())}, nil
	}
	if !filepath.IsAbs(src) {
		if pb, ok := vars["__playbook_dir"].(string); ok && pb != "" {
			src = filepath.Join(pb, src)
		}
	}
	return Cmd{Copy: CopyInternal{Source: src, Dest: dst}}, nil
}

func buildTemplateCmd(v any, vars map[string]any) (Cmd, error) {
	params, _ := v.(map[string]any)
	src := anyToString(params["src"])
	dst := renderString(anyToString(params["dest"]), vars)
	owner := anyToString(params["owner"])
	group := anyToString(params["group"])
	mode := anyToString(params["mode"])

	if !filepath.IsAbs(src) {
		if pb, ok := vars["__playbook_dir"].(string); ok && pb != "" {
			src = filepath.Join(pb, src)
		}
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return Cmd{}, fmt.Errorf("template src not found: %w", err)
	}
	rendered := renderString(string(data), vars)
	tmp, err := os.CreateTemp("", "spot-tpl-")
	if err != nil {
		return Cmd{}, err
	}
	if _, err := tmp.WriteString(rendered); err != nil {
		return Cmd{}, err
	}
	_ = tmp.Close()

	cmd := Cmd{Copy: CopyInternal{Source: tmp.Name(), Dest: dst}}
	// ownership and mode after copy
	var buf bytes.Buffer
	if owner != "" || group != "" {
		buf.WriteString(fmt.Sprintf("chown %s:%s %q\n", owner, group, dst))
	}
	if mode != "" {
		buf.WriteString(fmt.Sprintf("chmod %s %q\n", mode, dst))
	}
	if buf.Len() > 0 {
		cmd.OnExit = strings.TrimSpace(buf.String())
	}
	return cmd, nil
}

func buildUnarchiveScript(v any, vars map[string]any) string {
	params, _ := v.(map[string]any)
	src := renderString(anyToString(params["src"]), vars)
	dst := renderString(anyToString(params["dest"]), vars)
	creates := renderString(anyToString(params["creates"]), vars)
	extra := ""
	if arr, ok := params["extra_opts"].([]any); ok {
		opts := []string{}
		for _, it := range arr {
			opts = append(opts, anyToString(it))
		}
		extra = strings.Join(opts, " ")
	}
	var buf bytes.Buffer
	if creates != "" {
		buf.WriteString(fmt.Sprintf("[ -e %q ] || ", creates))
	}
	buf.WriteString(fmt.Sprintf("tmp=\"/tmp/spot-archive-$$.tar.gz\"; curl -fsSL %q -o \"$tmp\"; tar -xzf \"$tmp\" -C %q %s; rm -f \"$tmp\"", src, dst, extra))
	return buf.String()
}

func buildUserScript(v any, vars map[string]any) string {
	params, _ := v.(map[string]any)
	name := renderString(anyToString(params["name"]), vars)
	shell := renderString(anyToString(params["shell"]), vars)
	createHome := toBool(params["create_home"])
	system := toBool(params["system"])
	args := []string{"useradd"}
	if system {
		args = append(args, "--system")
	}
	if !createHome {
		args = append(args, "--no-create-home")
	}
	if shell != "" {
		args = append(args, "--shell", shell)
	}
	args = append(args, name)
	return strings.Join(args, " ")
}

func buildServiceScript(v any, vars map[string]any) string {
	params, _ := v.(map[string]any)
	name := renderString(anyToString(params["name"]), vars)
	state := renderString(anyToString(params["state"]), vars)
	daemonReload := toBool(params["daemon_reload"])
	enabled := toBool(params["enabled"])
	var buf bytes.Buffer
	if daemonReload {
		buf.WriteString("systemctl daemon-reload\n")
	}
	if enabled {
		buf.WriteString(fmt.Sprintf("systemctl enable %s\n", name))
	}
	switch state {
	case "started", "start":
		buf.WriteString(fmt.Sprintf("systemctl start %s\n", name))
	case "restarted", "restart":
		buf.WriteString(fmt.Sprintf("systemctl restart %s\n", name))
	case "reloaded", "reload":
		buf.WriteString(fmt.Sprintf("systemctl reload %s\n", name))
	case "stopped", "stop":
		buf.WriteString(fmt.Sprintf("systemctl stop %s\n", name))
	default:
		if state != "" {
			buf.WriteString(fmt.Sprintf("systemctl %s %s\n", state, name))
		}
	}
	return strings.TrimSpace(buf.String())
}

func buildIptablesScript(v any, vars map[string]any) string {
	params, _ := v.(map[string]any)
	chain := anyToString(params["chain"])
	proto := anyToString(params["protocol"])
	port := anyToString(params["destination_port"])
	ctstate := anyToString(params["ctstate"])
	jump := anyToString(params["jump"])
	cmd := []string{"iptables", "-A", chain}
	if proto != "" {
		cmd = append(cmd, "-p", proto)
	}
	if port != "" {
		cmd = append(cmd, "--dport", port)
	}
	if ctstate != "" {
		cmd = append(cmd, "-m", "conntrack", "--ctstate", ctstate)
	}
	if jump != "" {
		cmd = append(cmd, "-j", jump)
	}
	return strings.Join(cmd, " ")
}

// ----- when -----

func translateWhen(expr string) string {
	expr = strings.TrimSpace(expr)
	// split simple AND
	if strings.Contains(expr, " and ") {
		parts := strings.Split(expr, " and ")
		conds := []string{}
		for _, p := range parts {
			conds = append(conds, translateWhen(p))
		}
		return strings.Join(conds, " && ")
	}

	// inventory_hostname == 'name'
	reInv := regexp.MustCompile(`^inventory_hostname\s*([!=]=)\s*['\"]([^'\"]+)['\"]$`)
	if m := reInv.FindStringSubmatch(expr); len(m) == 3 {
		op := m[1]
		val := m[2]
		return fmt.Sprintf("[ \"$SPOT_REMOTE_NAME\" %s \"%s\" ]", op, val)
	}

	// var is defined / var is not defined
	reDef := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s+is\s+(not\s+)?defined$`)
	if m := reDef.FindStringSubmatch(expr); len(m) >= 2 {
		v := m[1]
		not := strings.TrimSpace(m[2]) != ""
		if not {
			return fmt.Sprintf("[ -z \"$%s\" ]", v)
		}
		return fmt.Sprintf("[ -n \"$%s\" ]", v)
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

	// "text" not in var
	reNotIn := regexp.MustCompile(`^['\"](.+?)['\"]\s+not\s+in\s+([A-Za-z_][A-Za-z0-9_]*)$`)
	if m := reNotIn.FindStringSubmatch(expr); len(m) == 3 {
		needle := m[1]
		v := m[2]
		return fmt.Sprintf("case \"$%s\" in *%s*) false ;; *) true ;; esac", v, needle)
	}

	// "text" in var
	reIn := regexp.MustCompile(`^['\"](.+?)['\"]\s+in\s+([A-Za-z_][A-Za-z0-9_]*)$`)
	if m := reIn.FindStringSubmatch(expr); len(m) == 3 {
		needle := m[1]
		v := m[2]
		return fmt.Sprintf("case \"$%s\" in *%s*) true ;; *) false ;; esac", v, needle)
	}

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
