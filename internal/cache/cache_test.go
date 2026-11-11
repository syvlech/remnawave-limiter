package cache

import (
	"context"
	"strconv"
	"sync"
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
	expired, _, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
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

	expired, _, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expected no expired timers, got %v", expired)
	}

	time.Sleep(1500 * time.Millisecond)

	expired, _, err = c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 1 || expired[0] != "99" {
		t.Fatalf("expected [uuid-abc], got %v", expired)
	}

	expired, _, err = c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
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

func TestCache_RestoreTimer_CanBeRequeued(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 781

	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}

	expired, _, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
	}
	if len(expired) != 1 || expired[0] != formatUserID(userID) {
		t.Fatalf("expired = %v, want [%s]", expired, formatUserID(userID))
	}

	if again, _, err := c.ClaimExpiredRestoreTimers(ctx); err != nil || len(again) != 0 {
		t.Fatalf("expected empty queue, got %v (err %v)", again, err)
	}

	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("requeue error: %v", err)
	}
	requeued, _, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
	}
	if len(requeued) != 1 {
		t.Errorf("после возврата в очередь получено %v, want 1 запись", requeued)
	}
}

func TestCache_WhitelistTemp_RejectsNonPositiveTTL(t *testing.T) {
	c, err := New("redis://127.0.0.1:1/15")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, ttl := range []time.Duration{0, -time.Minute} {
		if err := c.AddToWhitelistTemp(context.Background(), 5, ttl); err == nil {
			t.Errorf("AddToWhitelistTemp с TTL %v должен вернуть ошибку", ttl)
		}
	}
}

func TestCache_RemoveRestoreTimer(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	if err := c.SetRestoreTimer(ctx, 7, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer: %v", err)
	}
	if err := c.SetRestoreTimer(ctx, 8, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer: %v", err)
	}
	if _, err := c.IncrRestoreAttempts(ctx, 7); err != nil {
		t.Fatalf("IncrRestoreAttempts: %v", err)
	}

	if err := c.RemoveRestoreTimer(ctx, 7); err != nil {
		t.Fatalf("RemoveRestoreTimer: %v", err)
	}
	if err := c.RemoveRestoreTimer(ctx, 7); err != nil {
		t.Fatalf("повторный RemoveRestoreTimer: %v", err)
	}

	expired, _, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers: %v", err)
	}
	if len(expired) != 1 || expired[0] != "8" {
		t.Errorf("в очереди должен остаться только 8, получено %v", expired)
	}
	if n, _ := c.client.Exists(ctx, prefixRestoreAttempts+"7").Result(); n != 0 {
		t.Error("счётчик попыток восстановления должен удаляться вместе с таймером")
	}
}

func TestCache_NotifiedVersion(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	v, err := c.GetNotifiedVersion(ctx)
	if err != nil || v != "" {
		t.Fatalf("до первого уведомления ожидалась пустая версия без ошибки, получено %q, %v", v, err)
	}
	if err := c.SetNotifiedVersion(ctx, "4.7.0"); err != nil {
		t.Fatalf("SetNotifiedVersion: %v", err)
	}
	if v, err := c.GetNotifiedVersion(ctx); err != nil || v != "4.7.0" {
		t.Fatalf("получено %q, %v, ожидалось 4.7.0", v, err)
	}
	if ttl := c.client.TTL(ctx, keyUpdateNotified).Val(); ttl != -1 {
		t.Errorf("ключ %s не должен истекать, TTL = %v", keyUpdateNotified, ttl)
	}
}

