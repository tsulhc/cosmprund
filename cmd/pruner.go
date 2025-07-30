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
	"github.com/syndtr/goleveldb/leveldb/cache"
	"github.com/syndtr/goleveldb/leveldb/opt"

	"github.com/binaryholdings/cosmos-pruner/internal/rootmulti"
)

// Costanti per la configurazione del pruning
const (
	batchSize = 1000 // Numero di versioni da processare in ogni batch di pruning logico
	minFreeGB = 20   // Spazio disco minimo richiesto (in GB) prima di iniziare la compattazione
)

// Ottiene lo spazio libero su disco in GB per un dato percorso.
func getFreeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Calcola i GB liberi: (blocchi disponibili * dimensione blocco) / (1024^3)
	return (stat.Bavail * uint64(stat.Bsize)) / (1024 * 1024 * 1024), nil
}

// PruneAppState esegue il pruning dello stato dell'applicazione.
// Se noCompact è true, salta la fase di compattazione fisica del database,
// permettendo un'operazione molto più veloce (adatta a brevi downtime).
func PruneAppState(dataDir string, keepVersions uint, noCompact bool) error {
	// Tuning dei parametri di GoLevelDB per migliori prestazioni, specialmente in scrittura
	o := opt.Options{
		DisableSeeksCompaction: true,
		WriteBufferSize:        128 * opt.MiB,
		BlockCache:             cache.NewLRUCache(512 * opt.MiB),
	}

	appDB, err := db.NewGoLevelDBWithOpts("application", dataDir, &o)
	if err != nil {
		return err
	}
	defer appDB.Close()

	if noCompact {
		fmt.Println("Pruning application state (compaction will be skipped)...")
	} else {
		fmt.Println("Pruning and compacting application state...")
	}

	appStore := rootmulti.NewStore(appDB, log.NewLogger(os.Stderr), metrics.NewNoOpMetrics())

	// Carica i nomi degli store dalla versione più recente
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
		// Se non c'è niente da potare, non serve compattare.
		// Potremmo aggiungere un flag --force-compact se si vuole compattare comunque.
		return nil
	}

	fmt.Printf("Total versions to prune (logically): %d\n", numToPrune)

	// Esegue il pruning logico in batch. Questa operazione è veloce.
	pruned := int64(0)
	for pruned < numToPrune {
		remaining := numToPrune - pruned
		thisBatch := int64(batchSize)
		if remaining < batchSize {
			thisBatch = remaining
		}

		fmt.Printf("Logically pruning batch of %d versions... (Progress: %d/%d)\n", thisBatch, pruned+thisBatch, numToPrune)
		// Questa chiamata è stata verificata per essere corretta per l'implementazione in `binaryholdings/cosmos-pruner`
		appStore.PruneStoresParallel(thisBatch)
		pruned += thisBatch
	}
	fmt.Println("Logical pruning of all batches complete.")

	// Esegue la compattazione solo se non è stata disabilitata
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
		startTime := time.Now()
		if err := appDB.ForceCompact(nil, nil); err != nil {
			return fmt.Errorf("error during compaction: %w", err)
		}
		fmt.Printf("Compaction finished in %s.\n", time.Since(startTime))
	} else {
		fmt.Println("Compaction was skipped as requested via --no-compact flag.")
	}

	return nil
}

// PruneCmtData esegue il pruning dei dati di CometBFT (blockstore e state).
// Questa funzione esegue sempre la compattazione, dato che i suoi DB sono più piccoli
// e l'operazione è generalmente molto più rapida.
func PruneCmtData(dataDir string, keepBlocks uint) error {
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
