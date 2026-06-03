package cmd

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

type PruneOptions struct {
	App                 string
	Profile             string
	KeepBlocks          uint64
	KeepVersions        uint64
	Compact             bool
	Parallel            bool
	TxIndex             bool
	IncludeStores       []string
	ExcludeStores       []string
	AppBatchVersions    uint64
	CompactEveryBatches uint64
	MinFreeGB           uint64
}

type appStoreContext struct {
	db         *db.GoLevelDB
	store      *rootmulti.Store
	storeNames []string
	latest     int64
}

func (opts *PruneOptions) applyProfile() {
	if strings.EqualFold(opts.Profile, "babylon") || strings.EqualFold(opts.App, "babylon") {
		if opts.App == "" {
			opts.App = "babylon"
		}
		if opts.AppBatchVersions == batchSize {
			opts.AppBatchVersions = 50
		}
		if opts.CompactEveryBatches == 1 {
			opts.CompactEveryBatches = 0
		}
		if opts.MinFreeGB == minFreeGB {
			opts.MinFreeGB = 100
		}
	}
}

func csvList(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func stringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

func getFreeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024 * 1024), nil
}

func loadAppStore(dataDir string, opts PruneOptions) (*appStoreContext, error) {
	o := opt.Options{
		DisableSeeksCompaction: true,
	}

	appDB, err := db.NewGoLevelDBWithOpts("application", dataDir, &o)
	if err != nil {
		return nil, err
	}

	appStore := rootmulti.NewStore(appDB, log.NewLogger(os.Stderr), metrics.NewNoOpMetrics())
	latest := rootmulti.GetLatestVersion(appDB)
	storeNames := []string{}
	if latest != 0 {
		cInfo, err := appStore.GetCommitInfo(latest)
		if err != nil {
			appDB.Close()
			return nil, err
		}
		for _, storeInfo := range cInfo.StoreInfos {
			if len(storeInfo.CommitId.Hash) > 0 {
				storeNames = append(storeNames, storeInfo.Name)
			} else {
				fmt.Println("Skipping", storeInfo.Name, "store due to empty hash")
			}
		}
	}

	storeNames = filterStoreNames(storeNames, opts.IncludeStores, opts.ExcludeStores)
	keys := types.NewKVStoreKeys(storeNames...)
	for _, value := range keys {
		appStore.MountStoreWithDB(value, types.StoreTypeIAVL, nil)
	}
	if err := appStore.LoadLatestVersion(); err != nil {
		appDB.Close()
		return nil, err
	}

	return &appStoreContext{
		db:         appDB,
		store:      appStore,
		storeNames: storeNames,
		latest:     latest,
	}, nil
}

func filterStoreNames(storeNames []string, includeStores, excludeStores []string) []string {
	include := stringSet(includeStores)
	exclude := stringSet(excludeStores)
	filtered := make([]string, 0, len(storeNames))

	for _, storeName := range storeNames {
		if len(include) > 0 && !include[storeName] {
			continue
		}
		if exclude[storeName] {
			continue
		}
		filtered = append(filtered, storeName)
	}

	sort.Strings(filtered)
	return filtered
}

func dirSize(path string) uint64 {
	var size uint64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil && info.Size() > 0 {
			size += uint64(info.Size())
		}
		return nil
	})
	return size
}

