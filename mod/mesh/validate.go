package mesh

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/voluminor/ratatoskr/mod/peermgr"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cMinHealthInterval floors non-zero health checks: the library feeds the value into a ticker
	// and a failing mesh turns every tick into probe traffic and logs.
	cMinHealthInterval = time.Second
	// cMinRefreshInterval is the anti-storm floor for scheduled refreshes: one refresh re-probes
	// the whole candidate pool. refresh_interval predates this floor, so sub-minute values are
	// clamped at startup with a warning instead of failing validation.
	cMinRefreshInterval = time.Minute
)

// // // // // // // // // //

// clampToInt converts an unbounded config uint without overflow: values above MaxInt would wrap
// negative through a plain conversion and be rejected by the library at startup.
func clampToInt(value uint) int {
	if uint64(value) > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}

// selectableCapacity mirrors the peermgr maxSelectablePeers rule for v1.1.0: the sum over URI
// schemes of min(candidates in scheme, max_per_proto).
func selectableCapacity(entryArr []peermgr.PeerEntryObj, maxPerProto int) int {
	perProto := make(map[string]int, len(entryArr))
	for _, entryObj := range entryArr {
		perProto[entryObj.Scheme]++
	}
	total := 0
	for _, count := range perProto {
		if count > maxPerProto {
			count = maxPerProto
		}
		total += count
	}
	return total
}

// // // // // // // // // //

// ValidateYggConfig checks ygg.peers against ratatoskr peermgr rules so that --validate-config
// rejects configurations node startup would reject. Pre-existing deployments must keep starting:
// checks on fields that existed before v1 stay warn-or-clamp at runtime, hard errors apply only
// to the new fields and to configurations the library itself would reject. A disabled mesh
// (empty pem_key) skips the checks entirely because the peer list is inert. Invalid or duplicate
// URIs follow the library contract: they are dropped with startup warnings, and only a list with
// no usable candidate fails, because node startup would fail on it anyway.
func ValidateYggConfig(yg stconf.YggObj) error {
	peers := yg.Peers
	if yg.PemKey == "" || len(peers.Initial) == 0 {
		return nil
	}

	if peers.HealthInterval != 0 && peers.HealthInterval < cMinHealthInterval {
		return fmt.Errorf("ygg.peers.health_interval must be 0 (disable health recovery) or >= %s", cMinHealthInterval)
	}

	entryArr, _ := peermgr.ValidatePeers(peers.Initial)
	if len(entryArr) == 0 {
		return errors.New("ygg.peers.initial contains no usable peer URIs")
	}

	minPeersEnforced := !peers.Passive && peers.HealthInterval != 0 && peers.MinPeers > 0
	if minPeersEnforced {
		capacity := selectableCapacity(entryArr, clampToInt(peers.MaxPerProto))
		if clampToInt(peers.MinPeers) >= capacity {
			return fmt.Errorf(
				"ygg.peers.min_peers (%d) must stay below the selectable peer capacity (%d): sum over URI schemes of min(scheme candidates, max_per_proto)",
				peers.MinPeers, capacity)
		}
	}
	return nil
}
