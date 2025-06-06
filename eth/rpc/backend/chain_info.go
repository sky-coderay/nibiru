// Copyright (c) 2023-2024 Nibi, Inc.
package backend

import (
	"fmt"
	"math/big"

	cmtrpcclient "github.com/cometbft/cometbft/rpc/client"
	tmrpctypes "github.com/cometbft/cometbft/rpc/core/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethmath "github.com/ethereum/go-ethereum/common/math"
	gethcore "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
	pkgerrors "github.com/pkg/errors"

	"github.com/NibiruChain/nibiru/v2/eth"
	"github.com/NibiruChain/nibiru/v2/eth/rpc"
	"github.com/NibiruChain/nibiru/v2/x/evm"
)

// ChainID is the EIP-155 replay-protection chain id for the current ethereum chain config.
func (b *Backend) ChainID() *hexutil.Big {
	return (*hexutil.Big)(eth.ParseEthChainID(b.clientCtx.ChainID))
}

// ChainConfig returns the latest ethereum chain configuration
func (b *Backend) ChainConfig() *params.ChainConfig {
	_, err := b.queryClient.Params(b.ctx, &evm.QueryParamsRequest{})
	if err != nil {
		return nil
	}
	return evm.EthereumConfig(b.chainID)
}

// BaseFeeWei returns the EIP-1559 base fee.
// If the base fee is not enabled globally, the query returns nil.
func (b *Backend) BaseFeeWei(
	blockRes *tmrpctypes.ResultBlockResults,
) (baseFeeWei *big.Int, err error) {
	res, err := b.queryClient.BaseFee(rpc.NewContextWithHeight(blockRes.Height), &evm.QueryBaseFeeRequest{})
	if err != nil || res.BaseFee == nil {
		return nil, pkgerrors.Wrap(err, "failed to query base fee")
	}
	return res.BaseFee.BigInt(), nil
}

// CurrentHeader returns the latest block header
// This will return error as per node configuration
// if the ABCI responses are discarded ('discard_abci_responses' config param)
func (b *Backend) CurrentHeader() (*gethcore.Header, error) {
	return b.HeaderByNumber(rpc.EthLatestBlockNumber)
}

// PendingTransactions returns the transactions that are in the transaction pool
// and have a from address that is one of the accounts this node manages.
func (b *Backend) PendingTransactions() ([]*sdk.Tx, error) {
	mc, ok := b.clientCtx.Client.(cmtrpcclient.MempoolClient)
	if !ok {
		return nil, pkgerrors.New("invalid rpc client")
	}

	res, err := mc.UnconfirmedTxs(b.ctx, nil)
	if err != nil {
		return nil, err
	}

	result := make([]*sdk.Tx, 0, len(res.Txs))
	for _, txBz := range res.Txs {
		tx, err := b.clientCtx.TxConfig.TxDecoder()(txBz)
		if err != nil {
			return nil, err
		}
		result = append(result, &tx)
	}

	return result, nil
}

// FeeHistory returns data relevant for fee estimation based on the specified range of blocks.
func (b *Backend) FeeHistory(
	userBlockCount gethmath.HexOrDecimal64, // number blocks to fetch, maximum is 100
	lastBlock gethrpc.BlockNumber, // the block to start search, to oldest
	rewardPercentiles []float64, // percentiles to fetch reward
) (*rpc.FeeHistoryResult, error) {
	blockEnd := int64(lastBlock) //#nosec G701 -- checked for int overflow already

	if blockEnd < 0 {
		blockNumber, err := b.BlockNumber()
		if err != nil {
			return nil, err
		}
		blockEnd = int64(blockNumber) //#nosec G701 -- checked for int overflow already
	}

	blocks := int64(userBlockCount)                     // #nosec G701 -- checked for int overflow already
	maxBlockCount := int64(b.cfg.JSONRPC.FeeHistoryCap) // #nosec G701 -- checked for int overflow already
	if blocks > maxBlockCount {
		return nil, fmt.Errorf("FeeHistory user block count %d higher than %d", blocks, maxBlockCount)
	}

	if blockEnd+1 < blocks {
		blocks = blockEnd + 1
	}
	// Ensure not trying to retrieve before genesis.
	blockStart := blockEnd + 1 - blocks
	oldestBlock := (*hexutil.Big)(big.NewInt(blockStart))

	// prepare space
	reward := make([][]*hexutil.Big, blocks)
	rewardCount := len(rewardPercentiles)
	for i := 0; i < int(blocks); i++ {
		reward[i] = make([]*hexutil.Big, rewardCount)
	}

	thisBaseFee := make([]*hexutil.Big, blocks+1)
	thisGasUsedRatio := make([]float64, blocks)

	// rewards should only be calculated if reward percentiles were included
	calculateRewards := rewardCount != 0

	// fetch block
	for blockID := blockStart; blockID <= blockEnd; blockID++ {
		index := int32(blockID - blockStart) // #nosec G701
		// tendermint block
		tendermintblock, err := b.TendermintBlockByNumber(rpc.BlockNumber(blockID))
		if tendermintblock == nil {
			return nil, err
		}

		// eth block
		ethBlock, err := b.GetBlockByNumber(rpc.BlockNumber(blockID), true)
		if ethBlock == nil {
			return nil, err
		}

		// tendermint block result
		tendermintBlockResult, err := b.TendermintBlockResultByNumber(&tendermintblock.Block.Height)
		if tendermintBlockResult == nil {
			b.logger.Debug("block result not found", "height", tendermintblock.Block.Height, "error", err.Error())
			return nil, err
		}

		oneFeeHistory := rpc.OneFeeHistory{}
		err = b.retrieveEVMTxFeesFromBlock(tendermintblock, &ethBlock, rewardPercentiles, tendermintBlockResult, &oneFeeHistory)
		if err != nil {
			return nil, err
		}

		// copy
		thisBaseFee[index] = (*hexutil.Big)(oneFeeHistory.BaseFee)
		thisBaseFee[index+1] = (*hexutil.Big)(oneFeeHistory.NextBaseFee)
		thisGasUsedRatio[index] = oneFeeHistory.GasUsedRatio
		if calculateRewards {
			for j := 0; j < rewardCount; j++ {
				reward[index][j] = (*hexutil.Big)(oneFeeHistory.Reward[j])
				if reward[index][j] == nil {
					reward[index][j] = (*hexutil.Big)(big.NewInt(0))
				}
			}
		}
	}

	feeHistory := rpc.FeeHistoryResult{
		OldestBlock:  oldestBlock,
		BaseFee:      thisBaseFee,
		GasUsedRatio: thisGasUsedRatio,
	}

	if calculateRewards {
		feeHistory.Reward = reward
	}

	return &feeHistory, nil
}

// SuggestGasTipCap Not yet supported. Returns 0 as the suggested tip cap.
// After implementing tx prioritization, this function can come to life.
func (b *Backend) SuggestGasTipCap(baseFee *big.Int) (*big.Int, error) {
	maxDelta := big.NewInt(0)
	return maxDelta, nil
}

// GlobalMinGasPrice returns the minimum gas price for all nodes.
// This is distinct from the individual configuration set by the validator set.
func (b *Backend) GlobalMinGasPrice() (*big.Int, error) {
	// TODO: feat(eth): dynamic fees
	return big.NewInt(0), nil
}
