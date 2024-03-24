package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDestinations(t *testing.T) {
	testCases := []struct {
		name      string
		targets   map[string]Target
		user      string
		inventory *InventoryData
		expected  []Destination
		err       bool
	}{
		{
			name: "empty targets",
			targets: map[string]Target{
				"test": {},
			},
			user:      "user",
			inventory: nil,
			expected:  nil,
			err:       true,
		},

		{
			name: "no matching tags",
			targets: map[string]Target{
				"test": {Tags: []string{"no-match"}},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					"all": {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
				},
			},
			expected: nil,
			err:      true,
		},

		{
			name: "matching tags",
			targets: map[string]Target{
				"test": {Tags: []string{"web"}},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Port: 22, Tags: []string{"web"}},
						{Name: "server2", Host: "192.168.1.2", Port: 2222, Tags: []string{"db"}},
					},
				},
			},
			expected: []Destination{
				{Name: "server1", Host: "192.168.1.1", Port: 22, Tags: []string{"web"}},
			},
			err: false,
		},

		{
			name: "no matching groups",
			targets: map[string]Target{
				"test": {Groups: []string{"no-match"}},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
					"web": {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
					},
					"db": {
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
				},
			},
			expected: nil,
			err:      true,
		},

		{
			name: "matching groups",
			targets: map[string]Target{
				"test": {Groups: []string{"web"}},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Port: 2222, Tags: []string{"web"}},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
					"web": {
						{Name: "server1", Host: "192.168.1.1", Port: 2222, Tags: []string{"web"}},
					},
					"db": {
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
				},
			},
			expected: []Destination{
				{Name: "server1", Host: "192.168.1.1", Port: 2222, Tags: []string{"web"}},
			},
			err: false,
		},

		{
			name: "matching names",
			targets: map[string]Target{
				"test": {Names: []string{"server1"}},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
				},
			},
			expected: []Destination{
				{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
			},
			err: false,
		},

		{
			name: "multi match",
			targets: map[string]Target{
				"test": {
					Hosts:  []Destination{{Name: "host1", Host: "192.168.1.3"}},
					Groups: []string{"web"},
					Tags:   []string{"db"},
				},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}, Port: 2222, User: "user2"},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
					"web": {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}, Port: 2222, User: "user2"},
					},
					"db": {
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
				},
			},
			expected: []Destination{
				{Name: "host1", Host: "192.168.1.3"},
				{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}, Port: 2222, User: "user2"},
				{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
			},
			err: false,
		},

		{
			name: "duplicate hosts",
			targets: map[string]Target{
				"test": {
					Hosts:  []Destination{{Name: "host1", Host: "192.168.1.3"}},
					Groups: []string{"web"},
					Names:  []string{"server1"},
				},
			},
			user: "user",
			inventory: &InventoryData{
				Groups: map[string][]Destination{
					allHostsGrp: {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
					"web": {
						{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
					},
					"db": {
						{Name: "server2", Host: "192.168.1.2", Tags: []string{"db"}},
					},
				},
			},
			expected: []Destination{
				{Name: "host1", Host: "192.168.1.3"},
				{Name: "server1", Host: "192.168.1.1", Tags: []string{"web"}},
			},
			err: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tge := newTargetExtractor(tc.targets, tc.user, tc.inventory)
			res, err := tge.Destinations("test")

			if tc.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, res)
			}
		})
	}
}

func TestHostAddressParsing(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		user     string
		expected Destination
		err      bool
	}{
		{
			name:     "address only, default port",
			input:    "192.168.1.1",
			user:     "user",
			expected: Destination{Host: "192.168.1.1", Name: "192.168.1.1", Port: 22, User: "user"},
			err:      false,
		},
		{
			name:     "user and address only, default port",
			input:    "john@192.168.1.1",
			user:     "user",
			expected: Destination{Host: "192.168.1.1", Name: "192.168.1.1", Port: 22, User: "john"},
			err:      false,
		},
		{
			name:     "port specified",
			input:    "192.168.1.1:2222",
			user:     "user",
			expected: Destination{Host: "192.168.1.1", Name: "192.168.1.1", Port: 2222, User: "user"},
			err:      false,
		},
		{
			name:     "user and port specified",
			input:    "john@192.168.1.1:2222",
			user:     "user",
			expected: Destination{Host: "192.168.1.1", Name: "192.168.1.1", Port: 2222, User: "john"},
			err:      false,
		},
		{
			name:     "invalid port",
			input:    "192.168.1.1:invalid",
			user:     "user",
			expected: Destination{},
			err:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tge := newTargetExtractor(nil, tc.user, &InventoryData{})
			res, err := tge.Destinations(tc.input)

			if tc.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(res))
				assert.Equal(t, tc.expected, res[0])
			}
		})
	}
}
