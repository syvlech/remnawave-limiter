package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/remnawave/limiter/internal/api"
	"github.com/remnawave/limiter/internal/i18n"
)

const (
	prefixUser               = "user:"
	prefixCooldown           = "cooldown:"
	prefixSoftCooldown       = "cooldown:soft:"
	prefixViolationCount     = "violations:count:"
	prefixViolationThreshold = "violations:threshold:"
	prefixWhitelistTemp      = "whitelist:temp:"
	prefixRestoreAttempts    = "restore:attempts:"
	keyWhitelist             = "whitelist"
	keyRestoreQ              = "restore:queue"
	keyConfigOverrides       = "config:overrides"
	keyStatsEvents           = "stats:events"
	keyStatsUsernames        = "stats:usernames"
	keyUpdateNotified        = "update:notified"
	prefixTrafficDay         = "traffic:day:"
	prefixTrafficAlert       = "traffic:alerted:"
	keyTrafficFinalized      = "traffic:finalized"
	prefixHWIDHistory        = "hwid:seen:"
	prefixHWIDReported       = "hwid:reported:"
	keyHWIDState             = "hwid:state"

	trafficDayMarker = "_"

	statsRetention = 8 * 24 * time.Hour

	statsUsernamesTrimInterval = time.Hour
)

type Cache struct {
	client *redis.Client

	statsUsernamesTrimmedAt atomic.Int64
}

func formatUserID(userID int64) string {
	return strconv.FormatInt(userID, 10)
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

func (c *Cache) SetUser(ctx context.Context, userID int64, user *api.CachedUser, ttl time.Duration) error {
	data, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("marshal user: %w", err)
	}
	return c.client.Set(ctx, prefixUser+formatUserID(userID), data, ttl).Err()
}

func (c *Cache) GetUser(ctx context.Context, userID int64) (*api.CachedUser, error) {
	data, err := c.client.Get(ctx, prefixUser+formatUserID(userID)).Bytes()
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

func (c *Cache) SetCooldown(ctx context.Context, userID int64, ttl time.Duration) error {
	return c.client.Set(ctx, prefixCooldown+formatUserID(userID), "1", ttl).Err()
}

func (c *Cache) IsCooldownActive(ctx context.Context, userID int64) (bool, error) {
	_, err := c.client.Get(ctx, prefixCooldown+formatUserID(userID)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get cooldown: %w", err)
	}
	return true, nil
}

func (c *Cache) SetSoftCooldown(ctx context.Context, userID int64, ttl time.Duration) error {
	return c.client.Set(ctx, prefixSoftCooldown+formatUserID(userID), "1", ttl).Err()
}

func (c *Cache) IsSoftCooldownActive(ctx context.Context, userID int64) (bool, error) {
	_, err := c.client.Get(ctx, prefixSoftCooldown+formatUserID(userID)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get soft cooldown: %w", err)
	}
	return true, nil
}

func (c *Cache) AddToWhitelist(ctx context.Context, userID int64) error {
	return c.client.SAdd(ctx, keyWhitelist, formatUserID(userID)).Err()
}

func (c *Cache) AddToWhitelistTemp(ctx context.Context, userID int64, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf(i18n.T("error.cache.temp_whitelist_ttl"), ttl)
	}
	return c.client.Set(ctx, prefixWhitelistTemp+formatUserID(userID), "1", ttl).Err()
}

