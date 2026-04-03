package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/remnawave/limiter/internal/api"
)

const (
	prefixUser           = "user:"
	prefixCooldown       = "cooldown:"
	prefixViolationCount = "violations:count:"
	keyWhitelist         = "whitelist"
	keyRestoreQ          = "restore:queue"
)

type Cache struct {
	client *redis.Client
}

func New(redisURL string) (*Cache, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	return &Cache{client: redis.NewClient(opts)}, nil
}

func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Cache) Close() error {
	return c.client.Close()
}

func (c *Cache) SetUser(ctx context.Context, userID string, user *api.CachedUser, ttl time.Duration) error {
	data, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}
	return c.client.Set(ctx, prefixUser+userID, data, ttl).Err()
}

func (c *Cache) GetUser(ctx context.Context, userID string) (*api.CachedUser, error) {
	data, err := c.client.Get(ctx, prefixUser+userID).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	var user api.CachedUser
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}
	return &user, nil
}

func (c *Cache) SetCooldown(ctx context.Context, userID string, ttl time.Duration) error {
	return c.client.Set(ctx, prefixCooldown+userID, "1", ttl).Err()
}

func (c *Cache) IsCooldownActive(ctx context.Context, userID string) (bool, error) {
	_, err := c.client.Get(ctx, prefixCooldown+userID).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get cooldown: %w", err)
	}
	return true, nil
}

func (c *Cache) AddToWhitelist(ctx context.Context, userID string) error {
	return c.client.SAdd(ctx, keyWhitelist, userID).Err()
}

func (c *Cache) RemoveFromWhitelist(ctx context.Context, userID string) error {
	return c.client.SRem(ctx, keyWhitelist, userID).Err()
}

func (c *Cache) IsWhitelisted(ctx context.Context, userID string) (bool, error) {
	return c.client.SIsMember(ctx, keyWhitelist, userID).Result()
}

func (c *Cache) InitWhitelist(ctx context.Context, userIDs []string) error {
	pipe := c.client.Pipeline()
	pipe.Del(ctx, keyWhitelist)
	if len(userIDs) > 0 {
		members := make([]interface{}, len(userIDs))
		for i, id := range userIDs {
			members[i] = id
		}
		pipe.SAdd(ctx, keyWhitelist, members...)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (c *Cache) SetRestoreTimer(ctx context.Context, uuid string, duration time.Duration) error {
	expiry := float64(time.Now().Add(duration).Unix())
	return c.client.ZAdd(ctx, keyRestoreQ, redis.Z{
		Score:  expiry,
		Member: uuid,
	}).Err()
}

func (c *Cache) GetExpiredRestoreTimers(ctx context.Context) ([]string, error) {
	now := fmt.Sprintf("%d", time.Now().Unix())

	script := redis.NewScript(`
		local expired = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
		if #expired > 0 then
			redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
		end
		return expired
	`)

	result, err := script.Run(ctx, c.client, []string{keyRestoreQ}, now).StringSlice()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get expired restore timers: %w", err)
	}
	return result, nil
}

func (c *Cache) IncrViolationCount(ctx context.Context, userID string) (int64, error) {
	key := prefixViolationCount + userID
	count, err := c.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("incr violation count: %w", err)
	}
	c.client.Expire(ctx, key, 24*time.Hour)
	return count, nil
}

func (c *Cache) GetViolationCount(ctx context.Context, userID string) (int64, error) {
	count, err := c.client.Get(ctx, prefixViolationCount+userID).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get violation count: %w", err)
	}
	return count, nil
}
