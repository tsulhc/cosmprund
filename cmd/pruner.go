package cmd

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
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

func PruneAppState(dataDir string, keepVersions uint64, compact, parallel bool, app string) error {
	if keepVersions == 0 {
		return fmt.Errorf("versions must be greater than 0")
	}

	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	appDB, err := db.NewGoLevelDBWithOpts("application", dataDir, &o)
	if err != nil {
		return err
	}
	defer appDB.Close()

	if app != "" {
		fmt.Println("Application label:", app)
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

	allVersions := appStore.GetAllVersions()
	if uint64(len(allVersions)) <= keepVersions {
		fmt.Printf("No application versions to prune. found=%d keep=%d\n", len(allVersions), keepVersions)
		return nil
	}

	totalToPrune := len(allVersions) - int(keepVersions)
	finalPruneHeight := int64(allVersions[totalToPrune-1])
	fmt.Printf("Application versions found=%d keep=%d prune_count=%d prune_to=%d\n", len(allVersions), keepVersions, totalToPrune, finalPruneHeight)

	pruneFunc := appStore.PruneStores
	if parallel {
		pruneFunc = appStore.PruneStoresParallel
	}

	if !compact {
		fmt.Printf("Pruning application versions logically to %d (compaction disabled)...\n", finalPruneHeight)
		if err := pruneFunc(finalPruneHeight); err != nil {
			return fmt.Errorf("error during logical pruning: %w", err)
		}
		fmt.Println("Logical pruning complete. Run with --compact=true to reclaim disk space.")

	} else {
		fmt.Printf("Starting safe batched application pruning for %d versions...\n", totalToPrune)
		prunedCount := 0

		for prunedCount < totalToPrune {
			fmt.Println("Checking for available disk space...")
			freeSpace, err := getFreeDiskSpace(dataDir)
			if err != nil {
				return fmt.Errorf("error checking disk space: %w", err)
			}
			if freeSpace < minFreeGB {
				return fmt.Errorf("insufficient disk space to continue (%d GB available, %d GB required). Pruning halted safely. Pruned %d/%d versions.", freeSpace, minFreeGB, prunedCount, totalToPrune)
			}

			thisBatchSize := batchSize
			if totalToPrune-prunedCount < thisBatchSize {
				thisBatchSize = totalToPrune - prunedCount
			}
			batchPruneHeight := int64(allVersions[prunedCount+thisBatchSize-1])

			fmt.Printf("--- Pruning Batch %d / ~%d ---\n", (prunedCount/batchSize)+1, totalToPrune/batchSize+1)
			fmt.Printf("Logically pruning to version %d... (Total progress: %d/%d)\n", batchPruneHeight, prunedCount+thisBatchSize, totalToPrune)

			startTime := time.Now()
			if err := pruneFunc(batchPruneHeight); err != nil {
				return fmt.Errorf("error pruning batch: %w", err)
			}
			fmt.Printf("Logical pruning for batch finished in %s.\n", time.Since(startTime))

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

func PruneCmtData(dataDir string, keepBlocks uint64, compact bool) error {
	if keepBlocks == 0 {
		return fmt.Errorf("blocks must be greater than 0")
	}

	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	blockStoreDB, err := dbm.NewGoLevelDBWithOpts("blockstore", dataDir, &o)
	if err != nil {
		return err
	}
	blockStore := cmtstore.NewBlockStore(blockStoreDB)
	defer blockStore.Close()

	stateDB, err := dbm.NewGoLevelDBWithOpts("state", dataDir, &o)
	if err != nil {
		return err
	}
	defer stateDB.Close()
	stateStore := state.NewStore(stateDB, state.StoreOptions{})

	base := blockStore.Base()
	if blockStore.Height() <= int64(keepBlocks) {
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

	if compact {
		fmt.Println("Compacting block store...")
		if err := blockStoreDB.Compact(nil, nil); err != nil {
			return err
		}
	}

	fmt.Println("Pruning state store...")
	err = stateStore.PruneStates(base, pruneHeight, evidencePoint)
	if err != nil {
		return err
	}

	if compact {
		fmt.Println("Compacting state store...")
		if err := stateDB.Compact(nil, nil); err != nil {
			return err
		}
	}

	fmt.Println("CometBFT pruning complete.")
	return nil
}

func PruneTxIndex(dataDir string, keepBlocks uint64, compact bool) error {
	if keepBlocks == 0 {
		return fmt.Errorf("blocks must be greater than 0")
	}

	o := opt.Options{
		DisableSeeksCompaction: true,
	}
	txIndexDB, err := dbm.NewGoLevelDBWithOpts("tx_index", dataDir, &o)
	if err != nil {
		return err
	}
	defer txIndexDB.Close()

	blockStoreDB, err := dbm.NewGoLevelDBWithOpts("blockstore", dataDir, &o)
	if err != nil {
		return err
	}
	blockStore := cmtstore.NewBlockStore(blockStoreDB)
	defer blockStore.Close()

	pruneHeight := blockStore.Height() - int64(keepBlocks) - 10
	if pruneHeight <= 0 {
		fmt.Printf("No tx_index entries to prune. prune_height=%d\n", pruneHeight)
		return nil
	}

	fmt.Printf("Pruning tx_index.db to height %d...\n", pruneHeight)
	if err := pruneTxIndexDB(txIndexDB, pruneHeight); err != nil {
		return err
	}

	if compact {
		fmt.Println("Compacting tx_index.db...")
		if err := txIndexDB.Compact(nil, nil); err != nil {
			return err
		}
	}

	fmt.Println("tx_index pruning complete.")
	return nil
}

func pruneTxIndexDB(txIndexDB dbm.DB, pruneHeight int64) error {
	itr, err := txIndexDB.Iterator(nil, nil)
	if err != nil {
		return err
	}
	defer itr.Close()

	batch := txIndexDB.NewBatch()
	deletes := 0
	totalDeletes := 0

	for ; itr.Valid(); itr.Next() {
		key := copyBytes(itr.Key())
		value := copyBytes(itr.Value())

		for _, deleteKey := range txIndexKeysToDelete(key, value, pruneHeight) {
			if err := batch.Delete(deleteKey); err != nil {
				batch.Close()
				return err
			}
			deletes++
			totalDeletes++
		}

		if deletes >= batchSize {
			if err := batch.Write(); err != nil {
				batch.Close()
				return err
			}
			batch.Close()
			batch = txIndexDB.NewBatch()
			deletes = 0
		}
	}

	if deletes > 0 {
		if err := batch.Write(); err != nil {
			batch.Close()
			return err
		}
	}
	batch.Close()

	fmt.Printf("Deleted %d tx_index keys.\n", totalDeletes)
	return nil
}

func txIndexKeysToDelete(key, value []byte, pruneHeight int64) [][]byte {
	strKey := string(key)

	if strings.HasPrefix(strKey, "tx.height") {
		parts := strings.Split(strKey, "/")
		if len(parts) < 3 {
			return nil
		}
		height, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil && height < pruneHeight {
			if len(value) == 0 {
				return [][]byte{key}
			}
			return [][]byte{key, value}
		}
		return nil
	}

	if strings.HasPrefix(strKey, "block.height") || strings.HasPrefix(strKey, "block_events") {
		if int64FromBytes(value) < pruneHeight {
			return [][]byte{key}
		}
		return nil
	}

	if len(value) == 32 {
		parts := strings.Split(strKey, "/")
		if len(parts) != 4 {
			return nil
		}
		height, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil && height < pruneHeight {
			return [][]byte{key}
		}
	}

	return nil
}

func int64FromBytes(bz []byte) int64 {
	v, _ := binary.Varint(bz)
	return v
}

func copyBytes(bz []byte) []byte {
	cpy := make([]byte, len(bz))
	copy(cpy, bz)
	return cpy
}
