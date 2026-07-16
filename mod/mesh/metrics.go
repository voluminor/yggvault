package mesh

import (
	"net/url"
	"sort"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/voluminor/yggvault/mod/telemetry"
)

// // // // // // // // // //

// cPeerListMax bounds the published peer list. Inbound peers are foreign input with external
// cardinality, so the detailed list is capped; totals stay exact in the aggregate counters.
const cPeerListMax = 256

// //

// PeerSnapshotObj is one peer connection at snapshot time.
type PeerSnapshotObj struct {
	URI           string
	Up            bool
	Inbound       bool
	PublicKey     string
	LatencyNanos  int64
	Cost          uint64
	RXBytes       uint64
	TXBytes       uint64
	UptimeSeconds float64
	LastError     string
	LastErrorTime time.Time
}

// SnapshotObj carries the cheap ygg aggregates for the metrics group.
type SnapshotObj struct {
	PeersKnown       int
	PeersUp          int
	PeersInbound     int
	ActiveSelected   int
	RXBytes          uint64
	TXBytes          uint64
	BestLatencyNanos int64
	// NoReachableNotifications counts observed isolation notifications. The library delivers them
	// best-effort into a capacity-one channel, so rapid bursts coalesce: treat it as a lower bound.
	NoReachableNotifications uint64
}

// peerListCacheObj holds one built peer list; TTL bounds live node work under request pressure.
type peerListCacheObj struct {
	builtAt time.Time
	arr     []PeerSnapshotObj
}

// // // // // // // // // //

// redactPeerURI strips userinfo credentials from a peer URI. Yggdrasil keeps URL userinfo in peer
// state (only query parameters are dropped), and SOCKS peer URIs can carry proxy logins, so raw
// URIs must never reach a metrics surface. An unparsable URI is dropped entirely.
func redactPeerURI(uriText string) string {
	parsedObj, err := url.Parse(uriText)
	if err != nil {
		return ""
	}
	parsedObj.User = nil
	return parsedObj.String()
}

// // // // // // // // // //

// Snapshot returns cheap peer aggregates; ok is false on a disabled or closing node. It runs
// every telemetry collect interval, so it sums counters without building per-peer DTOs.
// The snapMu read lock pairs with the Close write barrier: the library does not guard node
// state reads after Close, so no snapshot may overlap node teardown.
func (obj *Obj) Snapshot() (SnapshotObj, bool) {
	if !obj.enabled || obj.node == nil || obj.closing.Load() {
		return SnapshotObj{}, false
	}
	obj.snapMu.RLock()
	defer obj.snapMu.RUnlock()
	if obj.closing.Load() {
		return SnapshotObj{}, false
	}

	nodeSnapObj := obj.node.Snapshot()
	out := SnapshotObj{
		PeersKnown:               len(nodeSnapObj.Peers),
		ActiveSelected:           len(nodeSnapObj.ActivePeers),
		NoReachableNotifications: obj.noPeersEvents.Load(),
	}
	for i := range nodeSnapObj.Peers {
		peerObj := &nodeSnapObj.Peers[i]
		if peerObj.Up {
			out.PeersUp++
			if latencyNanos := peerObj.Latency.Nanoseconds(); latencyNanos > 0 &&
				(out.BestLatencyNanos == 0 || latencyNanos < out.BestLatencyNanos) {
				out.BestLatencyNanos = latencyNanos
			}
		}
		if peerObj.Inbound {
			out.PeersInbound++
		}
		out.RXBytes += peerObj.RXBytes
		out.TXBytes += peerObj.TXBytes
	}
	return out, true
}

// PeerList returns the detailed peer list: credential-redacted, deterministically sorted, and
// capped at cPeerListMax entries. Results are cached for the telemetry snapshot interval, so
// request pressure costs at most one live node read per interval. Callers must treat the
// returned slice as read-only. ok is false on a disabled or closing node.
func (obj *Obj) PeerList() ([]PeerSnapshotObj, bool) {
	if !obj.enabled || obj.node == nil || obj.closing.Load() {
		return nil, false
	}
	obj.snapMu.RLock()
	defer obj.snapMu.RUnlock()
	if obj.closing.Load() {
		return nil, false
	}

	obj.peerListMu.Lock()
	defer obj.peerListMu.Unlock()
	if cacheObj := obj.peerListCache; cacheObj != nil && time.Since(cacheObj.builtAt) < obj.peerListTTL {
		return cacheObj.arr, true
	}

	nodeSnapObj := obj.node.Snapshot()
	peerArr := make([]PeerSnapshotObj, 0, min(len(nodeSnapObj.Peers), cPeerListMax))
	for i := range nodeSnapObj.Peers {
		if len(peerArr) == cPeerListMax {
			break
		}
		peerObj := &nodeSnapObj.Peers[i]
		peerArr = append(peerArr, PeerSnapshotObj{
			URI:           redactPeerURI(peerObj.URI),
			Up:            peerObj.Up,
			Inbound:       peerObj.Inbound,
			PublicKey:     peerObj.Key,
			LatencyNanos:  peerObj.Latency.Nanoseconds(),
			Cost:          peerObj.Cost,
			RXBytes:       peerObj.RXBytes,
			TXBytes:       peerObj.TXBytes,
			UptimeSeconds: peerObj.Uptime.Seconds(),
			LastError:     peerObj.LastError,
			LastErrorTime: peerObj.LastErrorTime,
		})
	}
	sort.Slice(peerArr, func(i, j int) bool {
		if peerArr[i].URI != peerArr[j].URI {
			return peerArr[i].URI < peerArr[j].URI
		}
		return peerArr[i].PublicKey < peerArr[j].PublicKey
	})
	obj.peerListCache = &peerListCacheObj{builtAt: time.Now(), arr: peerArr}
	return peerArr, true
}

// RegisterMetrics publishes flat ygg aggregates; a disabled node registers nothing.
// Per-peer series stay out of the exposition on purpose: inbound peers are foreign input and
// would grow label cardinality without bound. Traffic sums are gauges, not counters, because a
// disconnected peer drops out of the sum and makes it non-monotonic.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if !obj.enabled {
		return nil
	}
	_, err := telemetry.RegisterSpecs(meterObj, []telemetry.SpecObj{
		{Name: "ygg_peers_known", Help: "peers known to the node, configured and inbound"},
		{Name: "ygg_peers_up", Help: "peers with a connected link"},
		{Name: "ygg_peers_inbound", Help: "peers that initiated their connection"},
		{Name: "ygg_active_selected", Help: "peers currently selected by the peer manager"},
		{Name: "ygg_rx_bytes", Help: "bytes received across current peer connections", Unit: "By"},
		{Name: "ygg_tx_bytes", Help: "bytes transmitted across current peer connections", Unit: "By"},
		{Name: "ygg_best_latency_nanos", Help: "lowest measured latency among connected peers, 0 when unknown", Unit: "ns"},
		{Name: "ygg_no_reachable_notifications", Help: "isolation notifications observed from the peer manager; best-effort delivery, rapid bursts coalesce", Counter: true},
	}, func() ([]int64, bool) {
		snapObj, ok := obj.Snapshot()
		if !ok {
			return nil, false
		}
		return []int64{
			int64(snapObj.PeersKnown),
			int64(snapObj.PeersUp),
			int64(snapObj.PeersInbound),
			int64(snapObj.ActiveSelected),
			int64(snapObj.RXBytes),
			int64(snapObj.TXBytes),
			snapObj.BestLatencyNanos,
			int64(snapObj.NoReachableNotifications),
		}, true
	})
	return err
}