func (c *Cache) IsWhitelisted(ctx context.Context, userID int64) (bool, error) {
	id := formatUserID(userID)
	pipe := c.client.Pipeline()
	permCmd := pipe.SIsMember(ctx, keyWhitelist, id)
	tempCmd := pipe.Exists(ctx, prefixWhitelistTemp+id)
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

func (c *Cache) GetNotifiedVersion(ctx context.Context) (string, error) {
	v, err := c.client.Get(ctx, keyUpdateNotified).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (c *Cache) SetNotifiedVersion(ctx context.Context, version string) error {
	return c.client.Set(ctx, keyUpdateNotified, version, 0).Err()
}

func (c *Cache) SetRestoreTimer(ctx context.Context, userID int64, duration time.Duration) error {
	expiry := float64(time.Now().Add(duration).Unix())
	return c.client.ZAdd(ctx, keyRestoreQ, redis.Z{
		Score:  expiry,
		Member: formatUserID(userID),
	}).Err()
}

func (c *Cache) RemoveRestoreTimer(ctx context.Context, userID int64) error {
	id := formatUserID(userID)
	pipe := c.client.TxPipeline()
	pipe.ZRem(ctx, keyRestoreQ, id)
	pipe.Del(ctx, prefixRestoreAttempts+id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("remove restore timer: %w", err)
	}
	return nil
}

const restoreLeaseDuration = 5 * time.Minute

type RestoreLease string

var claimExpiredRestore = redis.NewScript(`
	local expired = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
	for _, member in ipairs(expired) do
		redis.call('ZADD', KEYS[1], ARGV[2], member)
	end
	return expired
`)

var completeRestore = redis.NewScript(`
	local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
	if score and tonumber(score) == tonumber(ARGV[2]) then
		return redis.call('ZREM', KEYS[1], ARGV[1])
	end
	return 0
`)

var rescheduleRestore = redis.NewScript(`
	local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
	if score and tonumber(score) == tonumber(ARGV[2]) then
		redis.call('ZADD', KEYS[1], ARGV[3], ARGV[1])
		return 1
	end
	return 0
`)

func (c *Cache) ClaimExpiredRestoreTimers(ctx context.Context) ([]string, RestoreLease, error) {
	now := time.Now()
	lease := RestoreLease(strconv.FormatInt(now.Add(restoreLeaseDuration).Unix(), 10) + ".5")

	result, err := claimExpiredRestore.Run(ctx, c.client, []string{keyRestoreQ},
		strconv.FormatInt(now.Unix(), 10), string(lease)).StringSlice()
	if err == redis.Nil {
		return nil, lease, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("claim expired restore timers: %w", err)
	}
	return result, lease, nil
}

func (c *Cache) RestoreLeaseHeld(ctx context.Context, member string, lease RestoreLease) (bool, error) {
	score, err := c.client.ZScore(ctx, keyRestoreQ, member).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check restore lease: %w", err)
	}
	want, err := strconv.ParseFloat(string(lease), 64)
	if err != nil {
		return false, fmt.Errorf("check restore lease: %w", err)
	}
	return score == want, nil
}

func (c *Cache) CompleteRestore(ctx context.Context, member string, lease RestoreLease) error {
	if err := completeRestore.Run(ctx, c.client, []string{keyRestoreQ}, member, string(lease)).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("complete restore: %w", err)
	}
	return nil
}

func (c *Cache) RescheduleRestore(ctx context.Context, member string, lease RestoreLease, delay time.Duration) error {
	due := strconv.FormatInt(time.Now().Add(delay).Unix(), 10)
	if err := rescheduleRestore.Run(ctx, c.client, []string{keyRestoreQ}, member, string(lease), due).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("reschedule restore: %w", err)
	}
	return nil
}

var incrFixedWindow = redis.NewScript(`
	local count = redis.call('INCR', KEYS[1])
	if count == 1 or redis.call('TTL', KEYS[1]) < 0 then
		redis.call('EXPIRE', KEYS[1], ARGV[1])
	end
	return count
`)

func (c *Cache) incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	seconds := int64(window.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return incrFixedWindow.Run(ctx, c.client, []string{key}, seconds).Int64()
}

func (c *Cache) IncrViolationCount(ctx context.Context, userID int64) (int64, error) {
	count, err := c.incr(ctx, prefixViolationCount+formatUserID(userID), 24*time.Hour)
	if err != nil {
		return 0, fmt.Errorf("incr violation count: %w", err)
	}
	return count, nil
}

func (c *Cache) IncrThresholdCount(ctx context.Context, userID int64, window time.Duration) (int64, error) {
	count, err := c.incr(ctx, prefixViolationThreshold+formatUserID(userID), window)
	if err != nil {
		return 0, fmt.Errorf("incr threshold count: %w", err)
	}
	return count, nil
}

func (c *Cache) ResetThresholdCount(ctx context.Context, userID int64) error {
	return c.client.Del(ctx, prefixViolationThreshold+formatUserID(userID)).Err()
}

func (c *Cache) IncrRestoreAttempts(ctx context.Context, userID int64) (int64, error) {
	count, err := c.incr(ctx, prefixRestoreAttempts+formatUserID(userID), time.Hour)
	if err != nil {
		return 0, fmt.Errorf("incr restore attempts: %w", err)
	}
	return count, nil
}

