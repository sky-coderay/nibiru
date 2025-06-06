package types

import (
	"errors"
	"fmt"

	sdkmath "cosmossdk.io/math"
)

var (
	KeyInflationEnabled      = []byte("InflationEnabled")
	KeyHasInflationStarted   = []byte("HasInflationStarted")
	KeyPolynomialFactors     = []byte("PolynomialFactors")
	KeyInflationDistribution = []byte("InflationDistribution")
	KeyEpochsPerPeriod       = []byte("EpochsPerPeriod")
	KeyPeriodsPerYear        = []byte("PeriodsPerYear")
	KeyMaxPeriod             = []byte("MaxPeriod")
)

var (
	DefaultInflation         = false
	DefaultPolynomialFactors = []sdkmath.LegacyDec{
		sdkmath.LegacyMustNewDecFromStr("-0.000147085524"),
		sdkmath.LegacyMustNewDecFromStr("0.074291982762"),
		sdkmath.LegacyMustNewDecFromStr("-18.867415611180"),
		sdkmath.LegacyMustNewDecFromStr("3128.641926954698"),
		sdkmath.LegacyMustNewDecFromStr("-334834.740631598223"),
		sdkmath.LegacyMustNewDecFromStr("17827464.906540066004"),
	}
	DefaultInflationDistribution = InflationDistribution{
		CommunityPool:     sdkmath.LegacyNewDecWithPrec(35_4825, 6), // 35.4825%
		StakingRewards:    sdkmath.LegacyNewDecWithPrec(28_1250, 6), // 28.1250%
		StrategicReserves: sdkmath.LegacyNewDecWithPrec(36_3925, 6), // 36.3925%
	}
	DefaultEpochsPerPeriod = uint64(30)
	DefaultPeriodsPerYear  = uint64(12)
	DefaultMaxPeriod       = uint64(8 * 12) // 8 years with 360 days per year
)

func NewParams(
	polynomialCalculation []sdkmath.LegacyDec,
	inflationDistribution InflationDistribution,
	inflationEnabled bool,
	hasInflationStarted bool,
	epochsPerPeriod,
	periodsPerYear,
	maxPeriod uint64,
) Params {
	return Params{
		PolynomialFactors:     polynomialCalculation,
		InflationDistribution: inflationDistribution,
		InflationEnabled:      inflationEnabled,
		HasInflationStarted:   hasInflationStarted,
		EpochsPerPeriod:       epochsPerPeriod,
		PeriodsPerYear:        periodsPerYear,
		MaxPeriod:             maxPeriod,
	}
}

// default inflation module parameters
func DefaultParams() Params {
	return Params{
		PolynomialFactors:     DefaultPolynomialFactors,
		InflationDistribution: DefaultInflationDistribution,
		InflationEnabled:      DefaultInflation,
		HasInflationStarted:   DefaultInflation,
		EpochsPerPeriod:       DefaultEpochsPerPeriod,
		PeriodsPerYear:        DefaultPeriodsPerYear,
		MaxPeriod:             DefaultMaxPeriod,
	}
}

func validatePolynomialFactors(i any) error {
	v, ok := i.([]sdkmath.LegacyDec)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if len(v) == 0 {
		return errors.New("polynomial factors cannot be empty")
	}
	return nil
}

func validateInflationDistribution(i any) error {
	v, ok := i.(InflationDistribution)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if v.StakingRewards.IsNegative() {
		return errors.New("staking distribution ratio must not be negative")
	}

	if v.CommunityPool.IsNegative() {
		return errors.New("community pool distribution ratio must not be negative")
	}

	if v.StrategicReserves.IsNegative() {
		return errors.New("pool incentives distribution ratio must not be negative")
	}

	totalProportions := v.StakingRewards.Add(v.StrategicReserves).Add(v.CommunityPool)
	if !totalProportions.Equal(sdkmath.LegacyOneDec()) {
		return errors.New("total distributions ratio should be 1")
	}

	return nil
}

func validateBool(i any) error {
	_, ok := i.(bool)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	return nil
}

func validateUint64(i any) error {
	_, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid genesis state type: %T", i)
	}
	return nil
}

func validateEpochsPerPeriod(i any) error {
	val, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if val <= 0 {
		return fmt.Errorf("epochs per period must be positive: %d", val)
	}

	return nil
}

func validatePeriodsPerYear(i any) error {
	val, ok := i.(uint64)
	if !ok {
		return fmt.Errorf("invalid parameter type: %T", i)
	}

	if val <= 0 {
		return fmt.Errorf("periods per year must be positive: %d", val)
	}

	return nil
}

func (p Params) Validate() error {
	if err := validateEpochsPerPeriod(p.EpochsPerPeriod); err != nil {
		return err
	}
	if err := validatePeriodsPerYear(p.PeriodsPerYear); err != nil {
		return err
	}
	if err := validatePolynomialFactors(p.PolynomialFactors); err != nil {
		return err
	}
	if err := validateInflationDistribution(p.InflationDistribution); err != nil {
		return err
	}
	if err := validateUint64(p.MaxPeriod); err != nil {
		return err
	}
	if err := validateBool(p.HasInflationStarted); err != nil {
		return err
	}

	return validateBool(p.InflationEnabled)
}
