package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	Version string
	Commit  string
)

var (
	cosmosSdk           bool
	cometbft            bool
	blocks              uint64
	versions            uint64
	compact             bool
	parallel            bool
	txIndex             bool
	app                 string
	profile             string
	includeStore        string
	excludeStore        string
	appBatchVersions    uint64
	compactEveryBatches uint64
	minFreeDiskGB       uint64
	skipDiskCheck       bool
	appName             = "cosmprund"
)

func NewRootCmd() *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:     "cosmprund",
		Short:   "cosmprund cleans up databases of Cosmos SDK applications, removing historical data generally not needed for validator nodes",
		Version: Version,
	}

	pruneCmd := &cobra.Command{
		Use:   "prune <data_dir>",
		Short: "Prune database stores",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dataDir := args[0]
			opts := pruneOptionsFromFlags()

			if cosmosSdk {
				if err := PruneAppState(dataDir, opts); err != nil {
					return err
				}
			}

			if cometbft {
				if err := PruneCmtData(dataDir, blocks, compact); err != nil {
					return err
				}
			}

			if txIndex {
				if err := PruneTxIndex(dataDir, blocks, compact); err != nil {
					return err
				}
			}

			return nil
		},
	}

	inspectCmd := &cobra.Command{
		Use:   "inspect <data_dir>",
		Short: "Inspect database stores without pruning",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return InspectData(args[0], pruneOptionsFromFlags())
		},
	}

	rootCmd.AddCommand(pruneCmd, inspectCmd)

	pruneFlags := pruneCmd.PersistentFlags()
	inspectFlags := inspectCmd.PersistentFlags()

	pruneFlags.Uint64VarP(&blocks, "blocks", "b", 10, "set the amount of blocks to keep")
	if err := viper.BindPFlag("blocks", pruneFlags.Lookup("blocks")); err != nil {
		panic(err)
	}

	pruneFlags.Uint64VarP(&versions, "versions", "v", 10, "set the amount of versions to keep in the application store")
	if err := viper.BindPFlag("versions", pruneFlags.Lookup("versions")); err != nil {
		panic(err)
	}

	for _, flags := range []struct {
		name string
		set  func()
	}{
		{name: "prune", set: func() {
			pruneFlags.StringVar(&app, "app", "", "application label for logging, e.g. babylon")
			pruneFlags.StringVar(&profile, "profile", "", "pruning profile, e.g. babylon")
			pruneFlags.StringVar(&includeStore, "include-store", "", "comma-separated application stores to prune")
			pruneFlags.StringVar(&excludeStore, "exclude-store", "", "comma-separated application stores to skip")
			pruneFlags.Uint64Var(&appBatchVersions, "app-batch-versions", 1000, "application versions to prune per store batch")
			pruneFlags.Uint64Var(&compactEveryBatches, "compact-every-batches", 1, "compact application DB every N batches; 0 means once at the end")
			pruneFlags.Uint64Var(&minFreeDiskGB, "min-free-gb", 20, "minimum free disk GiB required before each application pruning batch")
			pruneFlags.BoolVar(&skipDiskCheck, "skip-disk-check", false, "skip free disk space checks before application pruning batches")
		}},
		{name: "inspect", set: func() {
			inspectFlags.StringVar(&app, "app", "", "application label for logging, e.g. babylon")
			inspectFlags.StringVar(&profile, "profile", "", "pruning profile, e.g. babylon")
			inspectFlags.StringVar(&includeStore, "include-store", "", "comma-separated application stores to inspect")
			inspectFlags.StringVar(&excludeStore, "exclude-store", "", "comma-separated application stores to skip")
		}},
	} {
		_ = flags.name
		flags.set()
	}

	if err := viper.BindPFlag("app", pruneFlags.Lookup("app")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("profile", pruneFlags.Lookup("profile")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("include-store", pruneFlags.Lookup("include-store")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("exclude-store", pruneFlags.Lookup("exclude-store")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("app-batch-versions", pruneFlags.Lookup("app-batch-versions")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("compact-every-batches", pruneFlags.Lookup("compact-every-batches")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("min-free-gb", pruneFlags.Lookup("min-free-gb")); err != nil {
		panic(err)
	}
	if err := viper.BindPFlag("skip-disk-check", pruneFlags.Lookup("skip-disk-check")); err != nil {
		panic(err)
	}

	pruneFlags.BoolVar(&cosmosSdk, "cosmos-sdk", true, "set to false if using only with cometbft")
	if err := viper.BindPFlag("cosmos-sdk", pruneFlags.Lookup("cosmos-sdk")); err != nil {
		panic(err)
	}

	pruneFlags.BoolVar(&cometbft, "cometbft", true, "set to false you dont want to prune cometbft data")
	if err := viper.BindPFlag("cometbft", pruneFlags.Lookup("cometbft")); err != nil {
		panic(err)
	}

	pruneFlags.BoolVar(&compact, "compact", true, "compact databases after pruning")
	if err := viper.BindPFlag("compact", pruneFlags.Lookup("compact")); err != nil {
		panic(err)
	}

	pruneFlags.BoolVar(&parallel, "parallel", false, "Enable parallel pruning for the application state")
	if err := viper.BindPFlag("parallel", pruneFlags.Lookup("parallel")); err != nil {
		panic(err)
	}

	pruneFlags.BoolVar(&txIndex, "tx-index", true, "prune tx_index.db and block indexes")
	if err := viper.BindPFlag("tx-index", pruneFlags.Lookup("tx-index")); err != nil {
		panic(err)
	}

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Output extended version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("cosmprund version " + Version + " (commit " + Commit + ")")
		},
	}
	rootCmd.AddCommand(versionCmd)

	return rootCmd
}

func pruneOptionsFromFlags() PruneOptions {
	opts := PruneOptions{
		App:                 app,
		Profile:             profile,
		KeepBlocks:          blocks,
		KeepVersions:        versions,
		Compact:             compact,
		Parallel:            parallel,
		TxIndex:             txIndex,
		IncludeStores:       csvList(includeStore),
		ExcludeStores:       csvList(excludeStore),
		AppBatchVersions:    appBatchVersions,
		CompactEveryBatches: compactEveryBatches,
		MinFreeGB:           minFreeDiskGB,
		SkipDiskCheck:       skipDiskCheck,
	}
	opts.applyProfile()
	return opts
}

func Execute() {
	cobra.EnableCommandSorting = false

	rootCmd := NewRootCmd()
	rootCmd.SilenceUsage = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
