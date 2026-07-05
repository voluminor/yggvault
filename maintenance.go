package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/cli"
	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cMaintenanceKeyPage — page size when iterating storage keys (keyset pagination).
	cMaintenanceKeyPage = 512
)

// errAlreadyReported — sentinel: the error has already been rendered (json to stderr); main just exits with code 1.
var errAlreadyReported = errors.New("maintenance error already reported")

// // // // // // // // // //

func isStorageLockError(err error) bool {
	return errors.Is(err, syscall.EAGAIN) ||
		strings.Contains(err.Error(), "resource temporarily unavailable")
}

func openStorageExclusive(ctx context.Context, configObj *stconf.ConfigObj, logArr ...zerolog.Logger) (*storage.Obj, error) {
	storeObj, err := storage.New(ctx, configObj, logArr...)
	if err != nil {
		if isStorageLockError(err) {
			return nil, errors.New("storage is locked (another instance is running); stop the server before maintenance")
		}
		return nil, err
	}
	return storeObj, nil
}

func maintenanceCommandName(maintenanceObj cli.MaintenanceObj) string {
	switch {
	case maintenanceObj.Inspect:
		return cli.CommandInspect
	case maintenanceObj.Vacuum:
		return cli.CommandVacuum
	case maintenanceObj.Prune:
		return cli.CommandPrune
	case maintenanceObj.RebuildCache:
		return cli.CommandRebuildCache
	default:
		return "maintenance"
	}
}

func allDistinctKeys(ctx context.Context, storeObj *storage.Obj) ([]string, error) {
	var keyArr []string
	afterKey := ""
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pageArr, err := storeObj.DistinctKeys(ctx, afterKey, cMaintenanceKeyPage)
		if err != nil {
			return nil, err
		}
		if len(pageArr) == 0 {
			break
		}
		keyArr = append(keyArr, pageArr...)
		afterKey = pageArr[len(pageArr)-1]
		if len(pageArr) < cMaintenanceKeyPage {
			break
		}
	}
	return keyArr, nil
}

// // // // // // // // // //

func runMaintenance(bootObj *cli.Obj) error {
	maintenanceObj := bootObj.Maintenance

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	storageLogArr := []zerolog.Logger(nil)
	if !maintenanceObj.JsonOutput {
		storageLogArr = append(storageLogArr, *bootObj.Logger.Zero())
	}
	storeObj, err := openStorageExclusive(ctx, bootObj.Config, storageLogArr...)
	if err != nil {
		return reportMaintenanceError(maintenanceObj, err)
	}
	defer func() { _ = storeObj.Close(context.Background()) }()

	if err = dispatchMaintenance(ctx, bootObj, storeObj); err != nil {
		return reportMaintenanceError(maintenanceObj, err)
	}
	return nil
}

func reportMaintenanceError(maintenanceObj cli.MaintenanceObj, err error) error {
	if maintenanceObj.JsonOutput {
		renderErrorJSON(os.Stderr, maintenanceCommandName(maintenanceObj), err)
		return errAlreadyReported
	}
	return err
}

func dispatchMaintenance(ctx context.Context, bootObj *cli.Obj, storeObj *storage.Obj) error {
	maintenanceObj := bootObj.Maintenance
	switch {
	case maintenanceObj.Inspect:
		return runInspect(ctx, storeObj, maintenanceObj.JsonOutput)
	case maintenanceObj.Vacuum:
		return runVacuum(ctx, storeObj, maintenanceObj.JsonOutput)
	case maintenanceObj.Prune:
		return runPrune(ctx, bootObj.Config, storeObj, maintenanceObj.Force, maintenanceObj.JsonOutput)
	case maintenanceObj.RebuildCache:
		return runRebuildCache(ctx, bootObj.Config, storeObj, maintenanceObj.JsonOutput)
	default:
		return errors.New("no maintenance command requested")
	}
}

// // // // // // // // // //

func runInspect(ctx context.Context, storeObj *storage.Obj, jsonOutput bool) error {
	inspectObj, err := storeObj.Inspect(ctx)
	if err != nil {
		return err
	}
	return renderInspect(inspectObj, jsonOutput)
}

func runVacuum(ctx context.Context, storeObj *storage.Obj, jsonOutput bool) error {
	resultObj, err := storeObj.Vacuum(ctx)
	if err != nil {
		return err
	}
	return renderVacuum(resultObj, jsonOutput)
}