func (c *Cache) ResetRestoreAttempts(ctx context.Context, userID int64) error {
	return c.client.Del(ctx, prefixRestoreAttempts+formatUserID(userID)).Err()
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

func (c *Cache) RecordViolation(ctx context.Context, userID int64, username string) error {
	now := time.Now()
	id := formatUserID(userID)
	member := fmt.Sprintf("%d:%s", now.UnixNano(), id)
	cutoff := strconv.FormatInt(now.Add(-statsRetention).Unix(), 10)

	pipe := c.client.Pipeline()
	pipe.ZAdd(ctx, keyStatsEvents, redis.Z{Score: float64(now.Unix()), Member: member})
	if username != "" {
		pipe.HSet(ctx, keyStatsUsernames, id, username)
	}
	pipe.ZRemRangeByScore(ctx, keyStatsEvents, "-inf", "("+cutoff)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("record violation: %w", err)
	}

	last := c.statsUsernamesTrimmedAt.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < statsUsernamesTrimInterval {
		return nil
	}
	if !c.statsUsernamesTrimmedAt.CompareAndSwap(last, now.UnixNano()) {
		return nil
	}
	if _, err := c.trimStatsUsernames(ctx, now); err != nil {
		c.statsUsernamesTrimmedAt.CompareAndSwap(now.UnixNano(), last)
		return err
	}
	return nil
}

var trimStatsUsernames = redis.NewScript(`
	local alive = {}
	for _, member in ipairs(redis.call('ZRANGEBYSCORE', KEYS[1], ARGV[1], '+inf')) do
		local sep = string.find(member, ':', 1, true)
		if sep then
			alive[string.sub(member, sep + 1)] = true
		end
	end
	local removed = 0
	for _, id in ipairs(redis.call('HKEYS', KEYS[2])) do
		if not alive[id] then
			redis.call('HDEL', KEYS[2], id)
			removed = removed + 1
		end
	end
	return removed
`)

func (c *Cache) trimStatsUsernames(ctx context.Context, now time.Time) (int64, error) {
	cutoff := strconv.FormatInt(now.Add(-statsRetention).Unix(), 10)
	removed, err := trimStatsUsernames.Run(ctx, c.client, []string{keyStatsEvents, keyStatsUsernames}, cutoff).Int64()
	if err != nil {
		return 0, fmt.Errorf("trim stats usernames: %w", err)
	}
	return removed, nil
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

func (c *Cache) SaveTrafficDay(ctx context.Context, day string, totals map[int64]int64, ttl time.Duration) error {
	key := prefixTrafficDay + day
	fields := make([]interface{}, 0, 2+2*len(totals))
	fields = append(fields, trafficDayMarker, 0)
	for userID, bytes := range totals {
		fields = append(fields, formatUserID(userID), bytes)
	}
	pipe := c.client.TxPipeline()
	pipe.Del(ctx, key)
	pipe.HSet(ctx, key, fields...)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save traffic day %s: %w", day, err)
	}
	return nil
}

func (c *Cache) GetTrafficDays(ctx context.Context, days []string, userIDs []int64) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(userIDs))
	if len(userIDs) == 0 || len(days) == 0 {
		return result, nil
	}
	fields := make([]string, len(userIDs))
	for i, id := range userIDs {
		fields[i] = formatUserID(id)
		result[id] = make([]int64, len(days))
	}
	pipe := c.client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(days))
	for i, day := range days {
		cmds[i] = pipe.HMGet(ctx, prefixTrafficDay+day, fields...)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get traffic days: %w", err)
	}
	for d, cmd := range cmds {
		for u, raw := range cmd.Val() {
			str, ok := raw.(string)
			if !ok {
				continue
			}
			if v, err := strconv.ParseInt(str, 10, 64); err == nil {
				result[userIDs[u]][d] = v
			}
		}
	}
	return result, nil
}

func (c *Cache) CountTrafficDays(ctx context.Context, days []string) (int, error) {
	if len(days) == 0 {
		return 0, nil
	}
	keys := make([]string, len(days))
	for i, day := range days {
		keys[i] = prefixTrafficDay + day
	}
	n, err := c.client.Exists(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("count traffic days: %w", err)
	}
	return int(n), nil
}

