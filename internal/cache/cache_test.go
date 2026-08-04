package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/remnawave/limiter/internal/api"
)

const testRedisURL = "redis://localhost:6379/15"

func setupTestCache(t *testing.T) *Cache {
	t.Helper()

	c, err := New(testRedisURL)
	if err != nil {
		t.Skipf("Redis unavailable: %v", err)
	}

	ctx := context.Background()
	if err := c.Ping(ctx); err != nil {
		c.Close()
		t.Skipf("Redis unavailable: %v", err)
	}

	c.client.FlushDB(ctx)

	t.Cleanup(func() {
		c.client.FlushDB(ctx)
		c.Close()
	})

	return c
}

// Переход на Remnawave 3.x сменил тип ID пользователя со строки на число, но
// десятичное представление осталось прежним. Тест фиксирует, что имена ключей не
// изменились, — иначе при обновлении потребовалась бы миграция данных в Redis.
func TestCache_KeyNamesUnchangedAfterV3(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 456

	if err := c.SetUser(ctx, userID, &api.CachedUser{UserID: userID}, time.Minute); err != nil {
		t.Fatalf("SetUser error: %v", err)
	}
	if err := c.SetCooldown(ctx, userID, time.Minute); err != nil {
		t.Fatalf("SetCooldown error: %v", err)
	}
	if err := c.SetSoftCooldown(ctx, userID, time.Minute); err != nil {
		t.Fatalf("SetSoftCooldown error: %v", err)
	}
	if _, err := c.IncrViolationCount(ctx, userID); err != nil {
		t.Fatalf("IncrViolationCount error: %v", err)
	}
	if _, err := c.IncrThresholdCount(ctx, userID, time.Minute); err != nil {
		t.Fatalf("IncrThresholdCount error: %v", err)
	}
	if err := c.AddToWhitelistTemp(ctx, userID, time.Minute); err != nil {
		t.Fatalf("AddToWhitelistTemp error: %v", err)
	}

	for _, key := range []string{
		"user:456",
		"cooldown:456",
		"cooldown:soft:456",
		"violations:count:456",
		"violations:threshold:456",
		"whitelist:temp:456",
	} {
		n, err := c.client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%q) error: %v", key, err)
		}
		if n != 1 {
			t.Errorf("ожидался ключ %q, но его нет", key)
		}
	}

	if err := c.AddToWhitelist(ctx, userID); err != nil {
		t.Fatalf("AddToWhitelist error: %v", err)
	}
	inSet, err := c.client.SIsMember(ctx, keyWhitelist, "456").Result()
	if err != nil {
		t.Fatalf("SIsMember error: %v", err)
	}
	if !inSet {
		t.Error(`ожидался member "456" в множестве whitelist`)
	}

	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}
	expired, err := c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 1 || expired[0] != "456" {
		t.Errorf(`ожидался member "456" в restore:queue, получено %v`, expired)
	}
}

func TestCache_UserData(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	user := &api.CachedUser{
		UserID:          456,
		Username:        "testuser",
		Email:           "test@example.com",
		TelegramID:      789,
		HWIDDeviceLimit: 3,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub",
	}

	got, err := c.GetUser(ctx, 456)
	if err != nil {
		t.Fatalf("GetUser error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for non-existent user, got %+v", got)
	}

	if err := c.SetUser(ctx, 456, user, 10*time.Second); err != nil {
		t.Fatalf("SetUser error: %v", err)
	}

	got, err = c.GetUser(ctx, 456)
	if err != nil {
		t.Fatalf("GetUser error: %v", err)
	}
	if got == nil {
		t.Fatal("expected user, got nil")
	}
	if got.UserID != user.UserID {
		t.Errorf("UserID = %d, want %d", got.UserID, user.UserID)
	}
	if got.Username != user.Username {
		t.Errorf("Username = %q, want %q", got.Username, user.Username)
	}
	if got.Email != user.Email {
		t.Errorf("Email = %q, want %q", got.Email, user.Email)
	}
	if got.TelegramID != user.TelegramID {
		t.Errorf("TelegramID = %d, want %d", got.TelegramID, user.TelegramID)
	}
	if got.HWIDDeviceLimit != user.HWIDDeviceLimit {
		t.Errorf("HWIDDeviceLimit = %d, want %d", got.HWIDDeviceLimit, user.HWIDDeviceLimit)
	}
	if got.Status != user.Status {
		t.Errorf("Status = %q, want %q", got.Status, user.Status)
	}
	if got.SubscriptionURL != user.SubscriptionURL {
		t.Errorf("SubscriptionURL = %q, want %q", got.SubscriptionURL, user.SubscriptionURL)
	}
}

