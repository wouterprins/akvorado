//go:build !release

package snmp

import (
	"context"
	"fmt"
	"testing"

	"flowexporter/reporter"
)

// mockPoller will use static data.
type mockPoller struct {
	community string
	put       func(string, uint, Interface)
}

// newMockPoller creates a fake SNMP poller.
func newMockPoller(community string, put func(string, uint, Interface)) *mockPoller {
	return &mockPoller{
		community: community,
		put:       put,
	}
}

// Poll just builds synthetic data.
func (p *mockPoller) Poll(ctx context.Context, host string, port uint16, community string, ifIndex uint) {
	if community == p.community {
		p.put(host, ifIndex, Interface{
			Name:        fmt.Sprintf("Gi0/0/%d", ifIndex),
			Description: fmt.Sprintf("Interface %d", ifIndex),
		})
	}
}

// NewMock creates a new SNMP component building synthetic values. It is already started.
func NewMock(t *testing.T, reporter *reporter.Reporter, configuration Configuration, dependencies Dependencies) *Component {
	t.Helper()
	c, err := New(reporter, configuration, dependencies)
	if err != nil {
		t.Fatalf("New() error:\n%+v", err)
	}
	// Change the poller to a fake one.
	c.poller = newMockPoller("public", c.sc.Put)
	if err := c.Start(); err != nil {
		t.Fatalf("Start() error:\n%+v", err)
	}
	return c
}