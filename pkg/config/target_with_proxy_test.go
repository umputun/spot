package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDestinationsWithProxyCommand(t *testing.T) {
	testCases := []struct {
		name               string
		targets            map[string]Target
		proxyCommandParsed []string
		user               string
		inventory          *InventoryData
		expected           []Destination
		err                bool
	}{

		// It looks like test pkg/config/target_test.go is testing deduplication by tge.Destinations().
		// With configured ProxyCommand nothing changed in that aspect, because deduplication is based on Host+Port+User.
		// From that aspect there is nothing to test. Just repeating couple of tests.
		//
		// But there is one special case described below.

		{
			name: "matching tags",
			targets: map[string]Target{
				"test": {Tags: []string{"web"}},
			},
			proxyCommandParsed: nil,
			user:               "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Port: 22, Tags: []string{"web"}, ProxyCommand: "ssh jump1 -W %h:%p"},
						{Name: "server2", Host: "192.168.1.2", Port: 2222, Tags: []string{"db"}, ProxyCommand: "ssh jump2 -W %h:%p"},
					},
				},
			},
			expected: []Destination{
				{Name: "server1", Host: "192.168.1.1", Port: 22, Tags: []string{"web"}, ProxyCommand: "ssh jump1 -W %h:%p"},
			},
			err: false,
		},

		{
			name: "multi match",
			targets: map[string]Target{
				"test": {
					Hosts:  []Destination{{Name: "host1", Host: "192.168.1.3", ProxyCommand: "ssh gateway -W %h:%p"}},
					Groups: []string{"web"},
					Tags:   []string{"db"},
				},
			},
			proxyCommandParsed: nil,
			user:               "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}, Port: 2222, User: "user2", ProxyCommand: "ssh multi-jump -W %h:%p"},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}, ProxyCommand: "ssh multi-db -W %h:%p"},
					},
					"web": {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}, Port: 2222, User: "user2", ProxyCommand: "ssh multi-jump -W %h:%p"},
					},
					"db": {
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}, ProxyCommand: "ssh multi-db -W %h:%p"},
					},
				},
			},
			expected: []Destination{
				{Name: "host1", Host: "192.168.1.3", ProxyCommand: "ssh gateway -W %h:%p"},
				{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}, Port: 2222, User: "user2", ProxyCommand: "ssh multi-jump -W %h:%p"},
				{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}, ProxyCommand: "ssh multi-db -W %h:%p"},
			},
			err: false,
		},

		// If program was started with passing target name as string and targetExtractor.destinationsFromInventory() will not find it in
		// inventory/playbook files, then it will assume that name is the host name and all data related in inventory/playbook files
		// will be ignored. To be able to test that targetExtractor.destinationsFromInventory() will return Destination
		// with passed ProxyCommand this test case added.
		//
		{
			name:               "name not found in inventory or playbook",
			targets:            map[string]Target{},
			proxyCommandParsed: []string{"ssh", "jump1", "-W", "%h:%p"},
			user:               "user",
			inventory:          &InventoryData{},
			expected: []Destination{
				{Name: "test", Host: "test", Port: 22, User: "user", Tags: []string(nil), ProxyCommand: "ssh jump1 -W %h:%p", ProxyCommandParsed: []string{"ssh", "jump1", "-W", "%h:%p"}},
			},
			err: false,
		},

		{
			name:               "name not found in inventory or playbook, proxyCommandParsed is nil",
			targets:            map[string]Target{},
			proxyCommandParsed: nil,
			user:               "user",
			inventory:          &InventoryData{},
			expected: []Destination{
				{Name: "test", Host: "test", Port: 22, User: "user", Tags: []string(nil), ProxyCommand: "", ProxyCommandParsed: []string(nil)},
			},
			err: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tge := newTargetExtractor(tc.targets, tc.user, tc.inventory)
			res, err := tge.Destinations("test", tc.proxyCommandParsed)

			if tc.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, res)
			}
		})
	}
}
