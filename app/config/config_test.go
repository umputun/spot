package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")
	assert.Equal(t, "umputun", c.User, "user")

	tsk := c.Tasks["deploy-remark42"]
	assert.Equal(t, 5, len(tsk.Commands), "5 commands")
}

func TestPlayBook_Task(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
	require.NoError(t, err)

	t.Run("not-found", func(t *testing.T) {
		_, err = c.Task("no-such-task")
		assert.EqualError(t, err, "task no-such-task not found")
	})

	t.Run("found", func(t *testing.T) {
		tsk, err := c.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
		assert.Equal(t, "deploy-remark42", tsk.Name)
	})
}

func TestCmd_GetScript(t *testing.T) {
	c, err := New("testdata/f1.yml", nil)
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")

	t.Run("script", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[3]
		assert.Equal(t, "git", cmd.Name, "name")
		res := cmd.GetScript()
		assert.Equal(t, `sh -c "git clone https://example.com/remark42.git /srv || true; cd /srv; git pull"`, res)
	})

	t.Run("no-script", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[1]
		assert.Equal(t, "copy configuration", cmd.Name)
		res := cmd.GetScript()
		assert.Equal(t, "", res)
	})

	t.Run("script with env", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[4]
		assert.Equal(t, "docker", cmd.Name)
		res := cmd.GetScript()
		assert.Equal(t, `sh -c "BAR=qux FOO=bar docker pull umputun/remark42:latest; docker stop remark42 || true; docker rm remark42 || true; docker run -d --name remark42 -p 8080:8080 umputun/remark42:latest"`, res)
	})
}

func TestPlayBook_TargetHosts(t *testing.T) {
	tbl := []struct {
		name          string
		targets       map[string]Target
		input         string
		expectedHosts []string
		expectedError error
	}{
		{
			name: "existing target",
			targets: map[string]Target{
				"web": {
					Hosts: []string{"10.0.0.1", "10.0.0.2:2222"},
				},
			},
			input:         "web",
			expectedHosts: []string{"10.0.0.1:22", "10.0.0.2:2222"},
			expectedError: nil,
		},
		{
			name: "host IP",
			targets: map[string]Target{
				"web": {
					Hosts: []string{"10.0.0.1", "10.0.0.2"},
				},
			},
			input:         "192.168.1.1",
			expectedHosts: []string{"192.168.1.1:22"},
			expectedError: nil,
		},
		{
			name: "host IP with port",
			targets: map[string]Target{
				"web": {
					Hosts: []string{"10.0.0.1", "10.0.0.2:2222"},
				},
			},
			input:         "192.168.1.1:2222",
			expectedHosts: []string{"192.168.1.1:2222"},
			expectedError: nil,
		},
		{
			name: "valid FQDN",
			targets: map[string]Target{
				"web": {
					Hosts: []string{"10.0.0.1", "10.0.0.2"},
				},
			},
			input:         "www.example.com",
			expectedHosts: []string{"www.example.com:22"},
			expectedError: nil,
		},
		{
			name: "invalid target or host",
			targets: map[string]Target{
				"web": {
					Hosts: []string{"10.0.0.1", "10.0.0.2"},
				},
			},
			input:         "invalid",
			expectedHosts: nil,
			expectedError: fmt.Errorf("target invalid not found"),
		},
		{
			name: "invalid IP address or FQDN",
			targets: map[string]Target{
				"web": {
					Hosts: []string{"10.0.0.1", "10.0.0.2"},
				},
			},
			input:         "invalidhost",
			expectedHosts: nil,
			expectedError: fmt.Errorf("target invalidhost not found"),
		},
	}

	for _, tc := range tbl {
		t.Run(tc.name, func(t *testing.T) {
			p := &PlayBook{Targets: tc.targets}
			actualHosts, actualError := p.TargetHosts(tc.input)
			assert.Equal(t, tc.expectedHosts, actualHosts, tc.name)
			assert.Equal(t, tc.expectedError, actualError, tc.name)
		})
	}
}

func TestPlayBook_TargetHostsOverrides(t *testing.T) {

	t.Run("override hosts directly", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{TargetHosts: []string{"h1", "h2"}})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []string{"h1", "h2"}, res)
	})

	t.Run("override hosts with file", func(t *testing.T) {
		c, err := New("testdata/f1.yml", &Overrides{InventoryFile: "testdata/hosts"})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []string{"hh1.example.com:22", "h2.example.com:2233"}, res)
	})

	t.Run("override hosts with http", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("h1.example.com:2223\nh2.example.com"))
			require.NoError(t, err)
		}))
		defer ts.Close()
		c, err := New("testdata/f1.yml", &Overrides{InventoryURL: ts.URL})
		require.NoError(t, err)
		res, err := c.TargetHosts("blah")
		require.NoError(t, err)
		assert.Equal(t, []string{"h1.example.com:2223", "h2.example.com:22"}, res)
	})
}
