package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShutdownBudget_SharesOneDeadlineBetweenParts(t *testing.T) {
	timeout := 200 * time.Millisecond
	budget := newShutdownBudget(timeout)
	t.Cleanup(budget.release)

	startedAt := time.Now()
	httpCtx := budget.httpContext()
	httpDeadline, ok := httpCtx.Deadline()
	require.True(t, ok)
	globalCtx := budget.context()
	globalDeadline, ok := globalCtx.Deadline()

	require.True(t, ok)
	assert.Equal(t, timeout/2, globalDeadline.Sub(httpDeadline))
	assert.WithinDuration(t, startedAt.Add(timeout), globalDeadline, 20*time.Millisecond)
	assert.Same(t, globalCtx, budget.context(), "очередь получает тот же абсолютный срок")

	time.Sleep(30 * time.Millisecond)
	reusedDeadline, ok := budget.context().Deadline()
	require.True(t, ok)
	assert.Equal(t, globalDeadline, reusedDeadline, "время HTTP-дрена расходует общий бюджет")
}

func TestShutdownBudget_ReleaseEndsBudget(t *testing.T) {
	budget := newShutdownBudget(time.Minute)
	httpCtx := budget.httpContext()
	globalCtx := budget.context()

	budget.release()

	assert.Error(t, httpCtx.Err())
	assert.Error(t, globalCtx.Err())
}

func TestShutdownBudget_ReleaseWithoutBudgetIsHarmless(t *testing.T) {
	assert.NotPanics(t, newShutdownBudget(time.Minute).release)
}
