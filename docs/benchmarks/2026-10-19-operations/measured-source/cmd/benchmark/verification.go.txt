package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func acceptedRequests(data []byte) (int64, error) {
	var summary struct {
		Root struct {
			Checks map[string]struct{ Passes int64 } `json:"checks"`
		} `json:"root_group"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return 0, fmt.Errorf("invalid k6 acceptance summary")
	}
	count := summary.Root.Checks["event accepted"].Passes
	if count <= 0 {
		return 0, fmt.Errorf("k6 summary has no accepted events")
	}
	return count, nil
}

func verifyDelivery(ctx context.Context, drain time.Duration, expected int64,
	query func(context.Context) (deliveryReport, error), save func(deliveryReport) error,
) (string, error) {
	budget := ctx
	if drain > 0 {
		var cancel context.CancelFunc
		budget, cancel = context.WithTimeout(ctx, drain)
		defer cancel()
	}
	for {
		queryCtx, cancel := context.WithTimeout(budget, 5*time.Second)
		report, err := query(queryCtx)
		cancel()
		if ctx.Err() != nil {
			return "canceled", ctx.Err()
		}
		if err != nil {
			if budget.Err() != nil {
				return "drain_timeout", budget.Err()
			}
			return "query_failed", fmt.Errorf("delivery query failed")
		}
		if err := save(report); err != nil {
			return "artifact_failed", fmt.Errorf("cannot retain delivery snapshot")
		}
		if expected <= 0 || report.Accepted != expected {
			return "count_mismatch", fmt.Errorf("SQL accepted %d events; k6 accepted %d", report.Accepted, expected)
		}
		if report.Pending == 0 {
			if report.Succeeded != expected || report.DLQ != 0 || report.Canceled != 0 {
				return "terminal_failures", fmt.Errorf("delivery ended with %d succeeded, %d DLQ, %d canceled", report.Succeeded, report.DLQ, report.Canceled)
			}
			return "verified", nil
		}
		if drain == 0 {
			return "drain_timeout", context.DeadlineExceeded
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-budget.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return "canceled", ctx.Err()
			}
			return "drain_timeout", budget.Err()
		case <-timer.C:
		}
	}
}
