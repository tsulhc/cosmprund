# Cosmos-Pruner

The goal of this project is to prune a CometBFT database, Cosmos SDK application state, and tx indexes while keeping the last N blocks and application versions.

Application stores are detected from the latest commit info, so chain-specific stores such as Osmosis modules are mounted automatically when they are present in the database.

## WARNING

Due to inefficiencies of iavl and the simple approach of this tool, it can take ages to prune the data of a large node.  

We are working on integrating this natively into the Cosmos-sdk and CometBFT

## How to use

Cosmprund works of a data directory that has the same structure of a normal cosmos-sdk/cometbft node. By default it will prune all but 10 blocks from cometbft, and all but 10 versions of application state.

> Note: Application pruning can take a very long time dependent on the size of the db. 


```
# clone & build cosmprund repo
git clone https://github.com/binaryholdings/cosmprund
cd cosmprund
make build

# stop daemon/cosmovisor
sudo systemctl stop cosmovisor

# run cosmprund
./build/cosmprund prune ~/.gaiad/data --blocks 10000 --versions 10000 --app cosmoshub

# inspect a Babylon database without modifying it
./build/cosmprund inspect ~/.babylond/data --profile babylon

# conservative Babylon pruning
./build/cosmprund prune ~/.babylond/data --profile babylon --blocks 121376 --versions 121376 --compact=false
```

Flags: 

- `blocks`: amount of CometBFT blocks to keep on the node (default 10)
- `versions`: amount of application state versions to keep on the node (default 10)
- `app`: application label used for logging, for example `osmosis`
- `cosmos-sdk`: set to false to skip application state pruning (default true)
- `cometbft`: set to false to skip CometBFT block/state pruning (default true)
- `tx-index`: prune `tx_index.db` and block indexes (default true)
- `compact`: compact databases after pruning (default true)
- `parallel`: prune application substores concurrently (default false)
- `profile`: pruning profile, currently useful for `babylon`
- `include-store`: comma-separated application stores to prune/inspect
- `exclude-store`: comma-separated application stores to skip
- `app-batch-versions`: application versions to prune per store batch
- `compact-every-batches`: compact application DB every N batches; `0` means once at the end
- `min-free-gb`: minimum free disk GiB required before each application pruning batch

For short maintenance windows, use `--compact=false` to perform logical pruning only. Run again with compaction enabled when reclaiming disk space is required.

The `babylon` profile uses smaller application batches, higher free-space requirements, and final-only application compaction by default. Application stores are still discovered from commit info, so Babylon custom stores are handled without hard-coded store lists.
