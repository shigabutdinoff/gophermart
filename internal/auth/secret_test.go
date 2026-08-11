package auth

import (
	"encoding/hex"
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

func TestResolveSecretRejectsMissingValue(t *testing.T) {
	_, err := ResolveSecret("")

	require.Error(t, err)
}

func TestResolveSecretAcceptsGeneratedValue(t *testing.T) {
	secret, err := ResolveSecret(GenerateSecret())

	require.NoError(t, err)
	require.Len(t, secret, hex.EncodedLen(SecretSize))
}
