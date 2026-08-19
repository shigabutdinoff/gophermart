package apiconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLimitsLeaveHumaTheFirstBoundary(t *testing.T) {
	const limit = 1 << 20

	assert.Equal(t, int64(limit+1), MaxBodyBytes(limit))
	assert.Greater(t, RouterLimit(limit), MaxBodyBytes(limit))
}
