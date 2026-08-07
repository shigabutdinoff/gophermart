package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSecretUsesConfiguredValue(t *testing.T) {
	configured := strings.Repeat("s", 32) + "-configured"

	first, err := ResolveSecret(configured)
	require.NoError(t, err)
	require.Equal(t, configured, string(first))

	second, err := ResolveSecret(configured)
	require.NoError(t, err)
	first[0] = 'x'
	require.Equal(t, configured, string(second))
}

func TestResolveSecretRejectsShortConfiguredValueWithoutLeakingIt(t *testing.T) {
	const configured = "too-short-private-value"

	_, err := ResolveSecret(configured)
	require.Error(t, err)
	require.NotContains(t, err.Error(), configured)
}

func TestResolveSecretGeneratesDistinct32ByteValues(t *testing.T) {
	first, err := ResolveSecret("")
	require.NoError(t, err)
	second, err := ResolveSecret("")
	require.NoError(t, err)

	require.Len(t, first, SecretSize)
	require.Len(t, second, SecretSize)
	require.NotEqual(t, first, second)
}
