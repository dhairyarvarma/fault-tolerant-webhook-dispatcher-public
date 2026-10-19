// Package signature implements the outbound webhook signature contract.
package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

func digest(secret, timestamp, id string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + id + "."))
	mac.Write(body)
	return mac.Sum(nil)
}

func Sign(secret, timestamp, id string, body []byte) string {
	return "v1=" + hex.EncodeToString(digest(secret, timestamp, id, body))
}

// Verify checks the exact received bytes and rejects timestamps outside tolerance.
func Verify(secret, timestamp, id string, body []byte, header string, now time.Time, tolerance time.Duration) bool {
	if secret == "" || id == "" || tolerance <= 0 || !strings.HasPrefix(header, "v1=") {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(seconds, 0))
	if age > tolerance || age < -tolerance {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(header, "v1="))
	if err != nil {
		return false
	}
	return hmac.Equal(provided, digest(secret, timestamp, id, body))
}
