package defi

import (
	"fmt"
	"strings"

	"github.com/upcomers-org/platformgo/internal/ids"
)

func ValidateAddress(address string) error {
	if !strings.HasPrefix(address, "0x") {
		return fmt.Errorf("Ethereum address must start with '0x': %s", address)
	}
	if len(address) != 42 {
		return fmt.Errorf("Blockchain address '%s' is incorrect: invalid string length", address)
	}
	for position, character := range address[2:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return fmt.Errorf(
				"Blockchain address '%s' is incorrect: invalid character '%c' at position %d",
				address, character, position,
			)
		}
	}
	if err := ids.ValidateBlockchainAddress(address); err != nil {
		return fmt.Errorf("Blockchain address '%s' has incorrect checksum", address)
	}
	return nil
}