func runPrune(ctx context.Context, configObj *stconf.ConfigObj, storeObj *storage.Obj, force bool, jsonOutput bool) error {
	storedKeyArr, err := allDistinctKeys(ctx, storeObj)
	if err != nil {
		return err
	}
	configKeySet := make(map[string]struct{}, len(configObj.ReleaseMirrors))
	for keyText := range configObj.ReleaseMirrors {
		configKeySet[keyText] = struct{}{}
	}
	staleArr := make([]string, 0)
	for _, keyText := range storedKeyArr {
		if _, ok := configKeySet[keyText]; !ok {
			staleArr = append(staleArr, keyText)
		}
	}
	sort.Strings(staleArr)

	if !force {
		itemArr := make([]pruneKeyViewObj, 0, len(staleArr))
		for _, keyText := range staleArr {
			versionCount, reclaimBytes, estErr := storeObj.KeyDeletionEstimate(ctx, keyText)
			if estErr != nil {
				return estErr
			}
			itemArr = append(itemArr, pruneKeyViewObj{Key: keyText, Versions: versionCount, ReclaimBytesEstimate: reclaimBytes})
		}
		return renderPrune(pruneResultViewObj{DryRun: true, StaleKeys: itemArr}, jsonOutput)
	}

	beforeBytes := storeObj.DurableBytes()
	itemArr := make([]pruneKeyViewObj, 0, len(staleArr))
	var deletedVersions uint64
	// DeleteKey deliberately does not touch shared blobs — RepairBlobRefs picks them up.
	// A prune interrupted (error, Ctrl-C) without repair leaks orphan blobs forever:
	// a re-run will no longer see the deleted keys. That is why repair runs to completion via defer.
	repairPending := false
	defer func() {
		if !repairPending {
			return
		}
		if repairErr := storeObj.RepairBlobRefs(context.WithoutCancel(ctx)); repairErr != nil {
			fmt.Fprintf(os.Stderr, "prune: orphan blob repair failed: %v\n", repairErr)
		}
	}()
	for _, keyText := range staleArr {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		repairPending = true
		versionCount, reclaimBytes, delErr := storeObj.DeleteKey(ctx, keyText)
		if delErr != nil {
			return delErr
		}
		deletedVersions += versionCount
		itemArr = append(itemArr, pruneKeyViewObj{Key: keyText, Versions: versionCount, ReclaimBytesEstimate: reclaimBytes})
	}
	if repairPending {
		// Clear before the explicit attempt; defer must not run a second repair after a failed call.
		repairPending = false
		if err = storeObj.RepairBlobRefs(ctx); err != nil {
			return err
		}
	}
	afterBytes := storeObj.DurableBytes()
	reclaimedBytes := uint64(0)
	if beforeBytes > afterBytes {
		reclaimedBytes = beforeBytes - afterBytes
	}
	return renderPrune(pruneResultViewObj{
		DryRun:          false,
		StaleKeys:       itemArr,
		DeletedKeys:     uint64(len(staleArr)),
		DeletedVersions: deletedVersions,
		ReclaimedBytes:  reclaimedBytes,
	}, jsonOutput)
}

func runRebuildCache(ctx context.Context, configObj *stconf.ConfigObj, storeObj *storage.Obj, jsonOutput bool) error {
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		return err
	}
	yggHost := ""
	if configObj.Ygg.PemKey != "" {
		if yggHost, err = mesh.HostFromKey(configObj.Ygg.PemKey); err != nil {
			return fmt.Errorf("derive ygg host: %w", err)
		}
	}
	listenerArr := listenerContextsFromConfig(configObj, yggHost)

	fingerprint := hostIdentityFingerprint(configObj, yggHost)
	priorFingerprint, hadPrior, err := storeObj.GetGlobal(ctx, cHostIdentityGlobalKey)
	if err != nil {
		return err
	}
	hostChanged := hadPrior && priorFingerprint != fingerprint

	resultObj, err := rebuildAllArtifacts(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		return err
	}
	if err = storeObj.SetGlobal(ctx, cHostIdentityGlobalKey, fingerprint); err != nil {
		return err
	}
	if err = storeObj.SetGlobal(ctx, cArtifactLayoutGlobalKey, artifactLayoutFingerprint()); err != nil {
		return err
	}
	return renderRebuild(resultObj, hostChanged, jsonOutput)
}
