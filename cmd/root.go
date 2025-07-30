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
	cosmosSdk    bool
	cometbft     bool
	keepBlocks   uint64
	keepVersions uint64
	noCompact    bool // *** MODIFICA: Aggiunta variabile per il flag --no-compact ***
	parallel     bool // *** MODIFICA: Aggiunta variabile per il flag --parallel ***
	appName      = "cosmprund"
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

			if cosmosSdk {
				// *** MODIFICA: Passa i nuovi flag alla funzione PruneAppState ***
				if err := PruneAppState(dataDir, keepVersions, noCompact, parallel); err != nil {
					return err
				}
			}

			if cometbft {
				// *** MODIFICA: Passa keepBlocks alla funzione PruneCmtData ***
				if err := PruneCmtData(dataDir, keepBlocks); err != nil {
					return err
				}
			}

			return nil
		},
	}

	rootCmd.AddCommand(pruneCmd)

	// --keep-blocks flag
	pruneCmd.PersistentFlags().Uint64VarP(&keepBlocks, "keep-blocks", "b", 10, "set the amount of blocks to keep")
	if err := viper.BindPFlag("keep-blocks", pruneCmd.PersistentFlags().Lookup("keep-blocks")); err != nil {
		panic(err)
	}

	// --keep-versions flag
	pruneCmd.PersistentFlags().Uint64VarP(&keepVersions, "keep-versions", "v", 10, "set the amount of versions to keep in the application store")
	if err := viper.BindPFlag("keep-versions", pruneCmd.PersistentFlags().Lookup("keep-versions")); err != nil {
		panic(err)
	}

	// --cosmos-sdk flag
	pruneCmd.PersistentFlags().BoolVar(&cosmosSdk, "cosmos-sdk", true, "set to false if using only with cometbft")
	if err := viper.BindPFlag("cosmos-sdk", pruneCmd.PersistentFlags().Lookup("cosmos-sdk")); err != nil {
		panic(err)
	}

	// --cometbft flag
	pruneCmd.PersistentFlags().BoolVar(&cometbft, "cometbft", true, "set to false you dont want to prune cometbft data")
	if err := viper.BindPFlag("cometbft", pruneCmd.PersistentFlags().Lookup("cometbft")); err != nil {
		panic(err)
	}

	// *** MODIFICA: Definizione del flag --no-compact ***
	pruneCmd.PersistentFlags().BoolVar(&noCompact, "no-compact", false, "Disable database compaction after pruning application state")
	if err := viper.BindPFlag("no-compact", pruneCmd.PersistentFlags().Lookup("no-compact")); err != nil {
		panic(err)
	}

	// *** MODIFICA: Definizione del flag --parallel ***
	pruneCmd.PersistentFlags().BoolVar(¶llel, "parallel", false, "Enable parallel pruning for the application state")
	if err := viper.BindPFlag("parallel", pruneCmd.PersistentFlags().Lookup("parallel")); err != nil {
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

func Execute() {
	cobra.EnableCommandSorting = false

	rootCmd := NewRootCmd()
	rootCmd.SilenceUsage = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
