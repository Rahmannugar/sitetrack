package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Option func(*pgxpool.Config)

func WithTracer(tracer pgx.QueryTracer) Option {
	return func(cfg *pgxpool.Config) { cfg.ConnConfig.Tracer = tracer }
}

func Open(ctx context.Context, databaseURL string, maxConnections int32, options ...Option) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// We omit parser errors because they can include database credentials.
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	for _, option := range options {
		option(cfg)
	}
	cfg.MaxConns = maxConnections
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("could not create PostgreSQL pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("could not connect to PostgreSQL")
	}
	return pool, nil
}
