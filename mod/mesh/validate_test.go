package mesh

import (
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func yggPeersConfig(initial ...string) stconf.YggObj {
	yg := stconf.FullConfig().Ygg
	yg.PemKey = "/tmp/enabled.pem"
	yg.Peers.Initial = initial
	return yg
}

// // // // // // // // // //

func TestValidateYggConfig(t *testing.T) {
	tlsPeer := "tls://peer-a.example:443"
	tcpPeer := "tcp://peer-b.example:443"

	testArr := []struct {
		name     string
		mutate   func(*stconf.YggObj)
		wantText string
	}{
		{
			name:   "empty initial skips all checks",
			mutate: func(yg *stconf.YggObj) { yg.Peers.Initial = nil; yg.Peers.MinPeers = 100 },
		},
		{
			name: "disabled mesh skips all checks",
			mutate: func(yg *stconf.YggObj) {
				yg.PemKey = ""
				yg.Peers.Initial = []string{"   "}
				yg.Peers.HealthInterval = time.Millisecond
			},
		},
		{
			name:   "defaults with one peer are valid",
			mutate: func(*stconf.YggObj) {},
		},
		{
			name:     "sub-second health_interval rejected",
			mutate:   func(yg *stconf.YggObj) { yg.Peers.HealthInterval = time.Millisecond },
			wantText: "ygg.peers.health_interval",
		},
		{
			name:   "zero health_interval allowed as disable",
			mutate: func(yg *stconf.YggObj) { yg.Peers.HealthInterval = 0 },
		},
		{
			name:   "sub-minute refresh_interval stays valid (clamped at startup)",
			mutate: func(yg *stconf.YggObj) { yg.Peers.RefreshInterval = 10 * time.Second },
		},
		{
			name:   "one-minute refresh_interval allowed",
			mutate: func(yg *stconf.YggObj) { yg.Peers.RefreshInterval = time.Minute },
		},
		{
			name:     "initial with no usable URI rejected",
			mutate:   func(yg *stconf.YggObj) { yg.Peers.Initial = []string{"not a uri"} },
			wantText: "no usable peer URIs",
		},
		{
			name:   "invalid entry next to a valid one stays valid like the library",
			mutate: func(yg *stconf.YggObj) { yg.Peers.Initial = []string{tlsPeer, "not a uri"} },
		},
		{
			name:   "duplicate peer URIs stay valid like the library",
			mutate: func(yg *stconf.YggObj) { yg.Peers.Initial = []string{tlsPeer, tlsPeer} },
		},
		{
			name:     "blank-only initial rejected",
			mutate:   func(yg *stconf.YggObj) { yg.Peers.Initial = []string{"   "} },
			wantText: "no usable peer URIs",
		},
		{
			name:     "min_peers at capacity rejected",
			mutate:   func(yg *stconf.YggObj) { yg.Peers.MinPeers = 1 },
			wantText: "ygg.peers.min_peers",
		},
		{
			name: "min_peers below two-scheme capacity accepted",
			mutate: func(yg *stconf.YggObj) {
				yg.Peers.Initial = []string{tlsPeer, tcpPeer}
				yg.Peers.MinPeers = 1
			},
		},
		{
			name: "capacity counts max_per_proto within one scheme",
			mutate: func(yg *stconf.YggObj) {
				yg.Peers.Initial = []string{tlsPeer, "tls://peer-c.example:443"}
				yg.Peers.MaxPerProto = 2
				yg.Peers.MinPeers = 1
			},
		},
		{
			name: "passive skips the capacity check like the library",
			mutate: func(yg *stconf.YggObj) {
				yg.Peers.Passive = true
				yg.Peers.MinPeers = 5
			},
		},
		{
			name: "disabled health recovery skips the capacity check like the library",
			mutate: func(yg *stconf.YggObj) {
				yg.Peers.HealthInterval = 0
				yg.Peers.MinPeers = 5
			},
		},
		{
			name: "capacity check counts only usable deduplicated candidates",
			mutate: func(yg *stconf.YggObj) {
				yg.Peers.Initial = []string{tlsPeer, tlsPeer, "not a uri"}
				yg.Peers.MinPeers = 1
			},
			wantText: "ygg.peers.min_peers",
		},
	}

	for _, testObj := range testArr {
		t.Run(testObj.name, func(t *testing.T) {
			yg := yggPeersConfig(tlsPeer)
			testObj.mutate(&yg)
			err := ValidateYggConfig(yg)
			if testObj.wantText == "" {
				if err != nil {
					t.Fatalf("expected valid config, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testObj.wantText) {
				t.Fatalf("expected error containing %q, got: %v", testObj.wantText, err)
			}
		})
	}
}
