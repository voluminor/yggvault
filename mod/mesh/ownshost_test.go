package mesh

import (
	"context"
	"testing"
)

// // // // // // // // // //

func TestOwnsHostYggLiteral(t *testing.T) {
	obj, err := New(context.Background(), meshConfig(t, ""))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, host := range []string{"0200::1", "0211:abcd::5", "03ff::1"} {
		if !obj.OwnsHost(host) {
			t.Errorf("ygg literal %s must be owned", host)
		}
	}
	for _, host := range []string{"8.8.8.8", "2606:2800::1", "fe80::1", "::1", "example.com"} {
		if obj.OwnsHost(host) {
			t.Errorf("%s must not be owned", host)
		}
	}
}
