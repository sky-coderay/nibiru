package ante_test

import (
	sdkmath "cosmossdk.io/math"
	sdkclienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/NibiruChain/nibiru/v2/app"
	"github.com/NibiruChain/nibiru/v2/app/ante"
	"github.com/NibiruChain/nibiru/v2/x/common/testutil"
)

func (s *AnteTestSuite) TestAnteDecoratorStakingCommission() {
	// nextAnteHandler: A no-op next handler to make this a unit test.
	var nextAnteHandler sdk.AnteHandler = func(
		ctx sdk.Context, tx sdk.Tx, simulate bool,
	) (newCtx sdk.Context, err error) {
		return ctx, nil
	}

	mockDescription := stakingtypes.Description{
		Moniker:         "mock-moniker",
		Identity:        "mock-identity",
		Website:         "mock-website",
		SecurityContact: "mock-security-contact",
		Details:         "mock-details",
	}

	valAddr := sdk.ValAddress(testutil.AccAddress()).String()
	commissionRatePointer := new(sdkmath.LegacyDec)
	*commissionRatePointer = sdkmath.LegacyNewDecWithPrec(10, 2)
	happyMsgs := []sdk.Msg{
		&stakingtypes.MsgCreateValidator{
			Description: mockDescription,
			Commission: stakingtypes.CommissionRates{
				Rate:          sdkmath.LegacyNewDecWithPrec(6, 2), // 6%
				MaxRate:       sdkmath.LegacyNewDec(420),
				MaxChangeRate: sdkmath.LegacyNewDec(420),
			},
			MinSelfDelegation: sdkmath.NewInt(1),
			DelegatorAddress:  testutil.AccAddress().String(),
			ValidatorAddress:  valAddr,
			Pubkey:            &codectypes.Any{},
			Value:             sdk.NewInt64Coin("unibi", 1),
		},
		&stakingtypes.MsgEditValidator{
			Description:       mockDescription,
			ValidatorAddress:  valAddr,
			CommissionRate:    commissionRatePointer, // 10%
			MinSelfDelegation: nil,
		},
	}

	createSadMsgs := func() []sdk.Msg {
		sadMsgCreateVal := new(stakingtypes.MsgCreateValidator)
		*sadMsgCreateVal = *(happyMsgs[0]).(*stakingtypes.MsgCreateValidator)
		sadMsgCreateVal.Commission.Rate = sdkmath.LegacyNewDecWithPrec(26, 2)

		sadMsgEditVal := new(stakingtypes.MsgEditValidator)
		*sadMsgEditVal = *(happyMsgs[1]).(*stakingtypes.MsgEditValidator)

		newCommissionRate := new(sdkmath.LegacyDec)
		*newCommissionRate = sdkmath.LegacyNewDecWithPrec(26, 2)
		sadMsgEditVal.CommissionRate = newCommissionRate

		return []sdk.Msg{
			sadMsgCreateVal,
			sadMsgEditVal,
		}
	}
	sadMsgs := createSadMsgs()

	for _, tc := range []struct {
		name    string
		txMsgs  []sdk.Msg
		wantErr string
	}{
		{
			name:    "happy blank",
			txMsgs:  []sdk.Msg{},
			wantErr: "",
		},
		{
			name: "happy msgs",
			txMsgs: []sdk.Msg{
				happyMsgs[0],
				happyMsgs[1],
			},
			wantErr: "",
		},
		{
			name: "sad: max commission on create validator",
			txMsgs: []sdk.Msg{
				sadMsgs[0],
				happyMsgs[1],
			},
			wantErr: ante.ErrMaxValidatorCommission.Error(),
		},
		{
			name: "sad: max commission on edit validator",
			txMsgs: []sdk.Msg{
				happyMsgs[0],
				sadMsgs[1],
			},
			wantErr: ante.ErrMaxValidatorCommission.Error(),
		},
	} {
		s.Run(tc.name, func() {
			txGasCoins := sdk.NewCoins(
				sdk.NewCoin("unibi", sdkmath.NewInt(1_000)),
				sdk.NewCoin("utoken", sdkmath.NewInt(500)),
			)

			encCfg := app.MakeEncodingConfig()
			txBuilder, err := sdkclienttx.Factory{}.
				WithFees(txGasCoins.String()).
				WithChainID(s.ctx.ChainID()).
				WithTxConfig(encCfg.TxConfig).
				BuildUnsignedTx(tc.txMsgs...)
			s.NoError(err)

			anteDecorator := ante.AnteDecoratorStakingCommission{}
			simulate := true
			s.ctx, err = anteDecorator.AnteHandle(
				s.ctx, txBuilder.GetTx(), simulate, nextAnteHandler,
			)

			if tc.wantErr != "" {
				s.ErrorContains(err, tc.wantErr)
				return
			}
			s.NoError(err)
		})
	}
}
