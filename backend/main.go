package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"tripledger/internal/app"
	"tripledger/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		r, e := client.Get("http://127.0.0.1:8080/healthz")
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		slog.Error("Set DATABASE_URL to your Neon connection string")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		slog.Error("Invalid DATABASE_URL; check its format without sharing credentials")
		os.Exit(1)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("Cannot create database pool")
		os.Exit(1)
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		slog.Error("Cannot reach Neon; check DATABASE_URL and network")
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err = migrations.Run(ctx, db); err != nil {
			slog.Error("Migration failed", "error", err)
			os.Exit(1)
		}
		slog.Info("Migrations applied")
		return
	}
	origin := strings.TrimRight(os.Getenv("APP_ORIGIN"), "/")
	if origin == "" {
		origin = "http://localhost:3000"
	}
	cacheTTL := 60 * time.Second
	if v := os.Getenv("CACHE_TTL_SECONDS"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 86400 {
			cacheTTL = time.Duration(n) * time.Second
		}
	}
	cacheEnabled := true
	if v, ok := os.LookupEnv("CACHE_ENABLED"); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			cacheEnabled = b
		}
	}
	rawRedisURL := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if !cacheEnabled {
		rawRedisURL = ""
	}
	a := app.App{DB: db, Origin: origin, Secure: os.Getenv("COOKIE_SECURE") == "true", CacheEnabled: cacheEnabled && rawRedisURL != "", CacheTTL: cacheTTL}
	if rawRedisURL != "" {
		timeout := 250 * time.Millisecond
		if v := os.Getenv("REDIS_TIMEOUT_MS"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 5000 {
				timeout = time.Duration(n) * time.Millisecond
			}
		}
		cache, err := app.NewRedisDashboardCache(rawRedisURL, timeout)
		if err != nil {
			slog.Error("Invalid Redis configuration; continuing with PostgreSQL cache fallback")
		} else {
			a.Cache = cache
			defer cache.Close()
		}
	}
	srv := &http.Server{Addr: ":8080", Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	slog.Info("TripLedger API listening", "port", 8080)
	if err = srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server stopped", "error", err)
		os.Exit(1)
	}
}
