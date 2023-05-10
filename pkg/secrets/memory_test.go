package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryProvider_Get(t *testing.T) {
	m := NewMemoryProvider(map[string]string{"sec1": "val1", "sec2": "val2"})

	t.Run("get existing secret", func(t *testing.T) {
		val, err := m.Get("sec1")
		assert.NoError(t, err)
		assert.Equal(t, val, "val1")
	})

	t.Run("get non-existing secret", func(t *testing.T) {
		_, err := m.Get("sec3")
		assert.Error(t, err)
	})
}
