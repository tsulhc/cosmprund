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
	blocks       uint64
	versions     uint64
	compact      bool
	parallel     bool
	txIndex      bool
	app          string
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
				if err := PruneAppState(dataDir, versions, compact, parallel, app); err != nil {
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

	rootCmd.AddCommand(pruneCmd)

	pruneCmd.PersistentFlags().Uint64VarP(&blocks, "blocks", "b", 10, "set the amount of blocks to keep")
	if err := viper.BindPFlag("blocks", pruneCmd.PersistentFlags().Lookup("blocks")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().Uint64VarP(&versions, "versions", "v", 10, "set the amount of versions to keep in the application store")
	if err := viper.BindPFlag("versions", pruneCmd.PersistentFlags().Lookup("versions")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().StringVar(&app, "app", "", "application label for logging, e.g. osmosis")
	if err := viper.BindPFlag("app", pruneCmd.PersistentFlags().Lookup("app")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().BoolVar(&cosmosSdk, "cosmos-sdk", true, "set to false if using only with cometbft")
	if err := viper.BindPFlag("cosmos-sdk", pruneCmd.PersistentFlags().Lookup("cosmos-sdk")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().BoolVar(&cometbft, "cometbft", true, "set to false you dont want to prune cometbft data")
	if err := viper.BindPFlag("cometbft", pruneCmd.PersistentFlags().Lookup("cometbft")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().BoolVar(&compact, "compact", true, "compact databases after pruning")
	if err := viper.BindPFlag("compact", pruneCmd.PersistentFlags().Lookup("compact")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().BoolVar(&parallel, "parallel", false, "Enable parallel pruning for the application state")
	if err := viper.BindPFlag("parallel", pruneCmd.PersistentFlags().Lookup("parallel")); err != nil {
		panic(err)
	}

	pruneCmd.PersistentFlags().BoolVar(&txIndex, "tx-index", true, "prune tx_index.db and block indexes")
	if err := viper.BindPFlag("tx-index", pruneCmd.PersistentFlags().Lookup("tx-index")); err != nil {
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
