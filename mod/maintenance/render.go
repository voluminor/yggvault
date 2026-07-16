package maintenance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/voluminor/yggvault/mod/storage"
)

// // // // // // // // // //

type inspectViewObj struct {
	Command             string `json:"command"`
	Ok                  bool   `json:"ok"`
	RootPath            string `json:"root_path"`
	SqlitePath          string `json:"sqlite_path"`
	PebblePath          string `json:"pebble_path"`
	HotPath             string `json:"hot_path"`
	Versions            uint64 `json:"versions"`
	Blobs               uint64 `json:"blobs"`
	Artifacts           uint64 `json:"artifacts"`
	HistoryEvents       uint64 `json:"history_events"`
	PebbleDiskBytes     uint64 `json:"pebble_disk_bytes"`
	PebbleRealDiskBytes uint64 `json:"pebble_real_disk_bytes"`
	SqliteDiskBytes     uint64 `json:"sqlite_disk_bytes"`
	HotBytes            uint64 `json:"hot_bytes"`
}

type vacuumViewObj struct {
	Command     string `json:"command"`
	Ok          bool   `json:"ok"`
	BeforeBytes uint64 `json:"before_bytes"`
	AfterBytes  uint64 `json:"after_bytes"`
	FreedBytes  uint64 `json:"freed_bytes"`
}

type pruneKeyViewObj struct {
	Key                  string `json:"key"`
	Versions             uint64 `json:"versions"`
	ReclaimBytesEstimate uint64 `json:"reclaim_bytes_estimate"`
}

type pruneResultViewObj struct {
	DryRun          bool              `json:"dry_run"`
	StaleKeys       []pruneKeyViewObj `json:"stale_keys"`
	DeletedKeys     uint64            `json:"deleted_keys"`
	DeletedVersions uint64            `json:"deleted_versions"`
	ReclaimedBytes  uint64            `json:"reclaimed_bytes"`
}

type pruneViewObj struct {
	Command string `json:"command"`
	Ok      bool   `json:"ok"`
	pruneResultViewObj
}

type rebuildViewObj struct {
	Command     string             `json:"command"`
	Ok          bool               `json:"ok"`
	Scanned     uint64             `json:"scanned"`
	Drift       uint64             `json:"drift"`
	Updated     uint64             `json:"updated"`
	Created     uint64             `json:"created"`
	Pruned      uint64             `json:"pruned"`
	HostChanged bool               `json:"host_changed"`
	Items       []ArtifactDriftObj `json:"items,omitempty"`
}

type errorViewObj struct {
	Command string `json:"command"`
	Ok      bool   `json:"ok"`
	Error   string `json:"error"`
}

// // // // // // // // // //

func writeJSON(writerObj io.Writer, viewObj any) error {
	dataArr, err := json.MarshalIndent(viewObj, "", "  ")
	if err != nil {
		return err
	}
	if _, err = writerObj.Write(append(dataArr, '\n')); err != nil {
		return err
	}
	return nil
}

func renderErrorJSON(writerObj io.Writer, command string, err error) {
	_ = writeJSON(writerObj, errorViewObj{Command: command, Ok: false, Error: err.Error()})
}

func writeLine(writerObj io.Writer, argArr ...any) error {
	_, err := fmt.Fprintln(writerObj, argArr...)
	return err
}

func writeFormat(writerObj io.Writer, formatText string, argArr ...any) error {
	_, err := fmt.Fprintf(writerObj, formatText, argArr...)
	return err
}

// // // // // // // // // //

func renderInspect(inspectObj storage.InspectObj, jsonOutput bool) error {
	viewObj := inspectViewObj{
		Command:             cCommandInspect,
		Ok:                  true,
		RootPath:            inspectObj.RootPath,
		SqlitePath:          inspectObj.SQLitePath,
		PebblePath:          inspectObj.PebblePath,
		HotPath:             inspectObj.HotPath,
		Versions:            inspectObj.VersionCount,
		Blobs:               inspectObj.BlobCount,
		Artifacts:           inspectObj.ArtifactCount,
		HistoryEvents:       inspectObj.HistoryEventCount,
		PebbleDiskBytes:     inspectObj.PebbleDiskBytes,
		PebbleRealDiskBytes: inspectObj.PebbleRealDiskBytes,
		SqliteDiskBytes:     inspectObj.SQLiteDiskBytes,
		HotBytes:            inspectObj.HotBytes,
	}
	if jsonOutput {
		return writeJSON(os.Stdout, viewObj)
	}
	bufObj := bufio.NewWriter(os.Stdout)
	if err := writeLine(bufObj, "storage inspect"); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  root            %s\n", viewObj.RootPath); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  sqlite          %s\n", viewObj.SqlitePath); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  pebble          %s\n", viewObj.PebblePath); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  hot             %s\n", viewObj.HotPath); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  versions        %d\n", viewObj.Versions); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  blobs           %d\n", viewObj.Blobs); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  artifacts       %d\n", viewObj.Artifacts); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  history events  %d\n", viewObj.HistoryEvents); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  pebble disk     %s (real %s)\n", humanBytes(viewObj.PebbleDiskBytes), humanBytes(viewObj.PebbleRealDiskBytes)); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  sqlite disk     %s\n", humanBytes(viewObj.SqliteDiskBytes)); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  hot disk        %s\n", humanBytes(viewObj.HotBytes)); err != nil {
		return err
	}
	return bufObj.Flush()
}

