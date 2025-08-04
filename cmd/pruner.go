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

	"github.com/binaryholdings/cosmos-pruner/internal/rootmulti"
)

const (
	// Dimensione del batch, puoi regolarla. Un valore più piccolo è più sicuro per lo spazio,
	// ma potrebbe rendere il processo totale leggermente più lento.
	batchSize = 1000
	minFreeGB = 20
)

func getFreeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024 * 1024), nil
}

// PruneAppState esegue il pruning dello stato dell'applicazione.
// Il comportamento di default è un ciclo a batch sicuro che pota e compatta.
// --no-compact bypassa questo ciclo ed esegue solo il pruning logico in un'unica passata.
// --parallel accelera il pruning logico all'interno di ogni batch.
func PruneAppState(dataDir string, keepVersions uint64, noCompact, parallel bool) error {
	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	appDB, err := db.NewGoLevelDBWithOpts("application", dataDir, &o)
	if err != nil {
		return err
	}
	defer appDB.Close()

	// Caricamento dello store (codice standard)
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

	// Calcola il numero totale di versioni da eliminare
	versions := appStore.GetAllVersions()
	if uint64(len(versions)) <= keepVersions {
		fmt.Println("No versions to prune.")
		return nil
	}
	totalToPrune := int64(len(versions)) - int64(keepVersions)

	// === Logica di Esecuzione ===

	if noCompact {
		// Modalità "Downtime Minimo": potatura logica veloce, senza compattazione.
		fmt.Printf("Pruning %d versions logically in a single pass (compaction disabled)...\n", totalToPrune)
		pruneFunc := appStore.PruneStores
		if parallel {
			pruneFunc = appStore.PruneStoresParallel
		}
		if err := pruneFunc(totalToPrune); err != nil {
			return fmt.Errorf("error during logical pruning: %w", err)
		}
		fmt.Println("Logical pruning complete. Run without --no-compact to reclaim disk space.")

	} else {
		// Modalità "Spazio Sicuro" (Default): ciclo a batch con compattazione.
		fmt.Printf("Starting safe batched pruning for %d versions...\n", totalToPrune)
		var prunedCount int64 = 0

		for prunedCount < totalToPrune {
			// 1. Controlla lo spazio prima di ogni batch
			fmt.Println("Checking for available disk space...")
			freeSpace, err := getFreeDiskSpace(dataDir)
			if err != nil {
				return fmt.Errorf("error checking disk space: %w", err)
			}
			if freeSpace < minFreeGB {
				return fmt.Errorf("insufficient disk space to continue (%d GB available, %d GB required). Pruning halted safely. Pruned %d/%d versions.", freeSpace, minFreeGB, prunedCount, totalToPrune)
			}

			// 2. Definisci la dimensione del batch corrente
			thisBatchSize := int64(batchSize)
			if (totalToPrune - prunedCount) < thisBatchSize {
				thisBatchSize = totalToPrune - prunedCount
			}

			// 3. Esegui il pruning logico per il batch
			fmt.Printf("--- Pruning Batch %d / ~%d ---\n", (prunedCount/batchSize)+1, totalToPrune/batchSize+1)
			fmt.Printf("Logically pruning %d versions... (Total progress: %d/%d)\n", thisBatchSize, prunedCount+thisBatchSize, totalToPrune)

			pruneFunc := appStore.PruneStores
			if parallel {
				pruneFunc = appStore.PruneStoresParallel
			}

			startTime := time.Now()
			if err := pruneFunc(thisBatchSize); err != nil {
				return fmt.Errorf("error pruning batch: %w", err)
			}
			fmt.Printf("Logical pruning for batch finished in %s.\n", time.Since(startTime))

			// 4. Esegui la compattazione per liberare spazio
			fmt.Println("Compacting database to reclaim space... This may take a while.")
			compactStartTime := time.Now()
			if err := appDB.ForceCompact(nil, nil); err != nil {
				return fmt.Errorf("error during compaction: %w", err)
			}
			fmt.Printf("Compaction for batch finished in %s.\n", time.Since(compactStartTime))

			prunedCount += thisBatchSize
		}
	}

	fmt.Println("Application state pruning process finished successfully.")
	return nil
}

// PruneCmtData rimane invariato, la sua compattazione è generalmente veloce.
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
