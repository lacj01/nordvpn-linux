package dns

import (
	"fmt"
	"github.com/NordSecurity/nordvpn-linux/config"
)

// Dummy Noop DNS Method to prevent messing with the DNS.
type NoopDnsMethod struct {
	routing config.TrueField
}

func (m *NoopDnsMethod) Set(_ string, _ []string) error {
	if m.routing.Get() {
		return fmt.Errorf("routing is enabled: skipping set noop")
	} else {
		return nil
	}
}

func (m *NoopDnsMethod) Unset(_ string) error {
	if m.routing.Get() {
		return fmt.Errorf("routing is enabled: skipping usset noop")
	} else {
		return nil
	}
}

func (m *NoopDnsMethod) Name() string {
	return "noop"
}