func renderVacuum(resultObj storage.VacuumResultObj, jsonOutput bool) error {
	viewObj := vacuumViewObj{
		Command:     cCommandVacuum,
		Ok:          true,
		BeforeBytes: resultObj.BeforeBytes,
		AfterBytes:  resultObj.AfterBytes,
		FreedBytes:  resultObj.FreedBytes,
	}
	if jsonOutput {
		return writeJSON(os.Stdout, viewObj)
	}
	bufObj := bufio.NewWriter(os.Stdout)
	if err := writeLine(bufObj, "storage vacuum"); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  before  %s\n", humanBytes(viewObj.BeforeBytes)); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  after   %s\n", humanBytes(viewObj.AfterBytes)); err != nil {
		return err
	}
	if err := writeFormat(bufObj, "  freed   %s\n", humanBytes(viewObj.FreedBytes)); err != nil {
		return err
	}
	return bufObj.Flush()
}

func renderPrune(resultObj pruneResultViewObj, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(os.Stdout, pruneViewObj{Command: cCommandPrune, Ok: true, pruneResultViewObj: resultObj})
	}
	bufObj := bufio.NewWriter(os.Stdout)
	if resultObj.DryRun {
		if err := writeFormat(bufObj, "storage prune (dry-run): %d stale key(s) — use --force to apply\n", len(resultObj.StaleKeys)); err != nil {
			return err
		}
	} else {
		if err := writeFormat(bufObj, "storage prune: deleted %d key(s), %d version(s), reclaimed %s\n",
			resultObj.DeletedKeys, resultObj.DeletedVersions, humanBytes(resultObj.ReclaimedBytes)); err != nil {
			return err
		}
	}
	for i := range resultObj.StaleKeys {
		keyObj := resultObj.StaleKeys[i]
		if err := writeFormat(bufObj, "  %s  versions=%d  est=%s\n", keyObj.Key, keyObj.Versions, humanBytes(keyObj.ReclaimBytesEstimate)); err != nil {
			return err
		}
	}
	return bufObj.Flush()
}

func renderRebuild(resultObj RebuildResultObj, hostChanged bool, jsonOutput bool) error {
	viewObj := rebuildViewObj{
		Command:     cCommandRebuildCache,
		Ok:          true,
		Scanned:     resultObj.Scanned,
		Drift:       resultObj.Drift,
		Updated:     resultObj.Updated,
		Created:     resultObj.Created,
		Pruned:      resultObj.Pruned,
		HostChanged: hostChanged,
		Items:       resultObj.Items,
	}
	if jsonOutput {
		return writeJSON(os.Stdout, viewObj)
	}
	bufObj := bufio.NewWriter(os.Stdout)
	if hostChanged {
		if err := writeLine(bufObj, "rebuild-cache: WARNING host identity (domain/routing-prefix/ygg-host) changed since the last rebuild; host-sensitive artifacts were re-homed to new module paths"); err != nil {
			return err
		}
	}
	if err := writeFormat(bufObj, "rebuild-cache: scanned %d, drift %d, updated %d, created %d, pruned %d\n", viewObj.Scanned, viewObj.Drift, viewObj.Updated, viewObj.Created, viewObj.Pruned); err != nil {
		return err
	}
	for i := range viewObj.Items {
		itemObj := viewObj.Items[i]
		if err := writeFormat(bufObj, "  %s@%s %s/%s/%s f%d  %s -> %s\n",
			itemObj.Key, itemObj.Version, itemObj.MaterializerID, itemObj.ArtifactKind, itemObj.ListenerID,
			itemObj.FormatVersion, itemObj.StoredHash, itemObj.RebuiltHash); err != nil {
			return err
		}
	}
	return bufObj.Flush()
}

// // // // // // // // // //

func humanBytes(byteCount uint64) string {
	const unitStep = 1024
	if byteCount < unitStep {
		return fmt.Sprintf("%d B", byteCount)
	}
	divValue := uint64(unitStep)
	expValue := 0
	for byteCount/divValue >= unitStep && expValue < 4 {
		divValue *= unitStep
		expValue++
	}
	suffixArr := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	return fmt.Sprintf("%.2f %s", float64(byteCount)/float64(divValue), suffixArr[expValue])
}
