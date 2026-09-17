package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	StreamName = "webhooker:deliveries"
	GroupName  = "webhook-workers"
)

func NewClient(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return client, nil
}

// EnsureStreamAndGroup creates stream and consumer group if not exists (MKSTREAM)
func EnsureStreamAndGroup(ctx context.Context, rdb *redis.Client) error {
	// XGROUP CREATE MKSTREAM
	err := rdb.XGroupCreateMkStream(ctx, StreamName, GroupName, "0").Err()
	if err != nil {
		// ignore if already exists
		if err.Error() == "BUSYGROUP Consumer Group name already exists" {
			return nil
		}
		// redis go-redis returns error with BUSYGROUP substring
		if contains(err.Error(), "BUSYGROUP") {
			return nil
		}
		return err
	}
	return nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// PublishDelivery publishes delivery_id to stream
func PublishDelivery(ctx context.Context, rdb *redis.Client, deliveryID string) error {
	_, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName,
		Values: map[string]interface{}{"delivery_id": deliveryID},
	}).Result()
	return err
}
