package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey int

const poolKey contextKey = iota

func WithPool(ctx context.Context, pool *pgxpool.Pool) context.Context {
	return context.WithValue(ctx, poolKey, pool)
}

func poolFromCtx(ctx context.Context) (*pgxpool.Pool, error) {
	pool, ok := ctx.Value(poolKey).(*pgxpool.Pool)
	if !ok || pool == nil {
		return nil, fmt.Errorf("db: no pool in context - call db.WithPool first")
	}
	return pool, nil
}

func TenantContext(ctx context.Context, tenantID pgtype.UUID) (context.Context, pgx.Tx, error) {
	pool, err := poolFromCtx(ctx)
	if err != nil {
		return ctx, nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("db.TenantContext: begin tx: %w", err)
	}

	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		rollbackErr := tx.Rollback(ctx)
		return ctx, nil, errors.Join(fmt.Errorf("db.TenantContext: SET LOCAL: %w", err), rollbackErr)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_user"); err != nil {
		rollbackErr := tx.Rollback(ctx)
		return ctx, nil, errors.Join(fmt.Errorf("db.TenantContext: SET LOCAL ROLE: %w", err), rollbackErr)
	}
	return ctx, tx, nil
}
