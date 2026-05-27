package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	unlock   func()
)

func StartContainer() {
	once.Do(func() {
		var err error
		dp, err = dockertest.NewPool("")
		if err != nil {
			startErr = err
			return
		}
		releaseLock, err := testDBStartupLock()
		if err != nil {
			startErr = err
			return
		}
		unlock = releaseLock

		repository, tag := postgresImage()
		res, err = dp.RunWithOptions(&dockertest.RunOptions{
			Repository:   repository,
			Tag:          tag,
			ExposedPorts: []string{"5432/tcp"},
			Env: []string{
				"POSTGRES_USER=factvault_test",
				"POSTGRES_PASSWORD=factvault_test",
				"POSTGRES_DB=factvault_test",
				"POSTGRES_INITDB_ARGS=--no-sync",
			},
		}, func(cfg *docker.HostConfig) {
			cfg.AutoRemove = true
			cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
		})
		if err != nil {
			releaseTestDBStartupLock()
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
			releaseTestDBStartupLock()
			startErr = err
			return
		}
		defer sqlDB.Close()

		if err := goose.SetDialect("postgres"); err != nil {
			releaseTestDBStartupLock()
			startErr = err
			return
		}
		if err := goose.RunContext(context.Background(), "up", sqlDB, migrationsPath()); err != nil {
			releaseTestDBStartupLock()
			startErr = err
			return
		}
	})
}

func StopContainer() {
	if dp != nil && res != nil {
		_ = dp.Purge(res)
	}
	releaseTestDBStartupLock()
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

func testDBStartupLock() (func(), error) {
	lockPath := filepath.Join(os.TempDir(), "factvault-testdb-start.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open testdb startup lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock testdb startup: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func releaseTestDBStartupLock() {
	if unlock != nil {
		unlock()
		unlock = nil
	}
}

func postgresImage() (repository, tag string) {
	image := os.Getenv("FACTVAULT_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "factvault-postgres:latest"
	}
	slash := strings.LastIndexByte(image, '/')
	colon := strings.LastIndexByte(image, ':')
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}
