package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmtcli "github.com/cometbft/cometbft/libs/cli"
	cmtrand "github.com/cometbft/cometbft/libs/rand"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	sdkflags "github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/input"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	"github.com/cosmos/go-bip39"
	ibcwasmtypes "github.com/cosmos/ibc-go/modules/light-clients/08-wasm/types"
	ibcexported "github.com/cosmos/ibc-go/v7/modules/core/exported"
	ibctypes "github.com/cosmos/ibc-go/v7/modules/core/types"
	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/NibiruChain/nibiru/v2/app/appconst"
)

const (
	// FlagOverwrite defines a flag to overwrite an existing genesis JSON file.
	FlagOverwrite = "overwrite"

	// FlagSeed defines a flag to initialize the private validator key from a specific seed.
	FlagRecover = "recover"

	// FlagDefaultBondDenom defines the default denom to use in the genesis file.
	FlagDefaultBondDenom = "default-denom"
)

/*
InitCmd is a stand-in replacement for genutilcli.InitCmd that overwrites the
consensus configutation in the `config.toml` prior to writing it to the disk.
This helps keep consistency on between nodes without requiring extra work as
much manual work for node operators.

InitCmd returns a command that initializes all files needed for Tendermint
and the respective application.

Intended usage:

	```go
	import  genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	import "github.com/spf13/cobra"

	func initRootCmd(rootCmd *cobra.Command, encodingConfig app.EncodingConfig) {
		a := appCreator{encodingConfig}
		rootCmd.AddCommand(
			InitCmd(app.ModuleBasics, app.DefaultNodeHome),
		)
	}
	```
*/
func InitCmd(mbm module.BasicManager, defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [moniker]",
		Short: "Initialize private validator, p2p, genesis, and application configuration files",
		Long:  `Initialize validators's and node's configuration files.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			cdc := clientCtx.Codec

			serverCtx := server.GetServerContextFromCmd(cmd)
			config := serverCtx.Config
			config.SetRoot(clientCtx.HomeDir)

			chainID, _ := cmd.Flags().GetString(sdkflags.FlagChainID)
			switch {
			case chainID != "":
			case clientCtx.ChainID != "":
				chainID = clientCtx.ChainID
			default:
				chainID = fmt.Sprintf("test-chain-%v", cmtrand.Str(6))
			}

			// Get bip39 mnemonic
			var mnemonic string
			recover, _ := cmd.Flags().GetBool(FlagRecover)
			if recover {
				inBuf := bufio.NewReader(cmd.InOrStdin())
				value, err := input.GetString("Enter your bip39 mnemonic", inBuf)
				if err != nil {
					return err
				}

				mnemonic = value
				if !bip39.IsMnemonicValid(mnemonic) {
					return pkgerrors.New("invalid mnemonic")
				}
			}

			// Get initial height
			initHeight, _ := cmd.Flags().GetInt64(sdkflags.FlagInitHeight)
			if initHeight < 1 {
				initHeight = 1
			}

			nodeID, _, err := genutil.InitializeNodeValidatorFilesFromMnemonic(config, mnemonic)
			if err != nil {
				return err
			}

			config.Moniker = args[0]

			genFile := config.GenesisFile()
			overwrite, _ := cmd.Flags().GetBool(FlagOverwrite)
			defaultDenom, _ := cmd.Flags().GetString(FlagDefaultBondDenom)

			// use os.Stat to check if the file exists
			_, err = os.Stat(genFile)
			if !overwrite && !os.IsNotExist(err) {
				return fmt.Errorf("genesis.json file already exists: %v", genFile)
			}

			// Overwrites the SDK default denom for side-effects
			if defaultDenom != "" {
				sdk.DefaultBondDenom = defaultDenom
			}
			appGenState := mbm.DefaultGenesis(cdc)

			// add 08-wasm to AllowedClients
			ibcState := ibctypes.DefaultGenesisState()
			ibcState.ClientGenesis.Params.AllowedClients = append(ibcState.ClientGenesis.Params.AllowedClients, ibcwasmtypes.Wasm)
			appGenState[ibcexported.ModuleName] = cdc.MustMarshalJSON(ibcState)

			appState, err := json.MarshalIndent(appGenState, "", " ")
			if err != nil {
				return pkgerrors.Wrap(err, "Failed to marshal default genesis state")
			}

			genDoc := &cmttypes.GenesisDoc{}
			if _, err := os.Stat(genFile); err != nil {
				if !os.IsNotExist(err) {
					return err
				}
			} else {
				genDoc, err = cmttypes.GenesisDocFromFile(genFile)
				if err != nil {
					return pkgerrors.Wrap(err, "Failed to read genesis doc from file")
				}
			}

			genDoc.ChainID = chainID
			genDoc.Validators = nil
			genDoc.AppState = appState
			genDoc.InitialHeight = initHeight

			if err = genutil.ExportGenesisFile(genDoc, genFile); err != nil {
				return pkgerrors.Wrap(err, "Failed to export genesis file")
			}

			toPrint := newPrintInfo(config.Moniker, chainID, nodeID, "", appState)

			customCfg := appconst.NewDefaultTendermintConfig()
			config.Consensus = customCfg.Consensus
			config.DBBackend = customCfg.DBBackend
			cmtcfg.WriteConfigFile(filepath.Join(config.RootDir, "config", "config.toml"), config)
			return displayInfo(toPrint)
		},
	}

	cmd.Flags().String(cmtcli.HomeFlag, defaultNodeHome, "node's home directory")
	cmd.Flags().BoolP(FlagOverwrite, "o", false, "overwrite the genesis.json file")
	cmd.Flags().Bool(FlagRecover, false, "provide seed phrase to recover existing key instead of creating")
	cmd.Flags().String(sdkflags.FlagChainID, "", "genesis file chain-id, if left blank will be randomly created")
	cmd.Flags().String(FlagDefaultBondDenom, "", "genesis file default denomination, if left blank default value is 'stake'")
	cmd.Flags().Int64(sdkflags.FlagInitHeight, 1, "specify the initial block height at genesis")

	return cmd
}

type printInfo struct {
	Moniker    string          `json:"moniker" yaml:"moniker"`
	ChainID    string          `json:"chain_id" yaml:"chain_id"`
	NodeID     string          `json:"node_id" yaml:"node_id"`
	GenTxsDir  string          `json:"gentxs_dir" yaml:"gentxs_dir"`
	AppMessage json.RawMessage `json:"app_message" yaml:"app_message"`
}

func newPrintInfo(moniker, chainID, nodeID, genTxsDir string, appMessage json.RawMessage) printInfo {
	return printInfo{
		Moniker:    moniker,
		ChainID:    chainID,
		NodeID:     nodeID,
		GenTxsDir:  genTxsDir,
		AppMessage: appMessage,
	}
}

func displayInfo(info printInfo) error {
	out, err := json.MarshalIndent(info, "", " ")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(os.Stderr, "%s\n", sdk.MustSortJSON(out))

	return err
}
