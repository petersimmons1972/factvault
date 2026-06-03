package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/petersimmons1972/factvault/internal/db"
)

var (
	once     sync.Once
	startErr error
	contName string
	dsn      string
)

func StartContainer() {
	once.Do(func() {
		var err error
		releaseLock, err := testDBStartupLock()
		if err != nil {
			startErr = err
			return
		}
		defer releaseLock()

		repository, tag := postgresImage()
		contName = fmt.Sprintf("factvault-testdb-%d", time.Now().UnixNano())
		image := fmt.Sprintf("%s:%s", repository, tag)
		err = runDockerCommand(
			"run", "-d",
			"--name", contName,
			"--rm",
			"-p", "127.0.0.1::5432",
			"-e", "POSTGRES_USER=factvault_test",
			"-e", "POSTGRES_PASSWORD=factvault_test",
			"-e", "POSTGRES_DB=factvault_test",
			"-e", "POSTGRES_INITDB_ARGS=--no-sync",
			image,
		)
		if err != nil {
			startErr = err
			return
		}
		hostPort, err := dockerMappedPort(contName, "5432/tcp")
		if err != nil {
			startErr = err
			return
		}

		dsn = fmt.Sprintf(
			"postgres://factvault_test:factvault_test@localhost:%s/factvault_test?sslmode=disable",
			hostPort,
		)

		var sqlDB *sql.DB
		if err := retry(60, 500*time.Millisecond, func() error {
			sqlDB, err = sql.Open("pgx", dsn)
			if err != nil {
				return err
			}
			return sqlDB.PingContext(context.Background())
		}); err != nil {
			startErr = err
			return
		}
		defer func() {
			if err := sqlDB.Close(); err != nil {
				// Best-effort close of migration DB connection.
			}
		}()

		if err := goose.SetDialect("postgres"); err != nil {
			startErr = err
			return
		}
		if err := goose.RunContext(context.Background(), "up", sqlDB, migrationsPath()); err != nil {
			startErr = err
			return
		}
		if _, err := sqlDB.ExecContext(context.Background(), "GRANT app_user TO current_user"); err != nil {
			startErr = err
			return
		}
	})
}

func StopContainer() {
	if contName != "" {
		if err := runDockerCommand("rm", "-f", contName); err != nil {
			// Best-effort container removal; ignore error.
		}
		contName = ""
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
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	for range 8 {
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
		if closeErr := f.Close(); closeErr != nil {
			// Best-effort close on error path.
		}
		return nil, fmt.Errorf("lock testdb startup: %w", err)
	}
	return func() {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			// Best-effort unlock; ignore error.
		}
		if err := f.Close(); err != nil {
			// Best-effort close; ignore error.
		}
	}, nil
}

func postgresImage() (repository, tag string) {
	image := os.Getenv("FACTVAULT_TEST_POSTGRES_IMAGE")
	if image == "" {
		image = "ankane/pgvector:latest"
	}
	slash := strings.LastIndexByte(image, '/')
	colon := strings.LastIndexByte(image, ':')
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}

func runDockerCommand(args ...string) error {
	cmd := exec.Command("docker", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerMappedPort(containerName, containerPort string) (string, error) {
	cmd := exec.Command("docker", "port", containerName, containerPort)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker port %s %s: %w: %s", containerName, containerPort, err, strings.TrimSpace(string(out)))
	}
	target := strings.TrimSpace(string(out))
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", fmt.Errorf("parse docker port %q: %w", target, err)
	}
	if host == "" || port == "" {
		return "", fmt.Errorf("invalid docker port mapping %q", target)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("invalid mapped port %q: %w", port, err)
	}
	return port, nil
}

func retry(attempts int, delay time.Duration, fn func() error) error {
	var lastErr error
	for range attempts {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(delay)
	}
	return fmt.Errorf("retry exhausted after %d attempts: %w", attempts, lastErr)
}
