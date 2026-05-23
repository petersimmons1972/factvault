package workers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
	"github.com/petersimmons1972/factvault/internal/db"
)

type DossierWorker struct {
	DB *pgxpool.Pool
}

type DossierOptions struct {
	TenantID string
	EntityID string
	All      bool
	Limit    int
}

func (w DossierWorker) RunOnce(ctx context.Context, opts DossierOptions) (int, error) {
	if w.DB == nil {
		return 0, fmt.Errorf("dossier worker: nil db pool")
	}
	if opts.TenantID == "" {
		return 0, fmt.Errorf("tenant id required")
	}
	var tenant pgtype.UUID
	if err := tenant.Scan(opts.TenantID); err != nil {
		return 0, fmt.Errorf("invalid tenant id: %w", err)
	}
	txCtx := db.WithPool(ctx, w.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(txCtx)

	ids, err := dossierEntityIDs(txCtx, tx, opts)
	if err != nil {
		return 0, err
	}
	for _, entityID := range ids {
		bundle, err := assembler.Assemble(txCtx, tx, []string{entityID}, 0, opts.TenantID)
		if err != nil {
			return 0, err
		}
		data, err := json.Marshal(bundle)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(txCtx, `
			INSERT INTO dossiers (id, tenant_id, entity_id, assembled_at, bundle)
			VALUES ($1, $2::uuid, $3::uuid, now(), $4::jsonb)
			ON CONFLICT (tenant_id, entity_id)
			DO UPDATE SET assembled_at = EXCLUDED.assembled_at, bundle = EXCLUDED.bundle
		`, uuid.NewString(), opts.TenantID, entityID, string(data)); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(txCtx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func dossierEntityIDs(ctx context.Context, tx pgx.Tx, opts DossierOptions) ([]string, error) {
	if opts.EntityID != "" {
		return []string{opts.EntityID}, nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := tx.Query(ctx, `
		SELECT e.id::text
		FROM entities e
		LEFT JOIN dossiers d ON d.tenant_id = e.tenant_id AND d.entity_id = e.id
		WHERE e.tenant_id = $1::uuid
		  AND ($2 OR d.id IS NULL OR d.assembled_at <= now() - interval '24 hours')
		ORDER BY e.created_at ASC
		LIMIT $3
	`, opts.TenantID, opts.All, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
