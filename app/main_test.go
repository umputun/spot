package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/simplotask/config"
)

func Test_sshUserAndKey(t *testing.T) {
	testCases := []struct {
		name         string
		opts         options
		conf         config.PlayBook
		expectedUser string
		expectedKey  string
	}{
		{
			name: "All defaults",
			opts: options{},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "default_key",
				Tasks:  map[string]config.Task{},
			},
			expectedUser: "default_user",
			expectedKey:  "default_key",
		},
		{
			name: "Task config overrides user",
			opts: options{
				TaskName: "test_task",
			},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "default_key",
				Tasks: map[string]config.Task{
					"test_task": {User: "task_user"},
				},
			},
			expectedUser: "task_user",
			expectedKey:  "default_key",
		},
		{
			name: "Command line overrides all",
			opts: options{
				TaskName: "test_task",
				SSHUser:  "cmd_user",
				SSHKey:   "cmd_key",
			},
			conf: config.PlayBook{
				User:   "default_user",
				SSHKey: "default_key",
				Tasks: map[string]config.Task{
					"test_task": {User: "task_user"},
				},
			},
			expectedUser: "cmd_user",
			expectedKey:  "cmd_key",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			user, key := sshUserAndKey(tc.opts, &tc.conf)
			assert.Equal(t, tc.expectedUser, user, "user should match expected user")
			assert.Equal(t, tc.expectedKey, key, "key should match expected key")
		})
	}
}
