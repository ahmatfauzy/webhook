package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"webhooker/internal/config"
	"webhooker/internal/delivery"
	"webhooker/internal/handlers"
	"webhooker/internal/middleware"
	"webhooker/internal/queue"
	"webhooker/internal/storage"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: webhooker [server|worker|migrate]")
		os.Exit(1)
	}
	cmd := os.Args[1]
	switch cmd {
	case "server":
		runServer()
	case "worker":
		runWorker()
	case "migrate":
		runMigrate()
	default:
		fmt.Printf("unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func runServer() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx := context.Background()
	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	var rdb *redis.Client
	rdb, err = queue.NewClient(cfg.RedisURL)
	if err != nil {
		logger.Warn("redis not available, queue will be disabled", "error", err)
	} else {
		defer rdb.Close()
		if err := queue.EnsureStreamAndGroup(ctx, rdb); err != nil {
			logger.Warn("ensure stream failed", "error", err)
		}
	}

	r := chi.NewRouter()
	// global middleware
	r.Use(chiMiddleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger(logger))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins, cfg.AppEnv))
	// health without auth
	r.Get("/health", handlers.Health())
	r.Get("/ready", handlers.Ready(pool, rdb))
	r.Get("/metrics", handlers.Metrics())

	// API v1
	r.Route("/v1", func(v1 chi.Router) {
		// rate limiting
		if rdb != nil && cfg.RateLimitRPS > 0 {
			v1.Use(middleware.RateLimit(rdb, cfg.RateLimitRPS))
		}
		// projects: allow admin token to bootstrap first project without API key
		v1.Route("/projects", func(pr chi.Router) {
			ph := &handlers.ProjectHandler{Pool: pool}
			// POST /v1/projects — allow without API key if admin token present
			pr.Post("/", func(w http.ResponseWriter, req *http.Request) {
				// check auth
				if middleware.GetProjectID(req.Context()) == "" {
					// no project auth, check admin token
					tok := req.Header.Get("X-Admin-Token")
					if tok == "" {
						if h := req.Header.Get("Authorization"); h != "" {
							// try bearer
							if t, ok := extractBearerToken(h); ok && t == cfg.AdminToken && cfg.AdminToken != "" {
								tok = t
							}
						}
					}
					if cfg.AdminToken != "" && tok != cfg.AdminToken {
						// in production, require admin token
						if cfg.AppEnv == "production" {
							middleware.WriteError(w, req, http.StatusUnauthorized, "UNAUTHORIZED", "admin token required")
							return
						}
						// in dev, allow
					} else if cfg.AdminToken == "" && cfg.AppEnv == "development" {
						// allow
					} else if tok != cfg.AdminToken {
						middleware.WriteError(w, req, http.StatusUnauthorized, "UNAUTHORIZED", "admin token required")
						return
					}
				}
				ph.Create(w, req)
			})
			// other project routes require auth
			pr.Group(func(auth chi.Router) {
				auth.Use(middleware.APIKeyAuth(pool))
				auth.Get("/", ph.List)
				auth.Get("/{id}", ph.Get)
				auth.Patch("/{id}", ph.Update)
				auth.Delete("/{id}", ph.Delete)
			})
		})

		// api keys — POST allows admin token bootstrap for initial key
		akh := &handlers.APIKeyHandler{Pool: pool, AdminToken: cfg.AdminToken}
		v1.Post("/api-keys", akh.Create)

		// all other routes require API key auth
		v1.Group(func(auth chi.Router) {
			auth.Use(middleware.APIKeyAuth(pool))

			// api keys (list/revoke)
			auth.Get("/api-keys", akh.List)
			auth.Delete("/api-keys/{id}", akh.Revoke)

			// endpoints + subscriptions
			eh := &handlers.EndpointHandler{Pool: pool, EncryptionKey: cfg.EncryptionKey}
			auth.Mount("/endpoints", eh.Routes())

			// events
			evh := &handlers.EventHandler{Pool: pool, Redis: rdb, Config: cfg}
			auth.Mount("/events", evh.Routes())

			// deliveries
			auth.Get("/deliveries", func(w http.ResponseWriter, req *http.Request) {
				projectID := middleware.GetProjectID(req.Context())
				limit := 50
				if s := req.URL.Query().Get("limit"); s != "" {
					if v, err := parseLimit(s, 100); err == nil {
						limit = v
					}
				}
				after := req.URL.Query().Get("after")
				query := `SELECT d.id, d.event_id, d.endpoint_id, d.status, d.attempt_count, d.created_at FROM deliveries d JOIN events e ON e.id=d.event_id WHERE e.project_id=$1`
				args := []interface{}{projectID}
				if after != "" {
					query += ` AND d.id > $2 ORDER BY d.id ASC LIMIT $3`
					args = append(args, after, limit)
				} else {
					query += ` ORDER BY d.created_at DESC LIMIT $2`
					args = append(args, limit)
				}
				rows, err := pool.Query(req.Context(), query, args...)
				if err != nil {
					middleware.WriteError(w, req, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list deliveries")
					return
				}
				defer rows.Close()
				var list []map[string]interface{}
				for rows.Next() {
					var id, eid, epid, status string
					var attempt int
					var created interface{}
					_ = rows.Scan(&id, &eid, &epid, &status, &attempt, &created)
					list = append(list, map[string]interface{}{"id": id, "event_id": eid, "endpoint_id": epid, "status": status, "attempt_count": attempt, "created_at": created})
				}
				if list == nil {
					list = []map[string]interface{}{}
				}
				hasMore := len(list) == limit
				var next string
				if hasMore {
					next = list[len(list)-1]["id"].(string)
				}
				middleware.WriteJSON(w, req, http.StatusOK, map[string]interface{}{"data": list, "pagination": map[string]interface{}{"has_more": hasMore, "next_cursor": next}})
			})
			auth.Get("/deliveries/{id}", func(w http.ResponseWriter, req *http.Request) {
				// simplified
				id := chi.URLParam(req, "id")
				projectID := middleware.GetProjectID(req.Context())
				var did, eid, epid, status string
				var attempt int
				var created interface{}
				err := pool.QueryRow(req.Context(), `SELECT d.id, d.event_id, d.endpoint_id, d.status, d.attempt_count, d.created_at FROM deliveries d JOIN events e ON e.id=d.event_id WHERE d.id=$1 AND e.project_id=$2`, id, projectID).Scan(&did, &eid, &epid, &status, &attempt, &created)
				if err != nil {
					middleware.WriteError(w, req, http.StatusNotFound, "NOT_FOUND", "delivery not found")
					return
				}
				// attempts
				rows, _ := pool.Query(req.Context(), `SELECT attempt_number, http_status, error, duration_ms, created_at FROM delivery_attempts WHERE delivery_id=$1 ORDER BY attempt_number`, did)
				var attempts []map[string]interface{}
				if rows != nil {
					defer rows.Close()
					for rows.Next() {
						var n int
						var hs *int
						var errStr *string
						var dur *int
						var ca interface{}
						_ = rows.Scan(&n, &hs, &errStr, &dur, &ca)
						attempts = append(attempts, map[string]interface{}{"attempt_number": n, "http_status": hs, "error": errStr, "duration_ms": dur, "created_at": ca})
					}
				}
				if attempts == nil {
					attempts = []map[string]interface{}{}
				}
				middleware.WriteJSON(w, req, http.StatusOK, map[string]interface{}{"id": did, "event_id": eid, "endpoint_id": epid, "status": status, "attempt_count": attempt, "created_at": created, "attempts": attempts})
			})
		})
	})

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: r,
	}
	// graceful shutdown
	go func() {
		logger.Info("starting server", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down server...")
	ctxShutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctxShutdown); err != nil {
		logger.Error("shutdown error", "error", err)
	}
	pool.Close()
	if rdb != nil {
		rdb.Close()
	}
	logger.Info("server exited")
}

