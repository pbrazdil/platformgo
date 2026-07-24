package ids

import (
	"encoding/json"
	"fmt"
	"strings"
)

const SyntheticVenue = "SYNTH"

type Blockchain string
type DEXType string

var blockchainNames = func() map[string]Blockchain {
	names := []string{
		"Abstract", "Arbitrum", "ArbitrumNova", "ArbitrumSepolia", "Aurora",
		"Avalanche", "Base", "BaseSepolia", "Berachain", "BerachainBartio",
		"Blast", "BlastSepolia", "Boba", "Bsc", "BscTestnet", "Celo", "Chiliz",
		"CitreaTestnet", "Curtis", "Cyber", "Darwinia", "Ethereum", "Fantom",
		"Flare", "Fraxtal", "Fuji", "GaladrielDevnet", "Gnosis", "GnosisChiado",
		"GnosisTraces", "HarmonyShard0", "Holesky", "HoleskyTokenTest",
		"Hyperliquid", "HyperliquidTemp", "Ink", "InternalTestChain", "Kroma",
		"Linea", "Lisk", "Lukso", "LuksoTestnet", "Manta", "Mantle",
		"MegaethTestnet", "Merlin", "Metall2", "Metis", "MevCommit", "Mode",
		"MonadTestnet", "MonadTestnetBackup", "MoonbaseAlpha", "Moonbeam", "Morph",
		"MorphHolesky", "Opbnb", "Optimism", "OptimismSepolia", "PharosDevnet",
		"Polygon", "PolygonAmoy", "PolygonZkEvm", "Rootstock", "Saakuru", "Scroll",
		"Sepolia", "ShimmerEvm", "Soneium", "Sophon", "SophonTestnet", "Superseed",
		"Unichain", "UnichainSepolia", "Xdc", "XdcTestnet", "Zeta", "Zircuit",
		"ZKsync", "Zora",
	}
	result := make(map[string]Blockchain, len(names))
	for _, name := range names {
		result[strings.ToLower(name)] = Blockchain(name)
	}
	return result
}()

var dexNames = map[string]DEXType{
	"AerodromeSlipstream": "AerodromeSlipstream",
	"AerodromeV1":         "AerodromeV1",
	"BalancerV2":          "BalancerV2",
	"BalancerV3":          "BalancerV3",
	"BaseSwapV2":          "BaseSwapV2",
	"BaseX":               "BaseX",
	"CamelotV3":           "CamelotV3",
	"CurveFinance":        "CurveFinance",
	"FluidDEX":            "FluidDEX",
	"MaverickV1":          "MaverickV1",
	"MaverickV2":          "MaverickV2",
	"PancakeSwapV3":       "PancakeSwapV3",
	"SushiSwapV2":         "SushiSwapV2",
	"SushiSwapV3":         "SushiSwapV3",
	"UniswapV2":           "UniswapV2",
	"UniswapV3":           "UniswapV3",
	"UniswapV4":           "UniswapV4",
}

// Venue identifies a trading venue, including validated Chain:DEX venues.
type Venue string

func ParseVenue(value string) (Venue, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	if strings.Contains(value, ":") {
		if err := ValidateBlockchainVenue(value); err != nil {
			return "", &ValidationError{
				Kind:    "predicate_violation",
				Message: fmt.Sprintf("Error creating `Venue` from '%s': %s", value, err),
			}
		}
	}
	return Venue(value), nil
}

func MustVenue(value string) Venue {
	venue, err := ParseVenue(value)
	panicOnError(err)
	return venue
}

func UncheckedVenue(value string) Venue { return Venue(value) }
func SyntheticVenueID() Venue           { return MustVenue(SyntheticVenue) }
func (venue Venue) String() string      { return string(venue) }
func (venue Venue) IsSynthetic() bool   { return venue == SyntheticVenue }
func (venue Venue) IsDEX() bool         { return strings.Contains(string(venue), ":") }

func (venue Venue) ParseDEX() (Blockchain, DEXType, error) {
	chainName, dexName, ok := splitFirst(string(venue), ":")
	if !ok {
		return "", "", fmt.Errorf(
			"Venue '%s' is not a DEX venue (expected format 'Chain:DexId')",
			venue,
		)
	}
	chain, ok := blockchainNames[strings.ToLower(chainName)]
	if !ok {
		return "", "", fmt.Errorf("Invalid chain '%s' in venue '%s'", chainName, venue)
	}
	dex, ok := dexNames[dexName]
	if !ok {
		return "", "", fmt.Errorf("Invalid DEX '%s' in venue '%s'", dexName, venue)
	}
	return chain, dex, nil
}

func (venue Venue) MarshalJSON() ([]byte, error) {
	return marshalJSONString(string(venue))
}
func (venue *Venue) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseVenue(value)
	if err != nil {
		return err
	}
	*venue = parsed
	return nil
}

func ValidateBlockchainVenue(value string) error {
	chainName, dexName, ok := splitFirst(value, ":")
	if !ok || chainName == "" || dexName == "" {
		return &ValidationError{
			Kind: "predicate_violation",
			Message: fmt.Sprintf(
				"invalid blockchain venue '%s': expected format 'Chain:DexId'",
				value,
			),
		}
	}
	if _, ok := blockchainNames[strings.ToLower(chainName)]; !ok {
		return &ValidationError{
			Kind: "predicate_violation",
			Message: fmt.Sprintf(
				"invalid blockchain venue '%s': chain '%s' not recognized",
				value,
				chainName,
			),
		}
	}
	if _, ok := dexNames[dexName]; !ok {
		return &ValidationError{
			Kind: "predicate_violation",
			Message: fmt.Sprintf(
				"invalid blockchain venue '%s': dex '%s' not recognized",
				value,
				dexName,
			),
		}
	}
	return nil
}