func formatBytes(size uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func PruneAppState(dataDir string, opts PruneOptions) error {
	if opts.KeepVersions == 0 {
		return fmt.Errorf("versions must be greater than 0")
	}
	if opts.AppBatchVersions == 0 {
		return fmt.Errorf("app-batch-versions must be greater than 0")
	}

	ctx, err := loadAppStore(dataDir, opts)
	if err != nil {
		return err
	}
	defer ctx.db.Close()

	if opts.App != "" {
		fmt.Println("Application label:", opts.App)
	}
	if opts.Profile != "" {
		fmt.Println("Profile:", opts.Profile)
	}
	if opts.Parallel {
		fmt.Println("Parallel app pruning is disabled in store-by-store mode; pruning stores sequentially.")
	}

	if len(ctx.storeNames) == 0 {
		fmt.Println("No application stores selected for pruning.")
		return nil
	}

	allVersions := ctx.store.GetAllVersions()
	if uint64(len(allVersions)) <= opts.KeepVersions {
		fmt.Printf("No application versions to prune. found=%d keep=%d\n", len(allVersions), opts.KeepVersions)
		return nil
	}

	totalToPrune := len(allVersions) - int(opts.KeepVersions)
	finalPruneHeight := int64(allVersions[totalToPrune-1])
	fmt.Printf("Application versions found=%d keep=%d prune_count=%d prune_to=%d stores=%d\n", len(allVersions), opts.KeepVersions, totalToPrune, finalPruneHeight, len(ctx.storeNames))

	batchNumber := uint64(0)
	for _, storeName := range ctx.storeNames {
		storeStart := time.Now()
		fmt.Printf("=== Pruning application store %s ===\n", storeName)
		prunedCount := 0

		for prunedCount < totalToPrune {
			freeSpace, err := getFreeDiskSpace(dataDir)
			if err != nil {
				return fmt.Errorf("error checking disk space: %w", err)
			}
			if freeSpace < opts.MinFreeGB {
				return fmt.Errorf("insufficient disk space to continue (%d GB available, %d GB required). Pruning halted safely at store %s, progress %d/%d versions", freeSpace, opts.MinFreeGB, storeName, prunedCount, totalToPrune)
			}

			thisBatchSize := int(opts.AppBatchVersions)
			if totalToPrune-prunedCount < thisBatchSize {
				thisBatchSize = totalToPrune - prunedCount
			}
			batchPruneHeight := int64(allVersions[prunedCount+thisBatchSize-1])

			batchNumber++
			fmt.Printf("Store %s batch %d: pruning to version %d (%d/%d)\n", storeName, batchNumber, batchPruneHeight, prunedCount+thisBatchSize, totalToPrune)
			startTime := time.Now()
			if err := ctx.store.PruneStore(storeName, batchPruneHeight); err != nil {
				return fmt.Errorf("error pruning store %s to version %d: %w", storeName, batchPruneHeight, err)
			}
			fmt.Printf("Store %s batch %d finished in %s.\n", storeName, batchNumber, time.Since(startTime))

			prunedCount += thisBatchSize
			if opts.Compact && opts.CompactEveryBatches > 0 && batchNumber%opts.CompactEveryBatches == 0 {
				if err := compactApplicationDB(ctx.db); err != nil {
					return err
				}
			}
		}
		fmt.Printf("Store %s pruning finished in %s.\n", storeName, time.Since(storeStart))
	}

	if opts.Compact && opts.CompactEveryBatches == 0 {
		if err := compactApplicationDB(ctx.db); err != nil {
			return err
		}
	} else if !opts.Compact {
		fmt.Println("Logical pruning complete. Run with --compact=true to reclaim disk space.")
	}

	fmt.Println("Application state pruning process finished successfully.")
	return nil
}

func compactApplicationDB(appDB *db.GoLevelDB) error {
	fmt.Println("Compacting application database to reclaim space... This may take a while.")
	compactStartTime := time.Now()
	if err := appDB.ForceCompact(nil, nil); err != nil {
		return fmt.Errorf("error during application compaction: %w", err)
	}
	fmt.Printf("Application compaction finished in %s.\n", time.Since(compactStartTime))
	return nil
}

func InspectData(dataDir string, opts PruneOptions) error {
	opts.applyProfile()
	freeSpace, err := getFreeDiskSpace(dataDir)
	if err != nil {
		return fmt.Errorf("error checking disk space: %w", err)
	}

	fmt.Println("cosmprund inspect")
	fmt.Println("Data directory:", dataDir)
	if opts.App != "" {
		fmt.Println("Application label:", opts.App)
	}
	if opts.Profile != "" {
		fmt.Println("Profile:", opts.Profile)
	}
	fmt.Printf("Free disk: %d GiB\n", freeSpace)

	for _, name := range []string{"application", "blockstore", "state", "tx_index"} {
		path := filepath.Join(dataDir, name+".db")
		fmt.Printf("DB %-12s %s\n", name, formatBytes(dirSize(path)))
	}

	blockStoreDB, err := dbm.NewGoLevelDBWithOpts("blockstore", dataDir, &opt.Options{DisableSeeksCompaction: true})
	if err == nil {
		blockStore := cmtstore.NewBlockStore(blockStoreDB)
		fmt.Printf("CometBFT block base=%d height=%d\n", blockStore.Base(), blockStore.Height())
		blockStore.Close()
	}

	ctx, err := loadAppStore(dataDir, opts)
	if err != nil {
		return err
	}
	defer ctx.db.Close()

	fmt.Printf("Application latest version=%d selected_stores=%d\n", ctx.latest, len(ctx.storeNames))
	for _, storeName := range ctx.storeNames {
		versions, err := ctx.store.GetStoreVersions(storeName)
		if err != nil {
			fmt.Printf("Store %-24s error=%s\n", storeName, err)
			continue
		}
		if len(versions) == 0 {
			fmt.Printf("Store %-24s versions=0\n", storeName)
			continue
		}
		fmt.Printf("Store %-24s versions=%d first=%d latest=%d\n", storeName, len(versions), versions[0], versions[len(versions)-1])
	}

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
