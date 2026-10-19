// Package delivery implements safe outbound HTTP delivery.
package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/crypto"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/outbound"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/signature"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type Client struct {
	httpClient              *http.Client
	responseCaptureMaxBytes int64
	policy                  outbound.Policy
	secrets                 *crypto.SecretBox
}

func (c *Client) RequestTimeout() time.Duration { return c.httpClient.Timeout }

func NewClient(connectTimeout, requestTimeout time.Duration, responseCaptureMaxBytes int64, allowPrivateTargets bool, secrets ...*crypto.SecretBox) *Client {
	policy := outbound.NewPolicy(allowPrivateTargets)
	policy.ConnectTimeout = connectTimeout
	client := &Client{
		httpClient: &http.Client{
			Timeout: requestTimeout,
			// Redirects are intentionally not followed. Besides eliminating a
			// redirect-based SSRF path, this prevents net/http from converting a
			// webhook POST into a GET after a 301/302 response.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				DialContext: policy.DialContext,
			},
		},
		responseCaptureMaxBytes: responseCaptureMaxBytes,
		policy:                  policy,
	}
	if len(secrets) > 0 {
		client.secrets = secrets[0]
	}
	return client
}

// Result is one delivery attempt's outcome. ErrorMessage is bounded and
// never contains the raw downstream response body, per SECURITY_MODEL.md.
type Result struct {
	Success      bool
	HTTPStatus   *int
	StartedAt    time.Time
	FinishedAt   time.Time
	ErrorClass   *string
	ErrorMessage *string
	RetryAfter   string
}

// Deliver sends the payload with stable identity and signs configured targets.
func (c *Client) Deliver(ctx context.Context, target store.Target, event store.Event) Result {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()
	if _, err := c.policy.ValidateURL(ctx, target.URL); err != nil {
		if !errors.Is(err, outbound.ErrDisallowedDestination) {
			return failure(started, "network_error", "destination resolution failed")
		}
		return failure(started, "outbound_policy", "destination rejected by outbound policy")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(event.PayloadJSON))
	if err != nil {
		return failure(started, "invalid_request", "outbound request is invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", event.IdempotencyKey)
	req.Header.Set("X-Webhook-Id", event.ID)
	if len(target.SecretCiphertext) > 0 {
		if c.secrets == nil {
			return failure(started, "signing_error", "target signing secret unavailable")
		}
		secret, err := c.secrets.Open(target.SecretCiphertext)
		if err != nil {
			return failure(started, "signing_error", "target signing secret unavailable")
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("X-Webhook-Timestamp", timestamp)
		req.Header.Set("X-Webhook-Signature", signature.Sign(secret, timestamp, event.ID, event.PayloadJSON))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// http.Client errors may include the full target URL. Keep durable
		// attempt history and logs free of embedded credentials or private URLs.
		return failure(started, "network_error", "outbound request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, c.responseCaptureMaxBytes))

	status := resp.StatusCode
	finished := time.Now()
	if status >= 200 && status < 300 {
		return Result{Success: true, HTTPStatus: &status, StartedAt: started, FinishedAt: finished}
	}

	msg := fmt.Sprintf("target responded with status %d", status)
	errClass := "http_error"
	var retryAfter string
	if values := resp.Header.Values("Retry-After"); len(values) == 1 && len(values[0]) <= 128 {
		retryAfter = values[0]
	}
	return Result{
		Success:      false,
		HTTPStatus:   &status,
		StartedAt:    started,
		FinishedAt:   finished,
		ErrorClass:   &errClass,
		ErrorMessage: &msg,
		RetryAfter:   retryAfter,
	}
}

func failure(started time.Time, class, message string) Result {
	return Result{
		Success:      false,
		StartedAt:    started,
		FinishedAt:   time.Now(),
		ErrorClass:   &class,
		ErrorMessage: &message,
	}
}
