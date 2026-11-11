package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

type endpointKey struct{}
type probeKey struct{}
type endpointPlans struct {
	mu     sync.Mutex
	plans  map[string]json.RawMessage
	routes map[string]map[string]bool
	errors []string
}

func (p *endpointPlans) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	route, _ := ctx.Value(endpointKey{}).(string)
	if route == "" || ctx.Value(probeKey{}) != nil {
		return ctx
	}
	sql := strings.TrimSpace(data.SQL)
	if strings.HasPrefix(sql, "--") {
		_, sql, _ = strings.Cut(sql, "\n")
		sql = strings.TrimSpace(sql)
	}
	upper := strings.ToUpper(sql)
	if !(strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") || strings.HasPrefix(upper, "INSERT") || strings.HasPrefix(upper, "UPDATE") || strings.HasPrefix(upper, "DELETE")) || strings.Contains(upper, "NEXTVAL(") {
		return ctx
	}
	hash := sha256.Sum256([]byte(sql))
	key := hex.EncodeToString(hash[:8])
	p.mu.Lock()
	if p.routes[route] == nil {
		p.routes[route] = map[string]bool{}
	}
	p.routes[route][key] = true
	_, done := p.plans[key]
	p.mu.Unlock()
	if done {
		return ctx
	}
	probe := context.WithValue(ctx, probeKey{}, true)
	// We roll back measurements so each mutation happens only once.
	transactional := conn.PgConn().TxStatus() == 'T'
	ownTransaction := !transactional && !strings.HasPrefix(upper, "SELECT")
	var err error
	if transactional {
		_, err = conn.Exec(probe, "SAVEPOINT endpoint_plan_probe")
	} else if ownTransaction {
		_, err = conn.Exec(probe, "BEGIN")
	}
	var raw []byte
	if err == nil {
		err = conn.QueryRow(probe, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+sql, data.Args...).Scan(&raw)
	}
	if transactional {
		_, rollbackErr := conn.Exec(probe, "ROLLBACK TO SAVEPOINT endpoint_plan_probe")
		_, releaseErr := conn.Exec(probe, "RELEASE SAVEPOINT endpoint_plan_probe")
		if err == nil {
			err = rollbackErr
		}
		if err == nil {
			err = releaseErr
		}
	}
	if ownTransaction {
		_, rollbackErr := conn.Exec(probe, "ROLLBACK")
		if err == nil {
			err = rollbackErr
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.errors = append(p.errors, fmt.Sprintf("%s: SQL plan %s failed", route, key))
		return ctx
	}
	// We remove literal values from plans so authentication data stays private.
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		p.errors = append(p.errors, "invalid plan JSON")
		return ctx
	}
	redactPlan(tree)
	clean, _ := json.MarshalIndent(tree, "", "  ")
	p.plans[key] = clean
	return ctx
}
func (*endpointPlans) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

var planLiteral = regexp.MustCompile(`'(?:[^']|'')*'`)

func redactPlan(value any) {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			if text, ok := item.(string); ok {
				v[k] = planLiteral.ReplaceAllString(text, "'[redacted]'")
			} else {
				redactPlan(item)
			}
		}
	case []any:
		for _, item := range v {
			redactPlan(item)
		}
	}
}
func (p *endpointPlans) write(folder string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	}
	for key, plan := range p.plans {
		if err := os.WriteFile(filepath.Join(folder, key+".json"), plan, 0600); err != nil {
			return err
		}
	}
	return nil
}
