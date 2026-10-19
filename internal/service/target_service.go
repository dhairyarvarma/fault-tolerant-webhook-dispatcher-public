package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/crypto"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/outbound"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type TargetService struct {
	targets *store.TargetStore
	secrets *crypto.SecretBox
	policy  outbound.Policy
}

func NewTargetService(targets *store.TargetStore, secrets *crypto.SecretBox, allowPrivateTargets bool) *TargetService {
	return &TargetService{targets: targets, secrets: secrets, policy: outbound.NewPolicy(allowPrivateTargets)}
}

// Create validates and persists a new webhook target. The secret, if any,
// is encrypted before storage and is never returned to the caller.
func (s *TargetService) Create(ctx context.Context, name, targetURL, secret string) (store.Target, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.Target{}, validationErrorf("name is required")
	}
	if utf8.RuneCountInString(name) > 200 {
		return store.Target{}, validationErrorf("name must be at most 200 characters")
	}
	if err := validateTargetURL(ctx, s.policy, targetURL); err != nil {
		return store.Target{}, err
	}

	ciphertext, err := s.secrets.Seal(secret)
	if err != nil {
		return store.Target{}, err
	}

	return s.targets.Create(ctx, name, targetURL, ciphertext)
}

// Get returns a target by ID. Returns store.ErrNotFound if it does not exist.
func (s *TargetService) Get(ctx context.Context, id string) (store.Target, error) {
	if err := validateID(id); err != nil {
		return store.Target{}, err
	}
	return s.targets.Get(ctx, id)
}

// Disable marks a target as disabled without deleting its historical data.
func (s *TargetService) Disable(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	return s.targets.Disable(ctx, id)
}