func (c *Cache) GetTrafficFinalized(ctx context.Context) (string, error) {
	v, err := c.client.Get(ctx, keyTrafficFinalized).Result()
	if err == redis.Nil {
		return "", nil
	}
	return v, err
}

func (c *Cache) SetTrafficFinalized(ctx context.Context, day string) error {
	return c.client.Set(ctx, keyTrafficFinalized, day, 0).Err()
}

func (c *Cache) TrafficAlerted(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, prefixTrafficAlert+key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (c *Cache) MarkTrafficAlert(ctx context.Context, key string, ttl time.Duration) error {
	return c.client.Set(ctx, prefixTrafficAlert+key, 1, ttl).Err()
}

func (c *Cache) CountUserViolations(ctx context.Context, userID int64, since time.Duration) (int, error) {
	minScore := strconv.FormatInt(time.Now().Add(-since).Unix(), 10)
	members, err := c.client.ZRangeByScore(ctx, keyStatsEvents, &redis.ZRangeBy{Min: minScore, Max: "+inf"}).Result()
	if err != nil {
		return 0, fmt.Errorf("count user violations: %w", err)
	}
	suffix := ":" + formatUserID(userID)
	n := 0
	for _, m := range members {
		if strings.HasSuffix(m, suffix) {
			n++
		}
	}
	return n, nil
}

func (c *Cache) GetTrafficDaysAll(ctx context.Context, days []string) ([]map[int64]int64, error) {
	pipe := c.client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(days))
	for i, day := range days {
		cmds[i] = pipe.HGetAll(ctx, prefixTrafficDay+day)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("get traffic days: %w", err)
	}
	out := make([]map[int64]int64, len(days))
	for i, cmd := range cmds {
		out[i] = make(map[int64]int64, len(cmd.Val()))
		for field, raw := range cmd.Val() {
			if field == trafficDayMarker {
				continue
			}
			id, err1 := strconv.ParseInt(field, 10, 64)
			v, err2 := strconv.ParseInt(raw, 10, 64)
			if err1 == nil && err2 == nil {
				out[i][id] = v
			}
		}
	}
	return out, nil
}

type HWIDRecord struct {
	HWID       string
	UserID     int64
	Username   string
	TelegramID int64
	SeenAt     time.Time
}

type hwidRecordJSON struct {
	Username   string `json:"u,omitempty"`
	TelegramID int64  `json:"t,omitempty"`
	SeenAt     int64  `json:"s"`
}

func (r HWIDRecord) encode() (string, error) {
	data, err := json.Marshal(hwidRecordJSON{Username: r.Username, TelegramID: r.TelegramID, SeenAt: r.SeenAt.Unix()})
	if err != nil {
		return "", fmt.Errorf("marshal hwid record: %w", err)
	}
	return string(data), nil
}

type HWIDState struct {
	Watermark time.Time
	Polled    time.Time
}

func (c *Cache) GetHWIDState(ctx context.Context) (HWIDState, error) {
	raw, err := c.client.HGetAll(ctx, keyHWIDState).Result()
	if err != nil {
		return HWIDState{}, fmt.Errorf("get hwid state: %w", err)
	}
	var st HWIDState
	if v, err := time.Parse(time.RFC3339Nano, raw["watermark"]); err == nil {
		st.Watermark = v
	}
	if v, err := time.Parse(time.RFC3339Nano, raw["polled"]); err == nil {
		st.Polled = v
	}
	return st, nil
}

func (c *Cache) SetHWIDState(ctx context.Context, st HWIDState) error {
	return c.client.HSet(ctx, keyHWIDState,
		"watermark", st.Watermark.UTC().Format(time.RFC3339Nano),
		"polled", st.Polled.UTC().Format(time.RFC3339Nano),
	).Err()
}

func (c *Cache) HWIDRecorded(ctx context.Context, records []HWIDRecord) ([]bool, error) {
	out := make([]bool, len(records))
	if len(records) == 0 {
		return out, nil
	}
	pipe := c.client.Pipeline()
	cmds := make([]*redis.BoolCmd, len(records))
	for i, r := range records {
		cmds[i] = pipe.HExists(ctx, prefixHWIDHistory+r.HWID, formatUserID(r.UserID))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("check hwid history: %w", err)
	}
	for i, cmd := range cmds {
		out[i] = cmd.Val()
	}
	return out, nil
}

