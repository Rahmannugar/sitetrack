package database

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, receive func(string), logger *slog.Logger) {
	for ctx.Err() == nil {
		if err := listen(ctx, pool, channel, receive); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "PostgreSQL listener disconnected; polling remains active")
		}
		timer := time.NewTimer(time.Second + time.Duration(rand.Int64N(int64(time.Second))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func listen(ctx context.Context, pool *pgxpool.Pool, channel string, receive func(string)) error {
	setup, cancel := context.WithTimeout(ctx, 5*time.Second)
	connection, err := pool.Acquire(setup)
	if err != nil {
		cancel()
		return err
	}
	defer func() {
		// We close the listening connection before returning its slot to the pool.
		closeContext, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = connection.Conn().Close(closeContext)
		connection.Release()
	}()
	_, err = connection.Exec(setup, "LISTEN "+pgx.Identifier{channel}.Sanitize())
	cancel()
	if err != nil {
		return err
	}
	// We scan after subscribing, including after a reconnect, to cover missed signals.
	receive("")
	for ctx.Err() == nil {
		wait, stop := context.WithTimeout(ctx, time.Minute)
		notification, err := connection.Conn().WaitForNotification(wait)
		timedOut := errors.Is(wait.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		stop()
		if timedOut {
			ping, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = connection.Ping(ping)
			cancel()
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if notification != nil {
			receive(notification.Payload)
		}
	}
	return ctx.Err()
}
