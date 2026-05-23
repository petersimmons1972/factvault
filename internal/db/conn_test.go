package db_test

import (
	"context"
	"testing"

	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestNewPool_PingSucceeds(t *testing.T) {
	pool := testdb.New(t)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
}

func TestNewPool_VectorTypeRegistered(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pool.Acquire: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "CREATE TEMP TABLE _vec_registration_test (v vector(1024))"); err != nil {
		t.Fatalf("CREATE TEMP TABLE: %v", err)
	}

	sample := make([]float32, 1024)
	for i := range sample {
		sample[i] = float32(i) * 0.001
	}
	vec := pgvector.NewVector(sample)

	if _, err := conn.Exec(ctx, "INSERT INTO _vec_registration_test VALUES ($1)", vec); err != nil {
		t.Fatalf("INSERT vector: %v", err)
	}

	var result pgvector.Vector
	if err := conn.QueryRow(ctx, "SELECT v FROM _vec_registration_test LIMIT 1").Scan(&result); err != nil {
		t.Fatalf("Scan vector: %v", err)
	}
	if len(result.Slice()) != 1024 {
		t.Fatalf("expected 1024-dimension vector, got %d", len(result.Slice()))
	}
}