func TestCache_Cooldown(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	active, err := c.IsCooldownActive(ctx, 1)
	if err != nil {
		t.Fatalf("IsCooldownActive error: %v", err)
	}
	if active {
		t.Fatal("expected no cooldown initially")
	}

	if err := c.SetCooldown(ctx, 1, 10*time.Second); err != nil {
		t.Fatalf("SetCooldown error: %v", err)
	}

	active, err = c.IsCooldownActive(ctx, 1)
	if err != nil {
		t.Fatalf("IsCooldownActive error: %v", err)
	}
	if !active {
		t.Fatal("expected cooldown to be active")
	}
}

func TestCache_Whitelist(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	ok, err := c.IsWhitelisted(ctx, 1)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if ok {
		t.Fatal("expected not whitelisted initially")
	}

	if err := c.AddToWhitelist(ctx, 1); err != nil {
		t.Fatalf("AddToWhitelist error: %v", err)
	}

	ok, err = c.IsWhitelisted(ctx, 1)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if !ok {
		t.Fatal("expected whitelisted after add")
	}

	if err := c.RemoveFromWhitelist(ctx, 1); err != nil {
		t.Fatalf("RemoveFromWhitelist error: %v", err)
	}

	ok, err = c.IsWhitelisted(ctx, 1)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if ok {
		t.Fatal("expected not whitelisted after removal")
	}

	if err := c.InitWhitelist(ctx, []string{"11", "22", "33"}); err != nil {
		t.Fatalf("InitWhitelist error: %v", err)
	}
	for _, id := range []int64{11, 22, 33} {
		ok, err = c.IsWhitelisted(ctx, id)
		if err != nil {
			t.Fatalf("IsWhitelisted(%d) error: %v", id, err)
		}
		if !ok {
			t.Fatalf("expected %d to be whitelisted after InitWhitelist", id)
		}
	}
}

func TestCache_RestoreTimer(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	if err := c.SetRestoreTimer(ctx, 99, 1*time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}

	expired, err := c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expected no expired timers, got %v", expired)
	}

	time.Sleep(1500 * time.Millisecond)

	expired, err = c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 1 || expired[0] != "99" {
		t.Fatalf("expected [uuid-abc], got %v", expired)
	}

	expired, err = c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expected empty after retrieval, got %v", expired)
	}
}

func TestCache_InvalidURL(t *testing.T) {
	_, err := New("not-a-valid-url://bad")
	if err == nil {
		t.Fatal("expected error for invalid Redis URL")
	}
}

func TestCache_ClientType(t *testing.T) {
	c := setupTestCache(t)
	_ = c.client
	var _ *redis.Client = c.client
}

func TestCache_WhitelistTemp(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	ok, err := c.IsWhitelisted(ctx, 2)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if ok {
		t.Fatal("expected not whitelisted initially")
	}

	if err := c.AddToWhitelistTemp(ctx, 2, 10*time.Second); err != nil {
		t.Fatalf("AddToWhitelistTemp error: %v", err)
	}

	ok, err = c.IsWhitelisted(ctx, 2)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if !ok {
		t.Fatal("expected whitelisted after AddToWhitelistTemp")
	}

	inSet, err := c.client.SIsMember(ctx, keyWhitelist, "2").Result()
	if err != nil {
		t.Fatalf("SIsMember error: %v", err)
	}
	if inSet {
		t.Fatal("AddToWhitelistTemp must not add to permanent set")
	}
}

func TestCache_WhitelistTemp_Expires(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	if err := c.AddToWhitelistTemp(ctx, 3, 1*time.Second); err != nil {
		t.Fatalf("AddToWhitelistTemp error: %v", err)
	}

	ok, err := c.IsWhitelisted(ctx, 3)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if !ok {
		t.Fatal("expected whitelisted before TTL expiry")
	}

	time.Sleep(1500 * time.Millisecond)

	ok, err = c.IsWhitelisted(ctx, 3)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if ok {
		t.Fatal("expected NOT whitelisted after TTL expiry")
	}
}

