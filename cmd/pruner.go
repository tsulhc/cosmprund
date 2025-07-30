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
	batchSize  = 1000 // numero versioni da potare per batch
	minFreeGB  = 20   // GB minimi richiesti prima di procedere col batch
)

// Ottiene spazio disco libero in GB
func getFreeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024 * 1024), nil
}

func PruneAppState(dataDir string) error {
	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	appDB, err := db.NewGoLevelDBWithOpts("application", dataDir, &o)
	if err != nil {
		return err
	}

	fmt.Println("Pruning application state...")

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
	totalVersions := int64(len(versions))
	numToPrune := totalVersions - int64(keepVersions)

	if numToPrune <= 0 {
		fmt.Println("No versions to prune.")
		return nil
	}

	fmt.Printf("Total versions to prune: %d\n", numToPrune)

	pruned := int64(0)
	for pruned < numToPrune {
		freeSpace, err := getFreeDiskSpace(dataDir)
		if err != nil {
			return fmt.Errorf("errore nel controllo spazio disco: %w", err)
		}
		if freeSpace < minFreeGB {
			return fmt.Errorf("spazio insufficiente sul disco (%d GB disponibili)", freeSpace)
		}

		remaining := numToPrune - pruned
		thisBatch := batchSize
		if remaining < int64(batchSize) {
			thisBatch = int(remaining)
		}

		fmt.Printf("Pruning batch of %d versions... (progress: %d/%d)\n", thisBatch, pruned+int64(thisBatch), numToPrune)
		appStore.PruneStores(thisBatch)
		pruned += int64(thisBatch)

		fmt.Println("Compacting after batch...")
		if err := appDB.Compact(nil, nil); err != nil {
			return fmt.Errorf("errore durante la compattazione: %w", err)
		}

		time.Sleep(1 * time.Second)
	}

	fmt.Println("Application state pruning complete.")
	return nil
}

// PruneCmtData prunes the cometbft blocks and state based on the amount of blocks to keep
func PruneCmtData(dataDir string) error {
	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	// Get BlockStore
	blockStoreDB, err := dbm.NewGoLevelDBWithOpts("blockstore", dataDir, &o)
	if err != nil {
		return err
	}
	blockStore := cmtstore.NewBlockStore(blockStoreDB)

	// Get StateStore
	stateDB, err := dbm.NewGoLevelDBWithOpts("state", dataDir, &o)
	if err != nil {
		return err
	}

	stateStore := state.NewStore(stateDB, state.StoreOptions{})

	base := blockStore.Base()
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
