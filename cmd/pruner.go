package cmd

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/store/metrics"
	"cosmossdk.io/store/types"
	dbm "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/state"
	cmtstore "github.com/cometbft/cometbft/store"
	db "github.com/cosmos/cosmos-db"
	"github.com/syndtr/goleveldb/leveldb/opt"

	// Import corretto per il pacchetto rootmulti locale
	"github.com/binaryholdings/cosmos-pruner/internal/rootmulti"
)

const (
	minFreeGB = 20
)

func getFreeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024 * 1024), nil
}

// Firma della funzione aggiornata per usare uint64
func PruneAppState(dataDir string, keepVersions uint64, noCompact, parallel bool) error {
	// Opzioni di LevelDB semplificate per compatibilità
	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	appDB, err := db.NewGoLevelDBWithOpts("application", dataDir, &o)
	if err != nil {
		return err
	}
	defer appDB.Close()

	if noCompact {
		fmt.Println("Pruning application state (compaction disabled)...")
	} else {
		fmt.Println("Pruning and compacting application state...")
	}
	if parallel {
		fmt.Println("Using parallel pruning strategy.")
	} else {
		fmt.Println("Using sequential pruning strategy.")
	}

	appStore := rootmulti.NewStore(appDB, log.NewLogger(os.Stderr), metrics.NewNoOpMetrics())

	ver := rootmulti.GetLatestVersion(appDB)
	storeNames := []string{}
	if ver != 0 {
		cInfo, err := appStore.GetCommitInfo(ver)
		if err != nil {
			return err
		}
		for _, storeInfo := range cInfo.StoreInfos {
			if len(storeInfo.CommitId.Hash) > 0 {
				storeNames = append(storeNames, storeInfo.Name)
			} else {
				fmt.Println("Skipping", storeInfo.Name, "store due to empty hash")
			}
		}
	}
	keys := types.NewKVStoreKeys(storeNames...)
	for _, value := range keys {
		appStore.MountStoreWithDB(value, types.StoreTypeIAVL, nil)
	}
	if err := appStore.LoadLatestVersion(); err != nil {
		return err
	}

	versions := appStore.GetAllVersions()
	if len(versions) == 0 {
		fmt.Println("No versions found to prune.")
		return nil
	}

	if uint64(len(versions)) <= keepVersions {
		fmt.Println("No versions to prune.")
		return nil
	}

	numToPrune := len(versions) - int(keepVersions)
	pruningHeight := int64(versions[numToPrune-1])

	fmt.Printf("Pruning all versions up to height %d...\n", pruningHeight)

	startTime := time.Now()
	if parallel {
		if err := appStore.PruneStoresParallel(pruningHeight); err != nil {
			return fmt.Errorf("error during parallel pruning: %w", err)
		}
	} else {
		if err := appStore.PruneStores(pruningHeight); err != nil {
			return fmt.Errorf("error during sequential pruning: %w", err)
		}
	}
	fmt.Printf("Logical pruning finished in %s.\n", time.Since(startTime))

	if !noCompact {
		fmt.Println("Checking for available disk space before compaction...")
		freeSpace, err := getFreeDiskSpace(dataDir)
		if err != nil {
			return fmt.Errorf("error checking disk space: %w", err)
		}
		if freeSpace < minFreeGB {
			return fmt.Errorf("insufficient disk space to start compaction (%d GB available, %d GB required). Run with --no-compact and try again later.", freeSpace, minFreeGB)
		}

		fmt.Println("Starting database compaction... This may take a very long time.")
		compactStartTime := time.Now()
		if err := appDB.ForceCompact(nil, nil); err != nil {
			return fmt.Errorf("error during compaction: %w", err)
		}
		fmt.Printf("Compaction finished in %s.\n", time.Since(compactStartTime))
	} else {
		fmt.Println("Compaction was skipped as requested via --no-compact flag.")
	}

	return nil
}

// Firma della funzione aggiornata per usare uint64
func PruneCmtData(dataDir string, keepBlocks uint64) error {
	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	blockStoreDB, err := dbm.NewGoLevelDBWithOpts("blockstore", dataDir, &o)
	if err != nil {
		return err
	}
	blockStore := cmtstore.NewBlockStore(blockStoreDB)

	stateDB, err := dbm.NewGoLevelDBWithOpts("state", dataDir, &o)
	if err != nil {
		return err
	}
	stateStore := state.NewStore(stateDB, state.StoreOptions{})

	base := blockStore.Base()
	if blockStore.Height() < int64(keepBlocks) {
		fmt.Println("No blocks to prune from CometBFT.")
		return nil
	}
	pruneHeight := blockStore.Height() - int64(keepBlocks)

	state, err := stateStore.Load()
	if err != nil {
		return err
	}

	fmt.Println("Pruning block store...")
	_, evidencePoint, err := blockStore.PruneBlocks(pruneHeight, state)
	if err != nil {
		return err
	}

	fmt.Println("Compacting block store...")
	if err := blockStoreDB.Compact(nil, nil); err != nil {
		return err
	}

	fmt.Println("Pruning state store...")
	err = stateStore.PruneStates(base, pruneHeight, evidencePoint)
	if err != nil {
		return err
	}

	fmt.Println("Compacting state store...")
	if err := stateDB.Compact(nil, nil); err != nil {
		return err
	}

	fmt.Println("CometBFT pruning complete.")
	return nil
}
