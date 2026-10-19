package signature

import (
	"testing"
	"time"
)

func TestVerifyRejectsTamperingAndExpiredTimestamps(t *testing.T) {
	now := time.Unix(1700000000, 0)
	body := []byte(`{"amount":25}`)
	header := Sign("secret", "1700000000", "event", body)
	if !Verify("secret", "1700000000", "event", body, header, now, time.Minute) {
		t.Fatal("valid signature rejected")
	}
	for _, tc := range []struct {
		secret, timestamp, id, header string
		body                          []byte
		now                           time.Time
	}{
		{"wrong", "1700000000", "event", header, body, now},
		{"secret", "1700000000", "other", header, body, now},
		{"secret", "1700000000", "event", header, []byte(`{"amount":26}`), now},
		{"secret", "1700000000", "event", header, body, now.Add(2 * time.Minute)},
		{"secret", "1700000000", "event", header, body, now.Add(-2 * time.Minute)},
		{"secret", "invalid", "event", header, body, now},
		{"secret", "1700000000", "event", "v1=nothex", body, now},
		{"secret", "1700000000", "event", "v2=00", body, now},
	} {
		if Verify(tc.secret, tc.timestamp, tc.id, tc.body, tc.header, tc.now, time.Minute) {
			t.Errorf("accepted invalid signature: %+v", tc)
		}
	}
}
