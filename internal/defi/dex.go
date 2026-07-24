package defi

type DexType string

const (
	AerodromeSlipstream DexType = "AerodromeSlipstream"
	AerodromeV1         DexType = "AerodromeV1"
	BalancerV2          DexType = "BalancerV2"
	BalancerV3          DexType = "BalancerV3"
	BaseSwapV2          DexType = "BaseSwapV2"
	BaseX               DexType = "BaseX"
	CamelotV3           DexType = "CamelotV3"
	CurveFinance        DexType = "CurveFinance"
	FluidDEX            DexType = "FluidDEX"
	MaverickV1          DexType = "MaverickV1"
	MaverickV2          DexType = "MaverickV2"
	PancakeSwapV3       DexType = "PancakeSwapV3"
	SushiSwapV2         DexType = "SushiSwapV2"
	SushiSwapV3         DexType = "SushiSwapV3"
	UniswapV2           DexType = "UniswapV2"
	UniswapV3           DexType = "UniswapV3"
	UniswapV4           DexType = "UniswapV4"
)

var allDexTypes = []DexType{
	AerodromeSlipstream, AerodromeV1, BalancerV2, BalancerV3, BaseSwapV2,
	BaseX, CamelotV3, CurveFinance, FluidDEX, MaverickV1, MaverickV2,
	PancakeSwapV3, SushiSwapV2, SushiSwapV3, UniswapV2, UniswapV3, UniswapV4,
}

func ParseDexType(name string) (DexType, bool) {
	for _, dexType := range allDexTypes {
		if string(dexType) == name {
			return dexType, true
		}
	}
	return "", false
}

func (d DexType) String() string { return string(d) }
