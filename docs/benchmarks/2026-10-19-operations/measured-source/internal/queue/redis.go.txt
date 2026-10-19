// Package queue wraps Redis Streams as the asynchronous work transport
// between the outbox relay and the worker pool. Redis is transport only,
// never the durable record of an event; callers always re-check PostgreSQL
// before acting on a message.
package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func Open(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, errors.New("invalid REDIS_URL")
	}
	return redis.NewClient(opts), nil
}

// EnsureConsumerGroup creates the stream and consumer group if they don't
// already exist. Safe to call on every startup.
func EnsureConsumerGroup(ctx context.Context, client *redis.Client, stream, group string) error {
	err := client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}
