package macaddress

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"github.com/vitistack/proxmox-operator/internal/consts"
)

type vitstackmacaddress struct {
}

func NewVitistackMacGenerator() MacAddressGenerator {
	return &vitstackmacaddress{}
}

func (v *vitstackmacaddress) GetMACAddress() (string, error) {
	macAddr, err := generateMacAddress()
	if err != nil {
		return "", err
	}

	return macAddr, nil
}

func generateMacAddress() (string, error) {
	// Generate a 6-byte MAC address using crypto/rand
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil { // #nosec G404: crypto/rand used (not math/rand)
		return "", err
	}

	// First octet must be 0x02 (locally administered, unicast)
	b[0] = 0x02

	// Second octet from config (MAC_SET) as hex; default to 0x12
	setStr := strings.TrimSpace(viper.GetString(consts.MAC_SET))
	if setStr == "" {
		setStr = "12"
	}
	// Allow optional 0x prefix and normalize to hex
	setStr = strings.TrimPrefix(strings.ToLower(setStr), "0x")
	if len(setStr) > 2 {
		// keep only last two hex digits if longer
		setStr = setStr[len(setStr)-2:]
	}
	if _, err := strconv.ParseUint(setStr, 16, 8); err != nil {
		// fallback to default on parse error
		setStr = "12"
	}
	val, _ := strconv.ParseUint(setStr, 16, 8)
	b[1] = byte(val)

	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5]), nil
}
