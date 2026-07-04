package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/voluminor/yggvault/mod/cli"
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
	Command string             `json:"command"`
	Ok      bool               `json:"ok"`
	Scanned uint64             `json:"scanned"`
	Drift   uint64             `json:"drift"`
	Updated uint64             `json:"updated"`
	Items   []artifactDriftObj `json:"items,omitempty"`
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

// // // // // // // // // //

func renderInspect(inspectObj storage.InspectObj, jsonOutput bool) error {
	viewObj := inspectViewObj{
		Command:             cli.CommandInspect,
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
	fmt.Fprintln(bufObj, "storage inspect")
	fmt.Fprintf(bufObj, "  root            %s\n", viewObj.RootPath)
	fmt.Fprintf(bufObj, "  sqlite          %s\n", viewObj.SqlitePath)
	fmt.Fprintf(bufObj, "  pebble          %s\n", viewObj.PebblePath)
	fmt.Fprintf(bufObj, "  hot             %s\n", viewObj.HotPath)
	fmt.Fprintf(bufObj, "  versions        %d\n", viewObj.Versions)
	fmt.Fprintf(bufObj, "  blobs           %d\n", viewObj.Blobs)
	fmt.Fprintf(bufObj, "  artifacts       %d\n", viewObj.Artifacts)
	fmt.Fprintf(bufObj, "  history events  %d\n", viewObj.HistoryEvents)
	fmt.Fprintf(bufObj, "  pebble disk     %s (real %s)\n", humanBytes(viewObj.PebbleDiskBytes), humanBytes(viewObj.PebbleRealDiskBytes))
	fmt.Fprintf(bufObj, "  sqlite disk     %s\n", humanBytes(viewObj.SqliteDiskBytes))
	fmt.Fprintf(bufObj, "  hot disk        %s\n", humanBytes(viewObj.HotBytes))
	return bufObj.Flush()
}

func renderVacuum(resultObj storage.VacuumResultObj, jsonOutput bool) error {
	viewObj := vacuumViewObj{
		Command:     cli.CommandVacuum,
		Ok:          true,
		BeforeBytes: resultObj.BeforeBytes,
		AfterBytes:  resultObj.AfterBytes,
		FreedBytes:  resultObj.FreedBytes,
	}
	if jsonOutput {
		return writeJSON(os.Stdout, viewObj)
	}
	bufObj := bufio.NewWriter(os.Stdout)
	fmt.Fprintln(bufObj, "storage vacuum")
	fmt.Fprintf(bufObj, "  before  %s\n", humanBytes(viewObj.BeforeBytes))
	fmt.Fprintf(bufObj, "  after   %s\n", humanBytes(viewObj.AfterBytes))
	fmt.Fprintf(bufObj, "  freed   %s\n", humanBytes(viewObj.FreedBytes))
	return bufObj.Flush()
}

func renderPrune(resultObj pruneResultViewObj, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(os.Stdout, pruneViewObj{Command: cli.CommandPrune, Ok: true, pruneResultViewObj: resultObj})
	}
	bufObj := bufio.NewWriter(os.Stdout)
	if resultObj.DryRun {
		fmt.Fprintf(bufObj, "storage prune (dry-run): %d stale key(s) — use --force to apply\n", len(resultObj.StaleKeys))
	} else {
		fmt.Fprintf(bufObj, "storage prune: deleted %d key(s), %d version(s), reclaimed %s\n",
			resultObj.DeletedKeys, resultObj.DeletedVersions, humanBytes(resultObj.ReclaimedBytes))
	}
	for i := range resultObj.StaleKeys {
		keyObj := resultObj.StaleKeys[i]
		fmt.Fprintf(bufObj, "  %s  versions=%d  est=%s\n", keyObj.Key, keyObj.Versions, humanBytes(keyObj.ReclaimBytesEstimate))
	}
	return bufObj.Flush()
}

func renderRebuild(resultObj rebuildResultObj, jsonOutput bool) error {
	viewObj := rebuildViewObj{
		Command: cli.CommandRebuildCache,
		Ok:      true,
		Scanned: resultObj.Scanned,
		Drift:   resultObj.Drift,
		Updated: resultObj.Updated,
		Items:   resultObj.Items,
	}
	if jsonOutput {
		return writeJSON(os.Stdout, viewObj)
	}
	bufObj := bufio.NewWriter(os.Stdout)
	fmt.Fprintf(bufObj, "rebuild-cache: scanned %d, drift %d, updated %d\n", viewObj.Scanned, viewObj.Drift, viewObj.Updated)
	for i := range viewObj.Items {
		itemObj := viewObj.Items[i]
		fmt.Fprintf(bufObj, "  %s@%s %s/%s/%s f%d  %s -> %s\n",
			itemObj.Key, itemObj.Version, itemObj.MaterializerID, itemObj.ArtifactKind, itemObj.ListenerID,
			itemObj.FormatVersion, itemObj.StoredHash, itemObj.RebuiltHash)
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
