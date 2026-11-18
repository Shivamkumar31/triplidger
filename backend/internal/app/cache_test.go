package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeDashboardCache struct {
	mu     sync.Mutex
	values map[string][]byte
	err    error
	gets   int
	sets   int
}

func (f *fakeDashboardCache) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.err != nil {
		return nil, f.err
	}
	return f.values[key], nil
}

func (f *fakeDashboardCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.values[key] = value
	return nil
}

func (f *fakeDashboardCache) Close() error { return nil }

func testPayload(groupID string, version int64, total int64) map[string]any {
	return map[string]any{
		"id": groupID, "name": "Trip", "financial_version": version,
		"total_paise": total, "members": []any{}, "expenses": json.RawMessage("[]"),
		"settlements": json.RawMessage("[]"), "suggestions": []any{},
	}
}

func TestDashboardCacheFirstMissRepeatedHit(t *testing.T) {
	ctx := context.Background()
	cache := &fakeDashboardCache{values: map[string][]byte{}}
	groupID := "11111111-1111-1111-1111-111111111111"

	_, ok, err := loadCachedDashboard(cache, ctx, groupID, 1)
	if err != nil || ok {
		t.Fatalf("first lookup: ok=%v err=%v", ok, err)
	}
	payload := testPayload(groupID, 1, 90000)
	rawPayload, _ := json.Marshal(payload)
	raw, _ := encodeCachedDashboard(1, rawPayload)
	cache.values[dashboardCacheKey(groupID, 1)] = raw

	hit, ok, err := loadCachedDashboard(cache, ctx, groupID, 1)
	if err != nil || !ok {
		t.Fatalf("repeated lookup: ok=%v err=%v", ok, err)
	}
	if hit["id"] != groupID || hit["total_paise"] != float64(90000) {
		t.Fatalf("unexpected payload: %#v", hit)
	}
}

func TestDashboardCacheMalformedTimeoutAndOutageFallBack(t *testing.T) {
	ctx := context.Background()
	groupID := "22222222-2222-2222-2222-222222222222"

	invalid := &fakeDashboardCache{values: map[string][]byte{
		dashboardCacheKey(groupID, 1): []byte(`{"financial_version":1,"payload":{bad`),
	}}
	if _, ok, err := loadCachedDashboard(invalid, ctx, groupID, 1); err != nil || ok {
		t.Fatalf("malformed cache should be a miss: ok=%v err=%v", ok, err)
	}

	timeout := &fakeDashboardCache{values: map[string][]byte{}, err: context.DeadlineExceeded}
	if _, ok, err := loadCachedDashboard(timeout, ctx, groupID, 1); !errors.Is(err, context.DeadlineExceeded) || ok {
		t.Fatalf("timeout should fall back: ok=%v err=%v", ok, err)
	}

	outage := &fakeDashboardCache{values: map[string][]byte{}, err: errors.New("redis unavailable")}
	if _, ok, err := loadCachedDashboard(outage, ctx, groupID, 1); err == nil || ok {
		t.Fatalf("outage should fall back: ok=%v err=%v", ok, err)
	}
}

func TestCachedPayloadDoesNotLeakAuthenticatedRole(t *testing.T) {
	groupID := "33333333-3333-3333-3333-333333333333"
	payload := testPayload(groupID, 4, 100000)
	rawPayload, _ := json.Marshal(payload)
	raw, _ := encodeCachedDashboard(4, rawPayload)
	payload, ok := decodeCachedDashboard(raw, 4, groupID)
	if !ok {
		t.Fatal("expected valid cached payload")
	}
	if _, exists := payload["role"]; exists {
		t.Fatal("shared cache payload contains authenticated role")
	}
	owner := payload
	owner["role"] = "owner"
	memberPayload := testPayload(groupID, 4, 100000)
	memberPayload["role"] = "member"
	if owner["role"] == memberPayload["role"] {
		t.Fatal("test setup unexpectedly reused a role")
	}
}

func TestConcurrentCacheFillsStayVersionMatched(t *testing.T) {
	groupID := "44444444-4444-4444-4444-444444444444"
	cache := &fakeDashboardCache{values: map[string][]byte{}}
	var wg sync.WaitGroup
	for version, total := range map[int64]int64{1: 90000, 2: 100000} {
		version, total := version, total
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload, _ := json.Marshal(testPayload(groupID, version, total))
			raw, _ := encodeCachedDashboard(version, payload)
			_ = cache.Set(context.Background(), dashboardCacheKey(groupID, version), raw, time.Minute)
		}()
	}
	wg.Wait()
	payload, ok, err := loadCachedDashboard(cache, context.Background(), groupID, 2)
	if err != nil || !ok {
		t.Fatalf("version 2 lookup failed: ok=%v err=%v", ok, err)
	}
	if payload["financial_version"] != float64(2) || payload["total_paise"] != float64(100000) {
		t.Fatalf("mismatched payload/version: %#v", payload)
	}
}
