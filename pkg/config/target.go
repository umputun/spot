package config

import (
	"fmt"
	"log"
	"strconv"
	"strings"
)

// Target defines hosts to run commands on
type Target struct {
	Name   string        `yaml:"name" toml:"name"`     // name of target
	Hosts  []Destination `yaml:"hosts" toml:"hosts"`   // direct list of hosts to run commands on, no need to use inventory
	Groups []string      `yaml:"groups" toml:"groups"` // list of groups to run commands on, matches to inventory
	Names  []string      `yaml:"names" toml:"names"`   // list of host names to run commands on, matches to inventory
	Tags   []string      `yaml:"tags" toml:"tags"`     // list of tags to run commands on, matches to inventory
}

// Destination defines destination info
type Destination struct {
	Name string   `yaml:"name" toml:"name"`
	Host string   `yaml:"host" toml:"host"`
	Port int      `yaml:"port" toml:"port"`
	User string   `yaml:"user" toml:"user"`
	Tags []string `yaml:"tags" toml:"tags"`
}

type targetService struct {
	data      map[string]Target
	user      string
	inventory *InventoryData
}

func newTargetService(targets map[string]Target, user string, inventory *InventoryData) *targetService {
	return &targetService{data: targets, user: user, inventory: inventory}
}

func (tg *targetService) destinations(name string) (res []Destination, err error) {
	dedup := func(in []Destination) (res []Destination) {
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

	t, ok := tg.data[name] // get target from playbook
	if ok {
		res, err = tg.destinationsFromPlaybook(name, t)
	} else {
		res, err = tg.destinationsFromInventory(name)
	}
	if err != nil {
		return nil, err
	}
	return dedup(res), nil
}

func (tg *targetService) destinationsFromPlaybook(name string, t Target) ([]Destination, error) {
	if len(t.Hosts) == 0 && len(t.Names) == 0 && len(t.Groups) == 0 && len(t.Tags) == 0 {
		return nil, fmt.Errorf("target %q has no hosts, names, tags or groups", t.Name)
	}
	log.Printf("[DEBUG] target %q found in playbook", t.Name)

	res := appendHostsFromTarget(t)
	res = append(res, tg.matchNamesInventory(name, t.Names)...)
	res = append(res, tg.matchGroupsInventory(name, t.Groups)...)
	res = append(res, tg.matchTagsInventory(name, t.Tags)...)

	if len(res) == 0 {
		return nil, fmt.Errorf("hosts for target %q not found", t.Name)
	}
	log.Printf("[DEBUG] target %q has %d total hosts: %+v", t.Name, len(res), res)
	return res, nil
}

func appendHostsFromTarget(t Target) []Destination {
	res := []Destination{}
	if len(t.Hosts) > 0 {
		res = append(res, t.Hosts...)
		log.Printf("[DEBUG] target %q has %d hosts: %+v", t.Name, len(t.Hosts), t.Hosts)
	}
	return res
}

func (tg *targetService) matchNamesInventory(name string, names []string) []Destination {
	res := []Destination{}
	if len(names) == 0 || tg.inventory == nil {
		return res
	}
	for _, n := range names {
		for _, h := range tg.inventory.Groups[allHostsGrp] {
			if strings.EqualFold(h.Name, n) {
				res = append(res, h)
				log.Printf("[DEBUG] target %q found name match %+v", name, h)
				break
			}
		}
	}
	return res
}

func (tg *targetService) matchGroupsInventory(name string, groups []string) []Destination {
	res := []Destination{}
	if len(groups) == 0 || tg.inventory == nil {
		return res
	}
	for _, g := range groups {
		res = append(res, tg.inventory.Groups[g]...)
		log.Printf("[DEBUG] target %q found group match %+v", name, tg.inventory.Groups[g])
	}
	return res
}

func (tg *targetService) matchTagsInventory(name string, tags []string) []Destination {
	res := []Destination{}
	if len(tags) == 0 || tg.inventory == nil {
		return res
	}
	for _, tag := range tags {
		for _, h := range tg.inventory.Groups[allHostsGrp] {
			if len(h.Tags) == 0 {
				continue
			}
			for _, t := range h.Tags {
				if strings.EqualFold(t, tag) {
					res = append(res, h)
					log.Printf("[DEBUG] target %q found tag match %+v", name, h)
				}
			}
		}
	}
	return res
}

func (tg *targetService) destinationsFromInventory(name string) ([]Destination, error) {
	hosts, ok := tg.inventory.Groups[name]
	if ok {
		// the name is a group in inventory, return all hosts in the group
		res := make([]Destination, len(hosts))
		copy(res, hosts)
		log.Printf("[DEBUG] target %q found as group in inventory: %+v", name, res)
		return res, nil
	}

	// match name to tags in inventory
	res := tg.matchTagsInventory(name, []string{name})
	if len(res) > 0 {
		log.Printf("[DEBUG] target %q found as tag in inventory: %+v", name, res)
		return res, nil
	}

	for _, h := range tg.inventory.Groups[allHostsGrp] {
		// match name to names in inventory
		if strings.EqualFold(h.Name, name) {
			log.Printf("[DEBUG] target %q found as name in inventory: %+v", name, h)
			return []Destination{h}, nil
		}
		// match name to hosts in inventory
		if strings.EqualFold(h.Host, name) {
			log.Printf("[DEBUG] target %q found as host in inventory: %+v", name, h)
			return []Destination{h}, nil
		}
	}

	user := tg.user // default user from playbook
	if strings.Contains(name, "@") {
		// user is specified in target host
		elems := strings.Split(name, "@")
		user = elems[0]
		if len(elems) > 1 {
			name = elems[1]
		}
	}

	// check if name looks like host:port
	if strings.Contains(name, ":") {
		elems := strings.Split(name, ":")
		port, err := strconv.Atoi(elems[1])
		if err != nil {
			return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
		}
		log.Printf("[DEBUG] target %q used as host:port %s:%d", name, elems[0], port)
		return []Destination{{Host: elems[0], Port: port, User: user}}, nil
	}

	// we have no idea what this is, use it as host:22
	log.Printf("[DEBUG] target %q used as host:22 %s", name, name)
	return []Destination{{Host: name, Port: 22, User: user}}, nil
}