var recordHWID = redis.NewScript(`
	redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
	local cutoff = tonumber(ARGV[3])
	local all = redis.call('HGETALL', KEYS[1])
	for i = 1, #all, 2 do
		if all[i] ~= ARGV[1] then
			local ok, rec = pcall(cjson.decode, all[i + 1])
			if ok and type(rec) == 'table' and tonumber(rec.s) and tonumber(rec.s) < cutoff then
				redis.call('HDEL', KEYS[1], all[i])
			end
		end
	end
	redis.call('PEXPIRE', KEYS[1], ARGV[4])
	return 1
`)

func (c *Cache) RecordHWID(ctx context.Context, r HWIDRecord, ttl time.Duration) error {
	value, err := r.encode()
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-ttl).Unix()
	if err := recordHWID.Run(ctx, c.client, []string{prefixHWIDHistory + r.HWID},
		formatUserID(r.UserID), value, cutoff, ttl.Milliseconds()).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("record hwid: %w", err)
	}
	return nil
}

var seedHWIDRecord = redis.NewScript(`
	redis.call('HSETNX', KEYS[1], ARGV[1], ARGV[2])
	local ttl = tonumber(ARGV[3])
	local cur = redis.call('PTTL', KEYS[1])
	if cur == -1 or cur < ttl then
		redis.call('PEXPIRE', KEYS[1], ttl)
	end
	return 1
`)

func (c *Cache) SeedHWIDHistory(ctx context.Context, records []HWIDRecord, retention time.Duration) error {
	if len(records) == 0 {
		return nil
	}
	now := time.Now()
	pipe := c.client.Pipeline()
	for _, r := range records {
		value, err := r.encode()
		if err != nil {
			return err
		}
		ttl := r.SeenAt.Add(retention).Sub(now)
		if ttl < time.Minute {
			ttl = time.Minute
		}
		seedHWIDRecord.Eval(ctx, pipe, []string{prefixHWIDHistory + r.HWID}, formatUserID(r.UserID), value, ttl.Milliseconds())
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("seed hwid history: %w", err)
	}
	return nil
}

func (c *Cache) GetHWIDHistory(ctx context.Context, hwid string) (map[int64]HWIDRecord, error) {
	raw, err := c.client.HGetAll(ctx, prefixHWIDHistory+hwid).Result()
	if err != nil {
		return nil, fmt.Errorf("get hwid history: %w", err)
	}
	out := make(map[int64]HWIDRecord, len(raw))
	for field, value := range raw {
		id, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			continue
		}
		var rec hwidRecordJSON
		if err := json.Unmarshal([]byte(value), &rec); err != nil {
			continue
		}
		out[id] = HWIDRecord{HWID: hwid, UserID: id, Username: rec.Username, TelegramID: rec.TelegramID, SeenAt: time.Unix(rec.SeenAt, 0).UTC()}
	}
	return out, nil
}

func (c *Cache) GetHWIDReported(ctx context.Context, userID int64) (map[int64]bool, error) {
	members, err := c.client.SMembers(ctx, prefixHWIDReported+formatUserID(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("get hwid reported: %w", err)
	}
	out := make(map[int64]bool, len(members))
	for _, m := range members {
		if id, err := strconv.ParseInt(m, 10, 64); err == nil {
			out[id] = true
		}
	}
	return out, nil
}

func (c *Cache) MarkHWIDReported(ctx context.Context, userID int64, others []int64, ttl time.Duration) error {
	if len(others) == 0 {
		return nil
	}
	members := make([]interface{}, len(others))
	for i, id := range others {
		members[i] = formatUserID(id)
	}
	key := prefixHWIDReported + formatUserID(userID)
	pipe := c.client.TxPipeline()
	pipe.SAdd(ctx, key, members...)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("mark hwid reported: %w", err)
	}
	return nil
}

func (c *Cache) UnmarkHWIDReported(ctx context.Context, userID int64, others []int64) error {
	if len(others) == 0 {
		return nil
	}
	members := make([]interface{}, len(others))
	for i, id := range others {
		members[i] = formatUserID(id)
	}
	if err := c.client.SRem(ctx, prefixHWIDReported+formatUserID(userID), members...).Err(); err != nil {
		return fmt.Errorf("unmark hwid reported: %w", err)
	}
	return nil
}
