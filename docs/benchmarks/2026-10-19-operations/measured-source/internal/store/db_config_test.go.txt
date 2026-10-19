package store

import "testing"

func TestOpenRejectsMalformedURLWithoutEchoingCredentials(t *testing.T) {
	for _, raw := range []string{
		"postgres://user:secret-sentinel@host:bad-port/db",
		"postgres://user:secret-sentinel%zz@localhost/db",
		"password='secret-sentinel",
	} {
		db, err := Open(raw)
		if db != nil {
			db.Close()
			t.Fatal("malformed URL opened a pool")
		}
		if err == nil || err.Error() != "invalid DATABASE_URL" {
			t.Fatal("expected static configuration error")
		}
	}
}
