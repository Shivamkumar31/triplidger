package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type DashboardCache interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration) error
	Close() error
}

type redisDashboardCache struct {
	client *redis.Client
}

func NewRedisDashboardCache(rawURL string, timeout time.Duration) (DashboardCache, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, err
	}
	opts.MaxRetries = 1
	opts.DialTimeout = timeout
	opts.ReadTimeout = timeout
	opts.WriteTimeout = timeout
	opts.PoolTimeout = timeout
	return &redisDashboardCache{client: redis.NewClient(opts)}, nil
}

func (c *redisDashboardCache) Get(ctx context.Context, key string) ([]byte, error) {
	return c.client.Get(ctx, key).Bytes()
}

func (c *redisDashboardCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *redisDashboardCache) Close() error {
	return c.client.Close()
}

func dashboardCacheKey(groupID string, version int64) string {
	return "tripledger:group:" + groupID + ":dashboard:v" + strconv.FormatInt(version, 10)
}

type cachedDashboard struct {
	FinancialVersion int64           `json:"financial_version"`
	Payload          json.RawMessage `json:"payload"`
}

func encodeCachedDashboard(version int64, payload json.RawMessage) ([]byte, error) {
	return json.Marshal(cachedDashboard{FinancialVersion: version, Payload: payload})
}

func decodeCachedDashboard(raw []byte, expectedVersion int64, groupID string) (map[string]any, bool) {
	var envelope cachedDashboard
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	if envelope.FinancialVersion != expectedVersion || len(envelope.Payload) == 0 {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, false
	}
	required := []string{"id", "name", "financial_version", "total_paise", "members", "expenses", "settlements", "suggestions"}
	for _, key := range required {
		if _, ok := payload[key]; !ok {
			return nil, false
		}
	}
	if payload["id"] != groupID {
		return nil, false
	}
	return payload, true
}

func cacheLog(kind, groupID string) {
	slog.Info("dashboard cache "+kind, "group_id", groupID)
}

func isCacheMiss(err error) bool {
	return err == redis.Nil
}

var errMalformedCachedDashboard = errors.New("malformed cached dashboard")

func loadCachedDashboard(cache DashboardCache, ctx context.Context, groupID string, version int64) (map[string]any, bool, error) {
	raw, err := cache.Get(ctx, dashboardCacheKey(groupID, version))
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		return nil, false, redis.Nil
	}
	payload, ok := decodeCachedDashboard(raw, version, groupID)
	if !ok {
		return nil, false, errMalformedCachedDashboard
	}
	return payload, true, nil
}
