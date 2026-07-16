package server

import (
	"bytes"
	"context"

	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/mod/server/webui"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/telemetry"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func metricInt(valuesObj map[string]float64, key string) int64 {
	return int64(valuesObj[key])
}

func mapDiagnosticImpact(statusObj stcode.OperationalStatusType) api.MetricsDiagnosticObjImpact {
	switch statusObj {
	case stcode.OperationalStatusOk:
		return api.MetricsDiagnosticObjImpactOk
	case stcode.OperationalStatusDegraded:
		return api.MetricsDiagnosticObjImpactDegraded
	default:
		return api.MetricsDiagnosticObjImpactError
	}
}

func mapDiagnostics(diagArr []state.DiagnosticViewObj) []api.MetricsDiagnosticObj {
	recentArr := make([]api.MetricsDiagnosticObj, 0, len(diagArr))
	for i := range diagArr {
		viewObj := diagArr[i]
		entryObj := api.MetricsDiagnosticObj{
			Code:     viewObj.Code,
			Scope:    viewObj.Scope.String(),
			Impact:   mapDiagnosticImpact(viewObj.Impact),
			Reason:   viewObj.Reason.String(),
			Count:    int64(viewObj.Count),
			LastSeen: viewObj.LastSeen.UTC(),
		}
		if viewObj.Key != "" {
			entryObj.Key = api.NewOptString(viewObj.Key)
		}
		if viewObj.Version != "" {
			entryObj.Version = api.NewOptString(viewObj.Version)
		}
		if viewObj.Message != "" {
			entryObj.Message = api.NewOptString(viewObj.Message)
		}
		if !viewObj.FirstSeen.IsZero() {
			entryObj.FirstSeen = api.NewOptDateTime(viewObj.FirstSeen.UTC())
		}
		recentArr = append(recentArr, entryObj)
	}
	return recentArr
}

func (obj *funcObj) groupValues(enabled bool, groupObj telemetry.Group) (map[string]float64, bool) {
	if !enabled {
		return nil, false
	}
	return obj.deps.Telemetry.GroupValues(groupObj)
}

func mapYggPeers(peerArr []mesh.PeerSnapshotObj) []api.MetricsYggPeerObj {
	outArr := make([]api.MetricsYggPeerObj, 0, len(peerArr))
	for i := range peerArr {
		peerObj := peerArr[i]
		entryObj := api.MetricsYggPeerObj{
			Up:            peerObj.Up,
			Inbound:       peerObj.Inbound,
			LatencyNanos:  peerObj.LatencyNanos,
			Cost:          int64(peerObj.Cost),
			RxBytes:       int64(peerObj.RXBytes),
			TxBytes:       int64(peerObj.TXBytes),
			UptimeSeconds: peerObj.UptimeSeconds,
		}
		if peerObj.URI != "" {
			entryObj.URI = api.NewOptString(peerObj.URI)
		}
		if peerObj.PublicKey != "" {
			entryObj.PublicKey = api.NewOptString(peerObj.PublicKey)
		}
		if peerObj.LastError != "" {
			entryObj.LastError = api.NewOptString(peerObj.LastError)
		}
		if !peerObj.LastErrorTime.IsZero() {
			entryObj.LastErrorTime = api.NewOptDateTime(peerObj.LastErrorTime.UTC())
		}
		outArr = append(outArr, entryObj)
	}
	return outArr
}

// // // // // // // // // //

// GetMetricsIndex builds a no-cache HTML page of metric groups.
// Navigation uses the same gates, so hidden groups are not advertised.
func (obj *funcObj) GetMetricsIndex(ctx context.Context) (api.GetMetricsIndexRes, error) {
	lc := listenerCtxFrom(ctx)
	if !lc.publicMetricsEnabled && !lc.internalMetricsEnabled {
		return nil, serr.ErrNotFound
	}
	yggEnabled := obj.deps.Mesh != nil && obj.deps.Mesh.Enabled()
	htmlArr, err := webui.Metrics(obj.viewContext(lc), lc.publicMetricsEnabled, lc.internalMetricsEnabled, yggEnabled, obj.deps.Config.Metrics.SnapshotInterval)
	if err != nil {
		return nil, err
	}
	return &api.GetMetricsIndexOKHeaders{
		CacheControl: api.NewOptString(cNoCacheControl),
		Response:     api.GetMetricsIndexOK{Data: bytes.NewReader(htmlArr)},
	}, nil
}

// GetMetricsCore serves core HTTP request metrics as JSON behind the public gate.
// ogen records duration in milliseconds, so the sum is converted to seconds.
func (obj *funcObj) GetMetricsCore(ctx context.Context) (api.GetMetricsCoreRes, error) {
	lc := listenerCtxFrom(ctx)
	valuesObj, ok := obj.groupValues(lc.publicMetricsEnabled, telemetry.GroupCore)
	if !ok {
		return nil, serr.ErrNotFound
	}
	coreObj := api.MetricsCoreObj{
		RequestsTotal:               metricInt(valuesObj, "ogen_server_request_count"),
		ErrorsTotal:                 metricInt(valuesObj, "ogen_server_errors_count"),
		RequestDurationSecondsSum:   valuesObj["ogen_server_duration_sum"] / 1000,
		RequestDurationSecondsCount: metricInt(valuesObj, "ogen_server_duration_count"),
	}
	return &api.MetricsCoreObjHeaders{
		CacheControl: api.NewOptString(cNoCacheControl),
		Response:     coreObj,
	}, nil
}

// GetMetricsCache serves RAM cache and storage-engine metrics as JSON behind the public gate.
func (obj *funcObj) GetMetricsCache(ctx context.Context) (api.GetMetricsCacheRes, error) {
	lc := listenerCtxFrom(ctx)
	valuesObj, ok := obj.groupValues(lc.publicMetricsEnabled, telemetry.GroupCache)
	if !ok {
		return nil, serr.ErrNotFound
	}
	cacheObj := api.MetricsCacheObj{
		Cache: api.MetricsCacheObjCache{
			Hits:                    metricInt(valuesObj, "cache_hits"),
			Misses:                  metricInt(valuesObj, "cache_misses"),
			Materializations:        metricInt(valuesObj, "cache_materializations"),
			MaterializationsAborted: metricInt(valuesObj, "cache_materializations_aborted"),
			Evictions:               metricInt(valuesObj, "cache_evictions"),
			SingleflightShared:      metricInt(valuesObj, "cache_singleflight_shared"),
			Entries:                 metricInt(valuesObj, "cache_entries"),
			Bytes:                   metricInt(valuesObj, "cache_bytes"),
		},
		Storage: api.MetricsCacheObjStorage{
			LiveBytes:                   metricInt(valuesObj, "storage_pebble_live_bytes"),
			ObsoleteBytes:               metricInt(valuesObj, "storage_pebble_obsolete_bytes"),
			ZombieBytes:                 metricInt(valuesObj, "storage_pebble_zombie_bytes"),
			WalBytes:                    metricInt(valuesObj, "storage_pebble_wal_bytes"),
			MemtableBytes:               metricInt(valuesObj, "storage_pebble_memtable_bytes"),
			CompactionDebtBytes:         metricInt(valuesObj, "storage_pebble_compaction_debt_bytes"),
			CompactionInprogressBytes:   metricInt(valuesObj, "storage_pebble_compaction_inprogress_bytes"),
			Compactions:                 metricInt(valuesObj, "storage_pebble_compactions"),
			Flushes:                     metricInt(valuesObj, "storage_pebble_flushes"),
			BlockCacheHits:              metricInt(valuesObj, "storage_pebble_block_cache_hits"),
			BlockCacheMisses:            metricInt(valuesObj, "storage_pebble_block_cache_misses"),
			WriteStalls:                 metricInt(valuesObj, "storage_pebble_write_stalls"),
			BackgroundErrors:            metricInt(valuesObj, "storage_pebble_background_errors"),
			DataCorruptions:             metricInt(valuesObj, "storage_pebble_data_corruptions"),
			DiskSlow:                    metricInt(valuesObj, "storage_pebble_disk_slow"),
			PossibleAPIMisuse:           metricInt(valuesObj, "storage_pebble_possible_api_misuse"),
			FlushDurationNanos:          metricInt(valuesObj, "storage_pebble_flush_duration_nanos"),
			CompactionDurationNanos:     metricInt(valuesObj, "storage_pebble_compaction_duration_nanos"),
			HotEnforceWalks:             metricInt(valuesObj, "storage_hot_enforce_walks"),
			HotEnforceWalkDurationNanos: metricInt(valuesObj, "storage_hot_enforce_walk_duration_nanos"),
		},
	}
	return &api.MetricsCacheObjHeaders{
		CacheControl: api.NewOptString(cNoCacheControl),
		Response:     cacheObj,
	}, nil
}

// GetMetricsErrors serves the live Error Registry as JSON behind the public gate.
// Values come from state: the registry keeps diagnostic fields that are absent from the series.
func (obj *funcObj) GetMetricsErrors(ctx context.Context) (api.GetMetricsErrorsRes, error) {
	lc := listenerCtxFrom(ctx)
	if !lc.publicMetricsEnabled {
		return nil, serr.ErrNotFound
	}
	snapshotObj := obj.deps.State.Snapshot()
	healthView := snapshotObj.Health()
	recentArr := snapshotObj.RecentDiagnostics(int(obj.deps.Config.Metrics.Errors.RecentBuffer))
	errorsObj := api.MetricsErrorsObj{
		Active:  int64(healthView.DiagnosticsCount),
		Dropped: int64(healthView.DroppedDiagnostics),
		Recent:  mapDiagnostics(recentArr),
	}
	return &api.MetricsErrorsObjHeaders{
		CacheControl: api.NewOptString(cNoCacheControl),
		Response:     errorsObj,
	}, nil
}

// GetMetricsRescan serves rescan metrics as JSON behind the public gate.
// The average cycle duration is computed from the histogram sum/count.
func (obj *funcObj) GetMetricsRescan(ctx context.Context) (api.GetMetricsRescanRes, error) {
	lc := listenerCtxFrom(ctx)
	valuesObj, ok := obj.groupValues(lc.publicMetricsEnabled, telemetry.GroupRescan)
	if !ok {
		return nil, serr.ErrNotFound
	}
	rescanObj := api.MetricsRescanObj{
		CyclesTotal:            metricInt(valuesObj, "rescan_cycles_total"),
		VersionsPublishedTotal: metricInt(valuesObj, "rescan_versions_published_total"),
		KeysUnavailableTotal:   metricInt(valuesObj, "rescan_keys_unavailable_total"),
	}
	if countValue := valuesObj["rescan_cycle_duration_seconds_count"]; countValue > 0 {
		rescanObj.CycleDurationSecondsAvg = valuesObj["rescan_cycle_duration_seconds_sum"] / countValue
	}
	if lastRescan := obj.deps.State.Snapshot().LastRescan; !lastRescan.IsZero() {
		rescanObj.LastRescan = api.NewOptDateTime(lastRescan.UTC())
	}
	return &api.MetricsRescanObjHeaders{
		CacheControl: api.NewOptString(cNoCacheControl),
		Response:     rescanObj,
	}, nil
}

// GetMetricsYgg serves Yggdrasil mesh aggregates as JSON behind the public gate; a stopped mesh
// returns 404 on every listener. Aggregates come from the prebuilt telemetry snapshot, so a
// public request performs no live node work and stays as cheap as the other metric groups.
// The per-peer list exposes direct topology, so it is included only behind the internal gate;
// that live read is an operator-only surface, matching the errors group precedent.
func (obj *funcObj) GetMetricsYgg(ctx context.Context) (api.GetMetricsYggRes, error) {
	lc := listenerCtxFrom(ctx)
	if obj.deps.Mesh == nil || !obj.deps.Mesh.Enabled() {
		return nil, serr.ErrNotFound
	}
	valuesObj, ok := obj.groupValues(lc.publicMetricsEnabled, telemetry.GroupYgg)
	if !ok {
		return nil, serr.ErrNotFound
	}
	yggObj := api.MetricsYggObj{
		PeersKnown:                    metricInt(valuesObj, "ygg_peers_known"),
		PeersUp:                       metricInt(valuesObj, "ygg_peers_up"),
		PeersInbound:                  metricInt(valuesObj, "ygg_peers_inbound"),
		ActiveSelected:                metricInt(valuesObj, "ygg_active_selected"),
		RxBytes:                       metricInt(valuesObj, "ygg_rx_bytes"),
		TxBytes:                       metricInt(valuesObj, "ygg_tx_bytes"),
		NoReachableNotificationsTotal: metricInt(valuesObj, "ygg_no_reachable_notifications"),
	}
	if latencyNanos := metricInt(valuesObj, "ygg_best_latency_nanos"); latencyNanos > 0 {
		yggObj.BestLatencyNanos = api.NewOptInt64(latencyNanos)
	}
	if lc.internalMetricsEnabled {
		if peerArr, peersOK := obj.deps.Mesh.PeerList(); peersOK {
			yggObj.Peers = mapYggPeers(peerArr)
		}
	}
	return &api.MetricsYggObjHeaders{
		CacheControl: api.NewOptString(cNoCacheControl),
		Response:     yggObj,
	}, nil
}

// GetMetricsInternal serves the full prebuilt text exposition behind the internal gate.
func (obj *funcObj) GetMetricsInternal(ctx context.Context) (api.GetMetricsInternalRes, error) {
	lc := listenerCtxFrom(ctx)
	if !lc.internalMetricsEnabled {
		return nil, serr.ErrNotFound
	}
	docArr, ok := obj.deps.Telemetry.InternalOM()
	if !ok {
		return nil, serr.ErrNotFound
	}
	return &api.GetMetricsInternalOKHeaders{
		CacheControl: api.NewOptString(cNoStoreControl),
		Response:     api.GetMetricsInternalOK{Data: bytes.NewReader(docArr)},
	}, nil
}
