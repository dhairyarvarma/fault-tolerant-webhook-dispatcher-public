package service

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/outbound"
)

func validateID(id string) error {
	var value pgtype.UUID
	if err := value.Scan(id); err != nil || !value.Valid {
		return validationErrorf("invalid resource ID")
	}
	return nil
}

// validateTargetURL prevents unsafe destinations from entering durable target
// configuration. Delivery repeats this policy at connection time.
func validateTargetURL(ctx context.Context, policy outbound.Policy, raw string) error {
	if raw == "" {
		return validationErrorf("url is required")
	}
	if _, err := policy.ValidateURL(ctx, raw); err != nil {
		if errors.Is(err, outbound.ErrDisallowedDestination) {
			return validationErrorf("url is not permitted by outbound policy")
		}
		return validationErrorf("url validation failed")
	}
	return nil
}
