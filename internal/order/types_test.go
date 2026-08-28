package order

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinalStatuses_ReturnsIndependentCopy(t *testing.T) {
	statuses := FinalStatuses()
	require.Len(t, statuses, 2)
	statuses[0] = StatusNew

	assert.True(t, StatusProcessed.IsFinal())
	assert.True(t, StatusInvalid.IsFinal())
	assert.False(t, StatusNew.IsFinal())
	assert.False(t, StatusProcessing.IsFinal())
	assert.Equal(t, []Status{StatusProcessed, StatusInvalid}, FinalStatuses())
}
