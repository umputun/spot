package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	c, err := New("testdata/f1.yml")
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")
	assert.Equal(t, "umputun", c.User, "user")

	tsk := c.Tasks["deploy-remark42"]
	assert.Equal(t, 5, len(tsk.Commands), "5 commands")
}

func TestPlayBook_Task(t *testing.T) {
	c, err := New("testdata/f1.yml")
	require.NoError(t, err)

	t.Run("not-found", func(t *testing.T) {
		_, err = c.Task("no-such-task")
		assert.EqualError(t, err, "task no-such-task not found")
	})

	t.Run("found", func(t *testing.T) {
		tsk, err := c.Task("deploy-remark42")
		require.NoError(t, err)
		assert.Equal(t, 5, len(tsk.Commands))
	})
}

func TestCmd_GetScript(t *testing.T) {
	c, err := New("testdata/f1.yml")
	require.NoError(t, err)
	t.Logf("%+v", c)
	assert.Equal(t, 1, len(c.Tasks), "single task")

	t.Run("script", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[3]
		assert.Equal(t, "git", cmd.Name, "name")
		res := cmd.GetScript()
		assert.Equal(t, `sh -c "git clone https://example.com/remark42.git /srv || true; cd /srv; git pull`, res)
	})

	t.Run("no-script", func(t *testing.T) {
		cmd := c.Tasks["deploy-remark42"].Commands[1]
		assert.Equal(t, "copy configuration", cmd.Name)
		res := cmd.GetScript()
		assert.Equal(t, "", res)
	})
}
