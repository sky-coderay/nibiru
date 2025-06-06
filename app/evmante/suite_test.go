package evmante_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/suite"

	"github.com/NibiruChain/nibiru/v2/app"
)

type TestSuite struct {
	suite.Suite

	encCfg app.EncodingConfig
}

func TestAppTestSuite(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func (s *TestSuite) SetupSuite() {
	s.encCfg = app.MakeEncodingConfig()
}

func (s *TestSuite) TestGenesis() {
	getDefaultStakingGenesis := func() *stakingtypes.GenesisState {
		genStaking := new(stakingtypes.GenesisState)
		s.encCfg.Codec.MustUnmarshalJSON(
			app.StakingModule{}.DefaultGenesis(s.encCfg.Codec),
			genStaking,
		)
		return genStaking
	}

	gens := []*stakingtypes.GenesisState{}
	gens = append(gens, getDefaultStakingGenesis())

	genStaking := getDefaultStakingGenesis()
	genStaking.Params.MinCommissionRate = sdkmath.LegacyZeroDec()
	gens = append(gens, genStaking)

	for _, tc := range []struct {
		name    string
		gen     *stakingtypes.GenesisState
		wantErr string
	}{
		{
			name: "default should work fine",
			gen:  gens[0],
		},
		{
			name:    "zero commission should fail",
			gen:     gens[1],
			wantErr: "min_commission must be positive",
		},
	} {
		s.Run(tc.name, func() {
			genStakingJson := s.encCfg.Codec.MustMarshalJSON(tc.gen)
			err := app.StakingModule{}.ValidateGenesis(
				s.encCfg.Codec,
				s.encCfg.TxConfig,
				genStakingJson,
			)
			if tc.wantErr != "" {
				s.ErrorContains(err, tc.wantErr)
				return
			}
			s.NoError(err)
		})
	}
}
