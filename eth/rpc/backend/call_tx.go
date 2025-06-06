// Copyright (c) 2023-2024 Nibi, Inc.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	sdkioerrors "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethcore "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	pkgerrors "github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NibiruChain/nibiru/v2/eth/rpc"
	"github.com/NibiruChain/nibiru/v2/x/evm"
)

// SendRawTransaction send a raw Ethereum transaction.
func (b *Backend) SendRawTransaction(data hexutil.Bytes) (common.Hash, error) {
	// RLP decode raw transaction bytes
	tx := &gethcore.Transaction{}
	if err := tx.UnmarshalBinary(data); err != nil {
		b.logger.Error("transaction decoding failed", "error", err.Error())
		return common.Hash{}, err
	}

	// check the local node config in case unprotected txs are disabled
	if !b.allowUnprotectedTxs && !tx.Protected() {
		// Ensure only eip155 signed transactions are submitted if EIP155Required is set.
		return common.Hash{}, pkgerrors.New("only replay-protected (EIP-155) transactions allowed over RPC")
	}

	ethereumTx := &evm.MsgEthereumTx{}
	if err := ethereumTx.FromEthereumTx(tx); err != nil {
		b.logger.Error("transaction converting failed", "error", err.Error())
		return common.Hash{}, err
	}

	if err := ethereumTx.ValidateBasic(); err != nil {
		b.logger.Debug("tx failed basic validation", "error", err.Error())
		return common.Hash{}, err
	}

	cosmosTx, err := ethereumTx.BuildTx(b.clientCtx.TxConfig.NewTxBuilder(), evm.EVMBankDenom)
	if err != nil {
		b.logger.Error("failed to build cosmos tx", "error", err.Error())
		return common.Hash{}, err
	}

	// Encode transaction by default Tx encoder
	txBytes, err := b.clientCtx.TxConfig.TxEncoder()(cosmosTx)
	if err != nil {
		b.logger.Error("failed to encode eth tx using default encoder", "error", err.Error())
		return common.Hash{}, err
	}

	txHash := ethereumTx.AsTransaction().Hash()
	b.logger.Debug("eth_sendRawTransaction",
		"txHash", txHash.Hex(),
	)

	syncCtx := b.clientCtx.WithBroadcastMode(flags.BroadcastSync)
	rsp, err := syncCtx.BroadcastTx(txBytes)
	if rsp != nil && rsp.Code != 0 {
		err = sdkioerrors.ABCIError(rsp.Codespace, rsp.Code, rsp.RawLog)
	}
	if err != nil {
		b.logger.Error("failed to broadcast tx", "error", err.Error())
		return txHash, err
	}
	b.logger.Debug("eth_sendRawTransaction",
		"blockHeight", fmt.Sprintf("%d", rsp.Height),
	)

	return txHash, nil
}

// SetTxDefaults populates tx message with default values in case they are not
// provided on the args
func (b *Backend) SetTxDefaults(args evm.JsonTxArgs) (evm.JsonTxArgs, error) {
	if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
		return args, pkgerrors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}

	head, _ := b.CurrentHeader() // #nosec G703 -- no need to check error cause we're already checking that head == nil
	if head == nil {
		return args, pkgerrors.New("latest header is nil")
	}

	// If user specifies both maxPriorityfee and maxFee, then we do not
	// need to consult the chain for defaults.
	if args.MaxPriorityFeePerGas == nil || args.MaxFeePerGas == nil {
		// In this clause, user left some fields unspecified.
		if head.BaseFee != nil && args.GasPrice == nil {
			if args.MaxPriorityFeePerGas == nil {
				tip, err := b.SuggestGasTipCap(head.BaseFee)
				if err != nil {
					return args, err
				}
				args.MaxPriorityFeePerGas = (*hexutil.Big)(tip)
			}

			if args.MaxFeePerGas == nil {
				gasFeeCap := new(big.Int).Add(
					(*big.Int)(args.MaxPriorityFeePerGas),
					new(big.Int).Mul(head.BaseFee, big.NewInt(2)),
				)
				args.MaxFeePerGas = (*hexutil.Big)(gasFeeCap)
			}

			if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
				return args, fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
			}
		} else {
			if args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil {
				return args, pkgerrors.New("maxFeePerGas or maxPriorityFeePerGas specified but london is not active yet")
			}

			if args.GasPrice == nil {
				price, err := b.SuggestGasTipCap(head.BaseFee)
				if err != nil {
					return args, err
				}
				if head.BaseFee != nil {
					// The legacy tx gas price suggestion should not add 2x base fee
					// because all fees are consumed, so it would result in a spiral
					// upwards.
					price.Add(price, head.BaseFee)
				}
				args.GasPrice = (*hexutil.Big)(price)
			}
		}
	} else {
		// Both maxPriorityfee and maxFee set by caller. Sanity-check their internal relation
		if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
			return args, fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
		}
	}

	if args.Value == nil {
		args.Value = new(hexutil.Big)
	}
	if args.Nonce == nil && args.From != nil {
		// get the nonce from the account retriever
		// ignore error in case the account doesn't exist yet
		nonce, _ := b.getAccountNonce(*args.From, true, 0, b.logger) // #nosec G703s
		args.Nonce = (*hexutil.Uint64)(&nonce)
	}

	if args.Data != nil && args.Input != nil && !bytes.Equal(*args.Data, *args.Input) {
		return args, pkgerrors.New("both 'data' and 'input' are set and not equal. Please use 'input' to pass transaction call data")
	}

	if args.To == nil {
		// Contract creation
		var input []byte
		if args.Data != nil {
			input = *args.Data
		} else if args.Input != nil {
			input = *args.Input
		}

		if len(input) == 0 {
			return args, pkgerrors.New("contract creation without any data provided")
		}
	}

	if args.Gas == nil {
		// For backwards-compatibility reason, we try both input and data
		// but input is preferred.
		input := args.Input
		if input == nil {
			input = args.Data
		}

		callArgs := evm.JsonTxArgs{
			From:                 args.From,
			To:                   args.To,
			Gas:                  args.Gas,
			GasPrice:             args.GasPrice,
			MaxFeePerGas:         args.MaxFeePerGas,
			MaxPriorityFeePerGas: args.MaxPriorityFeePerGas,
			Value:                args.Value,
			Data:                 input,
			AccessList:           args.AccessList,
			ChainID:              args.ChainID,
			Nonce:                args.Nonce,
		}

		blockNr := rpc.EthPendingBlockNumber
		estimated, err := b.EstimateGas(callArgs, &blockNr)
		if err != nil {
			return args, err
		}
		args.Gas = &estimated
		b.logger.Debug("estimate gas usage automatically", "gas", args.Gas)
	}

	if args.ChainID == nil {
		args.ChainID = (*hexutil.Big)(b.chainID)
	}

	return args, nil
}

