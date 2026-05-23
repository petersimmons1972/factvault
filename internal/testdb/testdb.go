package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/pressly/goose/v3"

	"github.com/petersimmons1972/factvault/internal/db"
)

var (
	once     sync.Once
	startErr error
	dp       *dockertest.Pool
	res      *dockertest.Resource
	dsn      string
)

func StartContainer() {
	once.Do(func() {
		var err error
		dp, err = dockertest.NewPool("")
		if err != nil {
			startErr = err
			return
		}

		res, err = dp.RunWithOptions(&dockertest.RunOptions{
			Repository: "pgvector/pgvector",
			Tag:        "pg16",
			Env: []string{
				"POSTGRES_USER=factvault_test",
				"POSTGRES_PASSWORD=factvault_test",
				"POSTGRES_DB=factvault_test",
			},
		}, func(cfg *docker.HostConfig) {
			cfg.AutoRemove = true
			cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
		})
		if err != nil {
			startErr = err
			return
		}

		dsn = fmt.Sprintf(
			"postgres://factvault_test:factvault_test@localhost:%s/factvault_test?sslmode=disable",
			res.GetPort("5432/tcp"),
		)

		var sqlDB *sql.DB
		if err := dp.Retry(func() error {
			sqlDB, err = sql.Open("pgx", dsn)
			if err != nil {
				return err
			}
			return sqlDB.PingContext(context.Background())
		}); err != nil {
			startErr = err
			return
		}
		defer sqlDB.Close()

		if err := goose.SetDialect("postgres"); err != nil {
			startErr = err
			return
		}
		if err := goose.RunContext(context.Background(), "up", sqlDB, migrationsPath()); err != nil {
			startErr = err
			return
		}
	})
}

func StopContainer() {
	if dp != nil && res != nil {
		_ = dp.Purge(res)
	}
}

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if startErr != nil {
		t.Fatalf("testdb startup failed: %v", startErr)
	}
	pool, err := db.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("db.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Setup initializes the test database container and returns a connection pool.
// This is a convenience wrapper that calls StartContainer once and New for each test.
func Setup(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()
	StartContainer()
	return New(t)
}

func migrationsPath() string {
	cwd, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(cwd, "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		cwd = filepath.Dir(cwd)
	}
	return "migrations"
}
