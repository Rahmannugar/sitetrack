package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Rahmannugar/authlier/password"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"sitetrack/internal/infra/database"
)

const DefaultPassword = "SiteTrack123!"

var ErrNotEmpty = errors.New("seed database contains application data; use a fresh database")
var ErrBusy = errors.New("another seed run is using this database")

type Progress struct {
	Phase       string
	Rows, Total int
	Elapsed     time.Duration
}
type Account struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}
type Report struct {
	Profile                string           `json:"profile"`
	AsOf                   time.Time        `json:"as_of"`
	Counts                 map[string]int64 `json:"counts"`
	Accounts               []Account        `json:"accounts"`
	Projects               []string         `json:"projects"`
	Materials              []uuid.UUID      `json:"materials"`
	PendingCompanyDelivery uuid.UUID        `json:"pending_company_delivery"`
	PendingProjectDelivery uuid.UUID        `json:"pending_project_delivery"`
	DatabaseBytes          int64            `json:"database_bytes"`
}

func safeError(phase string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", phase, err)
	}
	var problem *pgconn.PgError
	if errors.As(err, &problem) {
		return fmt.Errorf("%s failed (SQLSTATE %s, constraint %s)", phase, problem.Code, problem.ConstraintName)
	}
	return fmt.Errorf("%s failed", phase)
}
func tables(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]string, error) {
	rows, err := q.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='sitetrack' AND tablename<>'worker_maintenance' ORDER BY tablename")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
func empty(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, names []string) error {
	for _, name := range names {
		var exists bool
		if err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+pgx.Identifier{"sitetrack", name}.Sanitize()+" LIMIT 1)").Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrNotEmpty
		}
	}
	return nil
}
func Run(ctx context.Context, pool *pgxpool.Pool, profileName, rawPassword string, at time.Time, progress func(Progress)) (Report, error) {
	profile, err := ProfileByName(profileName)
	if err != nil {
		return Report{}, err
	}
	if rawPassword == "" {
		rawPassword = DefaultPassword
	}
	if n := utf8.RuneCountInString(rawPassword); n < 12 || n > 128 {
		return Report{}, errors.New("seed password must contain 12 to 128 characters")
	}
	names, err := tables(ctx, pool)
	if err != nil {
		return Report{}, safeError("inspect database", err)
	}
	if err := empty(ctx, pool, names); err != nil {
		if errors.Is(err, ErrNotEmpty) {
			return Report{}, err
		}
		return Report{}, safeError("inspect database", err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		return Report{}, safeError("migrate seed database", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, safeError("begin seed", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(739412018)").Scan(&locked); err != nil {
		return Report{}, safeError("lock seed", err)
	}
	if !locked {
		return Report{}, ErrBusy
	}
	names, err = tables(ctx, tx)
	if err != nil {
		return Report{}, safeError("inspect schema", err)
	}
	for _, name := range names {
		if _, err := tx.Exec(ctx, "LOCK TABLE "+pgx.Identifier{"sitetrack", name}.Sanitize()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			return Report{}, safeError("lock seed tables", err)
		}
	}
	if err := empty(ctx, tx, names); err != nil {
		if errors.Is(err, ErrNotEmpty) {
			return Report{}, err
		}
		return Report{}, safeError("inspect seed tables", err)
	}
	for _, name := range []string{"app_role", "task_status", "dependency_status", "invitation_status", "delivery_direction", "delivery_status"} {
		typ, err := tx.Conn().LoadType(ctx, "sitetrack."+name)
		if err != nil {
			return Report{}, safeError("load seed types", err)
		}
		tx.Conn().TypeMap().RegisterType(typ)
	}
	hash, err := password.Hash(rawPassword)
	if err != nil {
		return Report{}, errors.New("could not hash seed password")
	}
	p := newPlan(profile, at)
	rows, err := tx.Query(ctx, "SELECT nextval('sitetrack.project_number_seq') FROM generate_series(1,$1::integer)", profile.Projects)
	if err != nil {
		return Report{}, safeError("allocate project codes", err)
	}
	for rows.Next() {
		var number int64
		if err := rows.Scan(&number); err != nil {
			rows.Close()
			return Report{}, safeError("read project codes", err)
		}
		p.projects = append(p.projects, fmt.Sprintf("PRJ-%04d", number))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Report{}, safeError("read project codes", err)
	}
	copyRows := func(table string, columns []string, total int, row func(int) ([]any, error)) error {
		start := time.Now()
		for offset := 0; offset < total; offset += 10000 {
			size := min(10000, total-offset)
			_, err := tx.CopyFrom(ctx, pgx.Identifier{"sitetrack", table}, columns, pgx.CopyFromSlice(size, func(i int) ([]any, error) { return row(offset + i) }))
			if err != nil {
				return safeError("copy "+table, err)
			}
			if progress != nil {
				progress(Progress{Phase: table, Rows: offset + size, Total: total, Elapsed: time.Since(start)})
			}
		}
		return nil
	}
	if err := p.populate(ctx, tx, hash, copyRows); err != nil {
		return Report{}, err
	}
	if progress != nil {
		progress(Progress{Phase: "balances"})
	}
	for index, statement := range strings.Split(reconcileSQL, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		var args []any
		if strings.Contains(statement, "$1") {
			args = []any{p.at}
		}
		if _, err := tx.Exec(ctx, statement, args...); err != nil {
			return Report{}, safeError(fmt.Sprintf("reconcile stock step %d", index+1), err)
		}
	}
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		return Report{}, safeError("validate constraints", err)
	}
	if progress != nil {
		progress(Progress{Phase: "validation"})
	}
	report, err := p.validate(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, safeError("commit seed", err)
	}
	// We analyze committed seed data so subsequent query plans use its actual distribution.
	for _, table := range names {
		if _, err := pool.Exec(ctx, "ANALYZE "+pgx.Identifier{"sitetrack", table}.Sanitize()); err != nil {
			return report, safeError("seed committed; analyze", err)
		}
	}
	if err := pool.QueryRow(ctx, "SELECT pg_database_size(current_database())").Scan(&report.DatabaseBytes); err != nil {
		return report, safeError("seed committed; measure database", err)
	}
	return report, nil
}
