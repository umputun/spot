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

func (tg *targetService) destinations(name string) ([]Destination, error) {
	t, ok := tg.data[name] // get target from playbook
	if ok {
		if len(t.Hosts) == 0 && len(t.Names) == 0 && len(t.Groups) == 0 && len(t.Tags) == 0 {
			return nil, fmt.Errorf("target %q has no hosts, names, tags or groups", name)
		}
		log.Printf("[DEBUG] target %q found in playbook", name)
		// we have found target in playbook, process hosts, names and group
		res := []Destination{}

		if len(t.Hosts) > 0 {
			// target has "hosts", use all of them as is
			res = append(res, t.Hosts...)
			log.Printf("[DEBUG] target %q has %d hosts: %+v", name, len(t.Hosts), t.Hosts)
		}

		if len(t.Names) > 0 && tg.inventory != nil {
			// target has "names", match them to "all" group in inventory by name
			for _, n := range t.Names {
				for _, h := range tg.inventory.Groups[allHostsGrp] {
					if strings.EqualFold(h.Name, n) {
						res = append(res, h)
						log.Printf("[DEBUG] target %q found name match %+v", name, h)
						break
					}
				}
			}
		}

		if len(t.Groups) > 0 && tg.inventory != nil {
			// target has "groups", get all hosts from inventory for each group
			for _, g := range t.Groups {
				// we don't set default port and user here, as they are set in inventory already
				res = append(res, tg.inventory.Groups[g]...)
				log.Printf("[DEBUG] target %q found group match %+v", name, tg.inventory.Groups[g])
			}
		}

		if len(t.Tags) > 0 && tg.inventory != nil {
			// target has "tags", get all hosts from inventory for each tag
			for _, tag := range t.Tags {
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
		}

		if len(res) == 0 {
			return nil, fmt.Errorf("hosts for target %q not found", name)
		}
		log.Printf("[DEBUG] target %q has %d total hosts: %+v", name, len(res), res)
		return res, nil
	}

	// target not defined in playbook
	log.Printf("[DEBUG] target %q not found in playbook", name)

	// try first as group in inventory
	hosts, ok := tg.inventory.Groups[name]
	if ok {
		res := make([]Destination, len(hosts))
		copy(res, hosts)
		log.Printf("[DEBUG] target %q found as group in inventory: %+v", name, res)
		return res, nil
	}

	// try as a tag in inventory
	res := []Destination{}
	for _, h := range tg.inventory.Groups[allHostsGrp] {
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
		log.Printf("[DEBUG] target %q found as tag in inventory: %+v", name, res)
		return res, nil
	}

	// try as single host name in inventory
	for _, h := range tg.inventory.Groups[allHostsGrp] {
		if strings.EqualFold(h.Name, name) {
			log.Printf("[DEBUG] target %q found as name in inventory: %+v", name, h)
			return []Destination{h}, nil
		}
	}

	// try as a single host address in inventory
	for _, h := range tg.inventory.Groups[allHostsGrp] {
		if strings.EqualFold(h.Host, name) {
			log.Printf("[DEBUG] target %q found as host in inventory: %+v", name, h)
			return []Destination{h}, nil
		}
	}

	user := tg.user
	// try as single host or host:port or user@host:port
	if strings.Contains(name, "@") { // extract user from name
		elems := strings.Split(name, "@")
		user = elems[0]
		if len(elems) > 1 {
			name = elems[1] // skip user part
		}
	}

	if strings.Contains(name, ":") {
		elems := strings.Split(name, ":")
		port, err := strconv.Atoi(elems[1])
		if err != nil {
			return nil, fmt.Errorf("can't parse port %s: %w", elems[1], err)
		}
		log.Printf("[DEBUG] target %q used as host:port %s:%d", name, elems[0], port)
		return []Destination{{Host: elems[0], Port: port, User: user}}, nil
	}

	// finally we assume it is a host name, with default port 22
	log.Printf("[DEBUG] target %q used as host:22 %s", name, name)
	return []Destination{{Host: name, Port: 22, User: user}}, nil
}
