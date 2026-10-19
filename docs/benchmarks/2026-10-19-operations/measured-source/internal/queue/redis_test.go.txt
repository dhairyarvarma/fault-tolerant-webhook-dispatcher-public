package queue

import "testing"

func TestOpenRejectsMalformedURLWithoutEchoingCredentials(t *testing.T) {
	for _, raw := range []string{
		"redis://user:secret-sentinel@host:bad-port/0",
		"redis://user:secret-sentinel%zz@localhost/0",
		"redis://localhost/secret-sentinel",
	} {
		client, err := Open(raw)
		if client != nil {
			client.Close()
			t.Fatal("malformed URL opened a client")
		}
		if err == nil || err.Error() != "invalid REDIS_URL" {
			t.Fatal("expected static configuration error")
		}
	}
}
