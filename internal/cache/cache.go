package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/remnawave/limiter/internal/api"
)

const (
	prefixUser               = "user:"
	prefixCooldown           = "cooldown:"
	prefixSoftCooldown       = "cooldown:soft:"
	prefixViolationCount     = "violations:count:"
	prefixViolationThreshold = "violations:threshold:"
	prefixWhitelistTemp      = "whitelist:temp:"
	keyWhitelist             = "whitelist"
	keyRestoreQ              = "restore:queue"
	keyConfigOverrides       = "config:overrides"
	keyStatsEvents           = "stats:events"
	keyStatsUsernames        = "stats:usernames"

	statsRetention = 8 * 24 * time.Hour
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

func (c *Cache) SetSoftCooldown(ctx context.Context, userID string, ttl time.Duration) error {
	return c.client.Set(ctx, prefixSoftCooldown+userID, "1", ttl).Err()
}

func (c *Cache) IsSoftCooldownActive(ctx context.Context, userID string) (bool, error) {
	_, err := c.client.Get(ctx, prefixSoftCooldown+userID).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get soft cooldown: %w", err)
	}
	return true, nil
}

func (c *Cache) AddToWhitelist(ctx context.Context, userID string) error {
	return c.client.SAdd(ctx, keyWhitelist, userID).Err()
}

func (c *Cache) AddToWhitelistTemp(ctx context.Context, userID string, ttl time.Duration) error {
	return c.client.Set(ctx, prefixWhitelistTemp+userID, "1", ttl).Err()
}

func (c *Cache) RemoveFromWhitelist(ctx context.Context, userID string) error {
	return c.client.SRem(ctx, keyWhitelist, userID).Err()
}

func (c *Cache) IsWhitelisted(ctx context.Context, userID string) (bool, error) {
	pipe := c.client.Pipeline()
	permCmd := pipe.SIsMember(ctx, keyWhitelist, userID)
	tempCmd := pipe.Exists(ctx, prefixWhitelistTemp+userID)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("whitelist pipeline: %w", err)
	}
	return permCmd.Val() || tempCmd.Val() > 0, nil
}

func (c *Cache) InitWhitelist(ctx context.Context, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	members := make([]interface{}, len(userIDs))
	for i, id := range userIDs {
		members[i] = id
	}
	return c.client.SAdd(ctx, keyWhitelist, members...).Err()
}

func (c *Cache) GetConfigOverrides(ctx context.Context) (map[string]string, error) {
	res, err := c.client.HGetAll(ctx, keyConfigOverrides).Result()
	if err != nil {
		return nil, fmt.Errorf("get config overrides: %w", err)
	}
	return res, nil
}

func (c *Cache) SetConfigOverride(ctx context.Context, key, value string) error {
	return c.client.HSet(ctx, keyConfigOverrides, key, value).Err()
}

func (c *Cache) DeleteConfigOverride(ctx context.Context, key string) error {
	return c.client.HDel(ctx, keyConfigOverrides, key).Err()
}

func (c *Cache) ClearConfigOverrides(ctx context.Context) error {
	return c.client.Del(ctx, keyConfigOverrides).Err()
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

func (c *Cache) IncrThresholdCount(ctx context.Context, userID string, window time.Duration) (int64, error) {
	key := prefixViolationThreshold + userID
	count, err := c.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("incr threshold count: %w", err)
	}
	c.client.Expire(ctx, key, window)
	return count, nil
}

func (c *Cache) ResetThresholdCount(ctx context.Context, userID string) error {
	return c.client.Del(ctx, prefixViolationThreshold+userID).Err()
}

type ViolatorStat struct {
	UserID   string
	Username string
	Count    int
}

type ViolationStats struct {
	Count24h  int
	CountWeek int
	Top       []ViolatorStat
}

func (c *Cache) RecordViolation(ctx context.Context, userID, username string) error {
	now := time.Now()
	member := fmt.Sprintf("%d:%s", now.UnixNano(), userID)
	cutoff := strconv.FormatInt(now.Add(-statsRetention).Unix(), 10)

	pipe := c.client.Pipeline()
	pipe.ZAdd(ctx, keyStatsEvents, redis.Z{Score: float64(now.Unix()), Member: member})
	if username != "" {
		pipe.HSet(ctx, keyStatsUsernames, userID, username)
	}
	pipe.ZRemRangeByScore(ctx, keyStatsEvents, "-inf", "("+cutoff)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("record violation: %w", err)
	}
	return nil
}

func (c *Cache) GetViolationStats(ctx context.Context, topN int) (*ViolationStats, error) {
	now := time.Now()
	dayMin := strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10)
	weekMin := strconv.FormatInt(now.Add(-7*24*time.Hour).Unix(), 10)

	pipe := c.client.Pipeline()
	cnt24Cmd := pipe.ZCount(ctx, keyStatsEvents, dayMin, "+inf")
	weekCmd := pipe.ZRangeByScore(ctx, keyStatsEvents, &redis.ZRangeBy{Min: weekMin, Max: "+inf"})
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("get violation stats: %w", err)
	}

	members := weekCmd.Val()
	counts := make(map[string]int, len(members))
	order := make([]string, 0)
	for _, m := range members {
		idx := strings.IndexByte(m, ':')
		if idx < 0 {
			continue
		}
		userID := m[idx+1:]
		if _, seen := counts[userID]; !seen {
			order = append(order, userID)
		}
		counts[userID]++
	}

	stats := &ViolationStats{
		Count24h:  int(cnt24Cmd.Val()),
		CountWeek: len(members),
	}

	if len(counts) == 0 {
		return stats, nil
	}

	violators := make([]ViolatorStat, 0, len(counts))
	for _, userID := range order {
		violators = append(violators, ViolatorStat{UserID: userID, Count: counts[userID]})
	}
	sort.SliceStable(violators, func(i, j int) bool {
		return violators[i].Count > violators[j].Count
	})
	if topN > 0 && len(violators) > topN {
		violators = violators[:topN]
	}

	ids := make([]string, len(violators))
	for i := range violators {
		ids[i] = violators[i].UserID
	}
	if names, err := c.client.HMGet(ctx, keyStatsUsernames, ids...).Result(); err == nil {
		for i, n := range names {
			if s, ok := n.(string); ok {
				violators[i].Username = s
			}
		}
	}

	stats.Top = violators
	return stats, nil
}