func runWorker() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("worker starting", "concurrency", cfg.WorkerConcurrency, "timeout", cfg.WebhookTimeout)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := storage.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	rdb, err := queue.NewClient(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to connect redis", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()

	workerCfg := &delivery.WorkerConfig{
		Concurrency:    cfg.WorkerConcurrency,
		Timeout:        cfg.WebhookTimeout,
		MaxRetries:     cfg.MaxRetryAttempts,
		RetrySchedule:  cfg.RetrySchedule,
		MaxDelay:       cfg.RetryMaxDelay,
		Jitter:         cfg.RetryJitter,
		AllowPrivate:   cfg.AllowPrivateIPs,
		Allowlist:      cfg.AllowlistCIDRs,
		EncryptionKey:  cfg.EncryptionKey,
	}
	worker := delivery.NewWorker(pool, rdb, workerCfg)
	logger.Info("worker ready, consuming", "stream", "webhooker:deliveries", "group", "webhook-workers")
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped", "error", err)
	}
	logger.Info("worker shutting down...")
}

func runMigrate() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()
	files := []string{"migrations/000001_init.up.sql", "migrations/000002_retry_policy.up.sql"}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", f, err)
			os.Exit(1)
		}
		if _, err := pool.Exec(ctx, string(b)); err != nil {
			fmt.Fprintf(os.Stderr, "migrate %s failed: %v\n", f, err)
			os.Exit(1)
		}
		fmt.Printf("migrated %s\n", f)
	}
	fmt.Println("migrations done")
}

func extractBearerToken(h string) (string, bool) {
	const bearer = "Bearer "
	if len(h) <= len(bearer) {
		return "", false
	}
	if h[:len(bearer)] != bearer {
		return "", false
	}
	return h[len(bearer):], true
}

func parseLimit(s string, max int) (int, error) {
	// simple
	var v int
	_, err := fmt.Sscan(s, &v)
	if err != nil {
		return 0, err
	}
	if v <= 0 {
		return 50, nil
	}
	if v > max {
		return max, nil
	}
	return v, nil
}

// ensure pgxpool import used
var _ = pgxpool.Pool{}