func TestCache_TrafficHistory(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	if err := c.SaveTrafficDay(ctx, "2026-11-10", map[int64]int64{1: 5 << 30, 2: 7 << 30}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveTrafficDay(ctx, "2026-11-11", map[int64]int64{}, time.Hour); err != nil {
		t.Fatal(err)
	}
	n, err := c.CountTrafficDays(ctx, []string{"2026-11-09", "2026-11-10", "2026-11-11"})
	if err != nil || n != 2 {
		t.Fatalf("сутки без пользователей тоже считаются сохранёнными: %d, %v", n, err)
	}
	hist, err := c.GetTrafficDays(ctx, []string{"2026-11-10", "2026-11-11"}, []int64{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if hist[1][0] != 5<<30 || hist[1][1] != 0 || hist[3][0] != 0 {
		t.Errorf("история прочитана неверно: %v", hist)
	}
	if err := c.SaveTrafficDay(ctx, "2026-11-10", map[int64]int64{2: 1 << 30}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if hist, _ := c.GetTrafficDays(ctx, []string{"2026-11-10"}, []int64{1}); hist[1][0] != 0 {
		t.Error("повторное сохранение суток должно перезаписывать их целиком")
	}

	if v, err := c.GetTrafficFinalized(ctx); err != nil || v != "" {
		t.Fatalf("до первого закрытия суток — пусто: %q, %v", v, err)
	}
	_ = c.SetTrafficFinalized(ctx, "2026-11-11")
	if v, _ := c.GetTrafficFinalized(ctx); v != "2026-11-11" {
		t.Errorf("закрытые сутки = %q", v)
	}

	if done, _ := c.TrafficAlerted(ctx, "spike:1"); done {
		t.Fatal("метки ещё нет")
	}
	_ = c.MarkTrafficAlert(ctx, "spike:1", time.Hour)
	if done, _ := c.TrafficAlerted(ctx, "spike:1"); !done {
		t.Error("метка должна появиться")
	}
}

func TestCache_CountUserViolations(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()
	for _, id := range []int64{5, 5, 15, 55} {
		if err := c.RecordViolation(ctx, id, "u"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := c.CountUserViolations(ctx, 5, time.Hour); err != nil || n != 2 {
		t.Errorf("нарушений у 5 = %d (%v), ожидалось 2: id 15 и 55 не должны совпадать по суффиксу", n, err)
	}
}

func TestCache_GetTrafficDaysAll(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()
	if err := c.SaveTrafficDay(ctx, "2026-11-10", map[int64]int64{7: 3 << 30, 8: 1 << 30}, time.Hour); err != nil {
		t.Fatalf("SaveTrafficDay error: %v", err)
	}
	all, err := c.GetTrafficDaysAll(ctx, []string{"2026-11-09", "2026-11-10"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || len(all[0]) != 0 || len(all[1]) != 2 || all[1][7] != 3<<30 {
		t.Errorf("сутки прочитаны неверно (служебное поле не должно попадать): %v", all)
	}
}

func TestCache_RestoreLease_KeepsTimerUntilCompleted(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 501
	member := formatUserID(userID)
	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}

	claimed, lease, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil || len(claimed) != 1 || claimed[0] != member {
		t.Fatalf("claimed = %v (err %v), want [%s]", claimed, err, member)
	}
	if n, err := c.client.ZCard(ctx, keyRestoreQ).Result(); err != nil || n != 1 {
		t.Fatalf("запись должна оставаться в очереди до завершения, ZCARD = %d (err %v)", n, err)
	}
	if held, err := c.RestoreLeaseHeld(ctx, member, lease); err != nil || !held {
		t.Fatalf("аренда должна удерживаться, held = %v (err %v)", held, err)
	}

	if err := c.CompleteRestore(ctx, member, lease); err != nil {
		t.Fatalf("CompleteRestore error: %v", err)
	}
	if n, err := c.client.ZCard(ctx, keyRestoreQ).Result(); err != nil || n != 0 {
		t.Errorf("после завершения запись убирается, ZCARD = %d (err %v)", n, err)
	}
}

func TestCache_RestoreLease_ReschedulesForRetry(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 502
	member := formatUserID(userID)
	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}
	_, lease, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
	}

	if err := c.RescheduleRestore(ctx, member, lease, -time.Second); err != nil {
		t.Fatalf("RescheduleRestore error: %v", err)
	}
	if held, err := c.RestoreLeaseHeld(ctx, member, lease); err != nil || held {
		t.Errorf("после возврата в очередь аренда снята, held = %v (err %v)", held, err)
	}
	again, _, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil || len(again) != 1 || again[0] != member {
		t.Errorf("возвращённая запись должна выдаваться снова, получено %v (err %v)", again, err)
	}
}

func TestCache_RestoreLease_DoesNotOverrideAdminAction(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const removed, retimed int64 = 503, 504
	for _, id := range []int64{removed, retimed} {
		if err := c.SetRestoreTimer(ctx, id, -time.Second); err != nil {
			t.Fatalf("SetRestoreTimer error: %v", err)
		}
	}
	claimed, lease, err := c.ClaimExpiredRestoreTimers(ctx)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed = %v (err %v), want 2 записи", claimed, err)
	}

	if err := c.RemoveRestoreTimer(ctx, removed); err != nil {
		t.Fatalf("RemoveRestoreTimer error: %v", err)
	}
	if held, err := c.RestoreLeaseHeld(ctx, formatUserID(removed), lease); err != nil || held {
		t.Errorf("снятый админом таймер не должен считаться арендованным, held = %v (err %v)", held, err)
	}
	if err := c.RescheduleRestore(ctx, formatUserID(removed), lease, time.Minute); err != nil {
		t.Fatalf("RescheduleRestore error: %v", err)
	}
	if _, err := c.client.ZScore(ctx, keyRestoreQ, formatUserID(removed)).Result(); err != redis.Nil {
		t.Errorf("повтор не должен возвращать в очередь таймер, снятый админом (err %v)", err)
	}

	if err := c.SetRestoreTimer(ctx, retimed, time.Hour); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}
	if err := c.CompleteRestore(ctx, formatUserID(retimed), lease); err != nil {
		t.Fatalf("CompleteRestore error: %v", err)
	}
	score, err := c.client.ZScore(ctx, keyRestoreQ, formatUserID(retimed)).Result()
	if err != nil {
		t.Fatalf("новый таймер админа должен остаться в очереди: %v", err)
	}
	if time.Unix(int64(score), 0).Before(time.Now().Add(50 * time.Minute)) {
		t.Errorf("новый таймер админа не должен меняться, score = %v", score)
	}
}

func TestCache_RestoreLease_ExpiresAndIsClaimedAgain(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const userID int64 = 505
	member := formatUserID(userID)
	if err := c.SetRestoreTimer(ctx, userID, -time.Second); err != nil {
		t.Fatalf("SetRestoreTimer error: %v", err)
	}
	if _, _, err := c.ClaimExpiredRestoreTimers(ctx); err != nil {
		t.Fatalf("ClaimExpiredRestoreTimers error: %v", err)
	}

	score, err := c.client.ZScore(ctx, keyRestoreQ, member).Result()
	if err != nil {
		t.Fatalf("ZScore error: %v", err)
	}
	due := time.Unix(int64(score), 0)
	if due.Before(time.Now().Add(restoreLeaseDuration-time.Minute)) || due.After(time.Now().Add(restoreLeaseDuration+time.Minute)) {
		t.Errorf("брошенная запись должна вернуться через %v, срок %v", restoreLeaseDuration, due)
	}
}

func seedStatsEvent(t *testing.T, c *Cache, userID int64, username string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	id := formatUserID(userID)
	member := strconv.FormatInt(at.UnixNano(), 10) + ":" + id
	if err := c.client.ZAdd(ctx, keyStatsEvents, redis.Z{Score: float64(at.Unix()), Member: member}).Err(); err != nil {
		t.Fatalf("ZAdd error: %v", err)
	}
	if username == "" {
		return
	}
	if err := c.client.HSet(ctx, keyStatsUsernames, id, username).Err(); err != nil {
		t.Fatalf("HSet error: %v", err)
	}
}

func statsUsernames(t *testing.T, c *Cache) map[string]string {
	t.Helper()
	names, err := c.client.HGetAll(context.Background(), keyStatsUsernames).Result()
	if err != nil {
		t.Fatalf("HGetAll error: %v", err)
	}
	return names
}

func TestCache_RecordViolation_TrimsStaleUsernames(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()
	now := time.Now()

	const expired, returning, recent, legacy, fresh int64 = 1, 2, 3, 4, 5

	seedStatsEvent(t, c, expired, "expired", now.Add(-statsRetention-time.Hour))
	seedStatsEvent(t, c, returning, "returning", now.Add(-statsRetention-time.Hour))
	seedStatsEvent(t, c, returning, "returning", now.Add(-time.Hour))
	seedStatsEvent(t, c, recent, "recent", now.Add(-statsRetention+time.Hour))
	if err := c.client.HSet(ctx, keyStatsUsernames, formatUserID(legacy), "legacy").Err(); err != nil {
		t.Fatalf("HSet error: %v", err)
	}

	if err := c.RecordViolation(ctx, fresh, "fresh"); err != nil {
		t.Fatalf("RecordViolation error: %v", err)
	}

	want := map[string]string{"2": "returning", "3": "recent", "5": "fresh"}
	got := statsUsernames(t, c)
	if len(got) != len(want) {
		t.Errorf("имена после очистки = %v, ожидалось %v", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("имя пользователя %s = %q, ожидалось %q", id, got[id], name)
		}
	}

	if n, err := c.client.ZCard(ctx, keyStatsEvents).Result(); err != nil || n != 3 {
		t.Errorf("в stats:events должны остаться 3 события из окна хранения, ZCARD = %d (err %v)", n, err)
	}

	stats, err := c.GetViolationStats(ctx, 10)
	if err != nil {
		t.Fatalf("GetViolationStats error: %v", err)
	}
	for _, v := range stats.Top {
		if v.Username == "" {
			t.Errorf("у нарушителя %s из окна статистики пропало имя", v.UserID)
		}
	}
}

func TestCache_TrimStatsUsernames_CountsOnlyRetainedEvents(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()
	now := time.Now()

	seedStatsEvent(t, c, 1, "outdated", now.Add(-statsRetention-time.Minute))
	seedStatsEvent(t, c, 2, "retained", now.Add(-statsRetention+time.Minute))

	removed, err := c.trimStatsUsernames(ctx, now)
	if err != nil {
		t.Fatalf("trimStatsUsernames error: %v", err)
	}
	if removed != 1 {
		t.Errorf("удалено имён: %d, ожидалось 1", removed)
	}
	got := statsUsernames(t, c)
	if _, ok := got["1"]; ok {
		t.Error("событие старше окна хранения не должно удерживать имя, даже если ещё не вычищено из stats:events")
	}
	if got["2"] != "retained" {
		t.Errorf("имя пользователя с событием внутри окна хранения должно остаться: %v", got)
	}

	if removed, err := c.trimStatsUsernames(ctx, now); err != nil || removed != 0 {
		t.Errorf("повторная очистка ничего не удаляет: %d (err %v)", removed, err)
	}
}

func TestCache_RecordViolation_TrimIsThrottled(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	if err := c.RecordViolation(ctx, 1, "first"); err != nil {
		t.Fatalf("RecordViolation error: %v", err)
	}
	if c.statsUsernamesTrimmedAt.Load() == 0 {
		t.Fatal("первая запись нарушения должна запускать очистку имён")
	}

	if err := c.client.HSet(ctx, keyStatsUsernames, "99", "stale").Err(); err != nil {
		t.Fatalf("HSet error: %v", err)
	}
	if err := c.RecordViolation(ctx, 2, "second"); err != nil {
		t.Fatalf("RecordViolation error: %v", err)
	}
	if _, ok := statsUsernames(t, c)["99"]; !ok {
		t.Error("очистка не должна запускаться чаще, чем раз в statsUsernamesTrimInterval")
	}

	c.statsUsernamesTrimmedAt.Store(time.Now().Add(-statsUsernamesTrimInterval - time.Minute).UnixNano())
	if err := c.RecordViolation(ctx, 3, "third"); err != nil {
		t.Fatalf("RecordViolation error: %v", err)
	}
	got := statsUsernames(t, c)
	if _, ok := got["99"]; ok {
		t.Error("по истечении интервала очистка должна убрать имя без событий")
	}
	for _, id := range []string{"1", "2", "3"} {
		if _, ok := got[id]; !ok {
			t.Errorf("имя пользователя %s со свежим событием не должно удаляться", id)
		}
	}
}

func TestCache_TrimStatsUsernames_KeepsConcurrentlyRecorded(t *testing.T) {
	c := setupTestCache(t)
	ctx := context.Background()

	const writers, perWriter = 8, 25
	errs := make(chan error, writers+1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if _, err := c.trimStatsUsernames(ctx, time.Now()); err != nil {
				errs <- err
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id := int64(w*perWriter + i + 1)
				if err := c.RecordViolation(ctx, id, "user"+formatUserID(id)); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	<-done
	close(errs)
	for err := range errs {
		t.Fatalf("ошибка при параллельной записи: %v", err)
	}

	got := statsUsernames(t, c)
	if len(got) != writers*perWriter {
		t.Errorf("очистка потеряла свежие имена: осталось %d из %d", len(got), writers*perWriter)
	}
}