func TestCache_Whitelist_PermanentAndTempIndependent(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	if err := c.AddToWhitelist(ctx, 5); err != nil {
		t.Fatalf("AddToWhitelist error: %v", err)
	}
	if err := c.AddToWhitelistTemp(ctx, 4, 10*time.Second); err != nil {
		t.Fatalf("AddToWhitelistTemp error: %v", err)
	}

	for _, id := range []int64{5, 4} {
		ok, err := c.IsWhitelisted(ctx, id)
		if err != nil {
			t.Fatalf("IsWhitelisted(%d) error: %v", id, err)
		}
		if !ok {
			t.Fatalf("expected %d to be whitelisted", id)
		}
	}

	if err := c.RemoveFromWhitelist(ctx, 4); err != nil {
		t.Fatalf("RemoveFromWhitelist error: %v", err)
	}
	ok, err := c.IsWhitelisted(ctx, 4)
	if err != nil {
		t.Fatalf("IsWhitelisted error: %v", err)
	}
	if !ok {
		t.Fatal("RemoveFromWhitelist должен удалять только постоянный, временный остаётся")
	}
}

// Раньше TTL переустанавливался после каждого INCR, из-за чего окно
// становилось скользящим: у постоянного нарушителя счётчик за 24ч не
// истекал никогда и рос неограниченно.
func TestCache_ViolationCount_WindowDoesNotSlide(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 777
	key := prefixViolationCount + formatUserID(userID)

	if _, err := c.IncrViolationCount(ctx, userID); err != nil {
		t.Fatalf("IncrViolationCount error: %v", err)
	}
	first, err := c.client.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	count, err := c.IncrViolationCount(ctx, userID)
	if err != nil {
		t.Fatalf("IncrViolationCount error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	second, err := c.client.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL error: %v", err)
	}
	if second >= first {
		t.Errorf("TTL продлился при повторном инкременте: было %v, стало %v", first, second)
	}
}

func TestCache_ThresholdCount_UsesConfiguredWindow(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 778
	key := prefixViolationThreshold + formatUserID(userID)

	if _, err := c.IncrThresholdCount(ctx, userID, 90*time.Second); err != nil {
		t.Fatalf("IncrThresholdCount error: %v", err)
	}

	ttl, err := c.client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL error: %v", err)
	}
	if ttl <= 0 || ttl > 90*time.Second {
		t.Errorf("TTL = %v, want 0 < ttl <= 90s", ttl)
	}
}

// Ключ, оставшийся без TTL от предыдущих версий, должен получить его
// на следующем инкременте, а не жить в Redis вечно.
func TestCache_Counter_HealsKeyWithoutTTL(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 779
	key := prefixViolationCount + formatUserID(userID)

	if err := c.client.Set(ctx, key, 5, 0).Err(); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if ttl := c.client.TTL(ctx, key).Val(); ttl >= 0 {
		t.Fatalf("подготовка теста: ожидался ключ без TTL, получено %v", ttl)
	}

	count, err := c.IncrViolationCount(ctx, userID)
	if err != nil {
		t.Fatalf("IncrViolationCount error: %v", err)
	}
	if count != 6 {
		t.Errorf("count = %d, want 6", count)
	}
	if ttl := c.client.TTL(ctx, key).Val(); ttl <= 0 {
		t.Errorf("TTL не выставлен для ключа без срока жизни: %v", ttl)
	}
}

func TestCache_RestoreAttempts(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 780

	for want := int64(1); want <= 3; want++ {
		got, err := c.IncrRestoreAttempts(ctx, userID)
		if err != nil {
			t.Fatalf("IncrRestoreAttempts error: %v", err)
		}
		if got != want {
			t.Errorf("attempt = %d, want %d", got, want)
		}
	}

	if err := c.ResetRestoreAttempts(ctx, userID); err != nil {
		t.Fatalf("ResetRestoreAttempts error: %v", err)
	}

	got, err := c.IncrRestoreAttempts(ctx, userID)
	if err != nil {
		t.Fatalf("IncrRestoreAttempts error: %v", err)
	}
	if got != 1 {
		t.Errorf("после сброса attempt = %d, want 1", got)
	}
}

// Истёкшие таймеры забираются из очереди деструктивно, поэтому монитор
// обязан уметь вернуть ID назад, если панель была недоступна.
func TestCache_RestoreTimer_CanBeRequeued(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 781

	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}

	expired, err := c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 1 || expired[0] != formatUserID(userID) {
		t.Fatalf("expired = %v, want [%s]", expired, formatUserID(userID))
	}

	// Повторный вызов ничего не возвращает — запись уже удалена.
	if again, err := c.GetExpiredRestoreTimers(ctx); err != nil || len(again) != 0 {
		t.Fatalf("expected empty queue, got %v (err %v)", again, err)
	}

	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("requeue error: %v", err)
	}
	requeued, err := c.GetExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("GetExpiredRestoreTimers error: %v", err)
	}
	if len(requeued) != 1 {
		t.Errorf("после возврата в очередь получено %v, want 1 запись", requeued)
	}
}