// EstimateGas returns an estimate of gas usage for the given smart contract call.
func (b *Backend) EstimateGas(
	args evm.JsonTxArgs, blockNrOptional *rpc.BlockNumber,
) (hexutil.Uint64, error) {
	blockNr := rpc.EthPendingBlockNumber
	if blockNrOptional != nil {
		blockNr = *blockNrOptional
	}

	bz, err := json.Marshal(&args)
	if err != nil {
		return 0, err
	}

	header, err := b.TendermintBlockByNumber(blockNr)
	if err != nil {
		// the error message imitates geth behavior
		return 0, pkgerrors.New("header not found")
	}

	req := evm.EthCallRequest{
		Args:            bz,
		GasCap:          b.RPCGasCap(),
		ProposerAddress: sdk.ConsAddress(header.Block.ProposerAddress),
		ChainId:         b.chainID.Int64(),
	}

	// From ContextWithHeight: if the provided height is 0,
	// it will return an empty context and the gRPC query will use
	// the latest block height for querying.
	res, err := b.queryClient.EstimateGas(rpc.NewContextWithHeight(blockNr.Int64()), &req)
	if err != nil {
		return 0, err
	}
	return hexutil.Uint64(res.Gas), nil
}

// DoCall performs a simulated call operation through the evmtypes. It returns the
// estimated gas used on the operation or an error if fails.
func (b *Backend) DoCall(
	args evm.JsonTxArgs, blockNr rpc.BlockNumber,
) (*evm.MsgEthereumTxResponse, error) {
	bz, err := json.Marshal(&args)
	if err != nil {
		return nil, err
	}
	header, err := b.TendermintBlockByNumber(blockNr)
	if err != nil {
		// the error message imitates geth behavior
		return nil, pkgerrors.New("header not found")
	}

	req := evm.EthCallRequest{
		Args:            bz,
		GasCap:          b.RPCGasCap(),
		ProposerAddress: sdk.ConsAddress(header.Block.ProposerAddress),
		ChainId:         b.chainID.Int64(),
	}

	// From ContextWithHeight: if the provided height is 0,
	// it will return an empty context and the gRPC query will use
	// the latest block height for querying.
	ctx := rpc.NewContextWithHeight(blockNr.Int64())
	timeout := b.RPCEVMTimeout()

	// Setup context so it may be canceled the call has completed
	// or, in case of unmetered gas, setup a context with a timeout.
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	// Make sure the context is canceled when the call has completed
	// this makes sure resources are cleaned up.
	defer cancel()

	res, err := b.queryClient.EthCall(ctx, &req)
	if err != nil {
		return nil, err
	}

	if res.Failed() {
		if res.VmError == vm.ErrExecutionReverted.Error() {
			return nil, evm.NewRevertError(res.Ret)
		}
		return nil, status.Error(codes.Internal, res.VmError)
	}

	return res, nil
}

// GasPrice returns the current gas price based on Ethermint's gas price oracle.
func (b *Backend) GasPrice() (*hexutil.Big, error) {
	var (
		result *big.Int
		err    error
	)

	head, err := b.CurrentHeader()
	if err != nil {
		return nil, err
	}

	if head.BaseFee != nil {
		result, err = b.SuggestGasTipCap(head.BaseFee)
		if err != nil {
			return nil, err
		}
		result = result.Add(result, head.BaseFee)
	} else {
		result = big.NewInt(b.RPCMinGasPrice())
	}

	// return at least GlobalMinGasPrice
	minGasPrice, err := b.GlobalMinGasPrice()
	if err != nil {
		return nil, err
	}
	minGasPriceInt := minGasPrice
	if result.Cmp(minGasPriceInt) < 0 {
		result = minGasPriceInt
	}

	return (*hexutil.Big)(result), nil
}

func (b *Backend) ClientCtx() client.Context {
	return b.clientCtx
}
