package nip5a

import (
	"fmt"
	"math/big"
	"strings"

	"fiatjaf.com/nostr"
)

func NormalizePath(p string) string {
	if !strings.HasSuffix(p, ".html") && !strings.HasSuffix(p, "/") {
		return p
	}
	if strings.HasSuffix(p, "/") {
		return p + "index.html"
	}
	return p
}

func PubKeyFromBase36(value string) (nostr.PubKey, error) {
	bi, ok := new(big.Int).SetString(value, 36)
	if !ok {
		return nostr.ZeroPK, fmt.Errorf("invalid base36 pubkey")
	}
	buf := bi.Bytes()
	if len(buf) > 32 {
		return nostr.ZeroPK, fmt.Errorf("base36 pubkey too long")
	}
	var pk nostr.PubKey
	copy(pk[32-len(buf):], buf)
	return pk, nil
}

func PubKeyToBase36(pubkey nostr.PubKey) string {
	value := new(big.Int).SetBytes(pubkey[:]).Text(36)
	return strings.Repeat("0", 50-len(value)) + value
}
