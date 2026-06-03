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

For short maintenance windows, use `--compact=false` to perform logical pruning only. Run again with compaction enabled when reclaiming disk space is required.
