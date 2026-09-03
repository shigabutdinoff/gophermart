package accrual

import (
	"fmt"
	"net/url"
	"strings"
)

// normalizeAddress приводит ACCRUAL_SYSTEM_ADDRESS вида host:port к URL.
func normalizeAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("accrual: address must not be empty")
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}

	parsed, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("accrual: parse address %q: %w", address, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("accrual: address %q must be an http or https url", address)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("accrual: address %q must carry a host", address)
	}

	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}).String(), nil
}
