package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers pgx driver for database/sql
	"github.com/pressly/goose/v3"

	"github.com/petersimmons1972/factvault/internal/db"
)

var (
	once         sync.Once
	startErr     error
	contName     string
	volumeName   string
	dsn          string
	cleanupOnce  sync.Once
	guardianPipe *os.File
)

const (
	restrictedRoleUser     = "factvault_app"
	restrictedRolePassword = "factvault_app" //nolint:gosec // test-only fixed password for a throwaway restricted role
)

func init() { //nolint:gochecknoinits // the re-executed test binary must become the cleanup guardian before TestMain
	if os.Getenv("TESTDB_CLEANUP_GUARDIAN") != "1" {
		return
	}
	container := os.Getenv("TESTDB_CONTAINER")
	volume := os.Getenv("TESTDB_VOLUME")
	_, _ = io.Copy(io.Discard, os.NewFile(3, "testdb-parent"))
	for _, command := range dockerCleanupCommands(container, volume) {
		_ = runDockerCommand(command...)
	}
	os.Exit(0)
}

// StartContainer launches a shared PostgreSQL container for tests in this process.
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
		volumeName = contName
		image := fmt.Sprintf("%s:%s", repository, tag)
		err = runDockerCommand(dockerRunArgs(contName, volumeName, image)...)
		if err != nil {
			startErr = err
			return
		}
		if err := startCleanupGuardian(contName, volumeName); err != nil {
			startErr = err
			return
		}
		installSignalCleanup()
		started := false
		defer func() {
			if !started {
				StopContainer()
			}
		}()
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
				fmt.Fprintf(os.Stderr, "close migration db: %v\n", err)
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
		if _, err := sqlDB.ExecContext(context.Background(), `
DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '`+restrictedRoleUser+`') THEN
		CREATE ROLE `+restrictedRoleUser+` LOGIN PASSWORD '`+restrictedRolePassword+`' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT;
	END IF;
END;
$$;
GRANT app_user TO `+restrictedRoleUser+`;
GRANT CONNECT ON DATABASE factvault_test TO `+restrictedRoleUser+`;
GRANT USAGE ON SCHEMA public TO `+restrictedRoleUser+`;
`); err != nil {
			startErr = err
			return
		}
		started = true
	})
}

// StopContainer stops and removes the shared PostgreSQL test container.
func StopContainer() {
	cleanupOnce.Do(func() {
		for _, command := range dockerCleanupCommands(contName, volumeName) {
			if err := runDockerCommand(command...); err != nil {
				fmt.Fprintf(os.Stderr, "testdb cleanup: %v\n", err)
			}
		}
		if guardianPipe != nil {
			if err := guardianPipe.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "close testdb cleanup guardian: %v\n", err)
			}
			guardianPipe = nil
		}
		contName = ""
		volumeName = ""
	})
}

func startCleanupGuardian(containerName, dataVolumeName string) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create testdb cleanup guardian pipe: %w", err)
	}
	cmd := exec.Command(os.Args[0]) //nolint:gosec // re-executes the current trusted test binary as a cleanup guardian
	cmd.ExtraFiles = []*os.File{reader}
	cmd.Env = append(
		os.Environ(),
		"TESTDB_CLEANUP_GUARDIAN=1",
		"TESTDB_CONTAINER="+containerName,
		"TESTDB_VOLUME="+dataVolumeName,
	)
	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return fmt.Errorf("start testdb cleanup guardian: %w", err)
	}
	if err := reader.Close(); err != nil {
		_ = writer.Close()
		return fmt.Errorf("close parent guardian reader: %w", err)
	}
	guardianPipe = writer
	go func() {
		if err := cmd.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "testdb cleanup guardian: %v\n", err)
		}
	}()
	return nil
}

func dockerRunArgs(containerName, dataVolumeName, image string) []string {
	return []string{
		"run", "-d", "--rm",
		"--name", containerName,
		"--mount", "type=volume,source=" + dataVolumeName + ",target=/var/lib/postgresql/data",
		"-p", "127.0.0.1::5432",
		"-e", "POSTGRES_USER=factvault_test",
		"-e", "POSTGRES_PASSWORD=factvault_test",
		"-e", "POSTGRES_DB=factvault_test",
		"-e", "POSTGRES_INITDB_ARGS=--no-sync",
		image,
	}
}

func dockerCleanupCommands(containerName, dataVolumeName string) [][]string {
	commands := [][]string{}
	if containerName != "" {
		commands = append(commands, []string{"rm", "-f", "-v", containerName})
	}
	if dataVolumeName != "" {
		commands = append(commands, []string{"volume", "rm", "-f", dataVolumeName})
	}
	return commands
}

func installSignalCleanup() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		sig := <-signals
		StopContainer()
		signal.Stop(signals)
		if unixSignal, ok := sig.(syscall.Signal); ok {
			os.Exit(128 + int(unixSignal))
		}
		os.Exit(1)
	}()
}

type resourceLister interface {
	listContainers() ([]string, error)
	listVolumes() ([]string, error)
}

type dockerResourceLister struct{}

func (dockerResourceLister) listContainers() ([]string, error) {
	return dockerNames("ps", "-a", "--filter", "name=^/factvault-testdb-", "--format", "{{.Names}}")
}

func (dockerResourceLister) listVolumes() ([]string, error) {
	return dockerNames("volume", "ls", "--filter", "name=^factvault-testdb-", "--format", "{{.Name}}")
}

func dockerNames(args ...string) ([]string, error) {
	cmd := exec.Command("docker", args...) //nolint:gosec // fixed Docker inspection arguments used only by the test harness
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	output := strings.TrimSpace(string(out))
	if output == "" {
		return []string{}, nil
	}
	return strings.Split(output, "\n"), nil
}

func findTestDBLeaks(lister resourceLister) ([]string, error) {
	containers, err := lister.listContainers()
	if err != nil {
		return nil, fmt.Errorf("list testdb containers: %w", err)
	}
	volumes, err := lister.listVolumes()
	if err != nil {
		return nil, fmt.Errorf("list testdb volumes: %w", err)
	}
	leaks := []string{}
	for _, name := range containers {
		if strings.HasPrefix(name, "factvault-testdb-") {
			leaks = append(leaks, "container "+name)
		}
	}
	for _, name := range volumes {
		if strings.HasPrefix(name, "factvault-testdb-") {
			leaks = append(leaks, "volume "+name)
		}
	}
	return leaks, nil
}

// New returns a pgx pool connected to the current test database.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if startErr != nil {
		t.Fatalf("testdb startup failed: %v", startErr)
	}
	pool, err := db.NewPool(t.Context(), dsn)
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
	return NewWithContext(ctx, t)
}

// NewWithContext returns a connection pool bound to the provided context.
// This should be preferred over New when callers already have an active context.
func NewWithContext(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()
	if startErr != nil {
		t.Fatalf("testdb startup failed: %v", startErr)
	}
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("db.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// RestrictedPool returns a pool connected as a non-superuser role so FORCE RLS
// behavior is exercised in tests instead of being bypassed by the container superuser.
func RestrictedPool(ctx context.Context, t *testing.T, applicationName string) *pgxpool.Pool {
	t.Helper()
	StartContainer()
	if startErr != nil {
		t.Fatalf("testdb startup failed: %v", startErr)
	}
	restrictedDSN, err := restrictedRoleDSN(applicationName)
	if err != nil {
		t.Fatalf("restrictedRoleDSN: %v", err)
	}
	pool, err := db.NewPool(ctx, restrictedDSN)
	if err != nil {
		t.Fatalf("db.NewPool restricted: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
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
	// lockPath is constrained to os.TempDir()+filename and not user-provided.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: lock path is fixed in test code.
	if err != nil {
		return nil, fmt.Errorf("open testdb startup lock: %w", err)
	}
	fd, err := fileDescriptor(f)
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close on error: %v\n", closeErr)
		}
		return nil, err
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close on error: %v\n", closeErr)
		}
		return nil, fmt.Errorf("lock testdb startup: %w", err)
	}
	return func() {
		unlockFd, derr := fileDescriptor(f)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "testdb startup lock descriptor: %v\n", derr)
			if err := f.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "close: %v\n", err)
			}
			return
		}
		if err := syscall.Flock(unlockFd, syscall.LOCK_UN); err != nil {
			fmt.Fprintf(os.Stderr, "unlock: %v\n", err)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close: %v\n", err)
		}
	}, nil
}

func fileDescriptor(f *os.File) (int, error) {
	fd := f.Fd()
	if fd > uintptr(^uint(0)>>1) {
		return 0, fmt.Errorf("file descriptor %d overflows int", fd)
	}
	return int(fd), nil
}

func postgresImage() (repository, tag string) {
	image := os.Getenv("FACTVAULT_TEST_POSTGRES_IMAGE")
	if image == "" {
		// Pinned to a specific Postgres major so version-sensitive behavior
		// (e.g. recursive-CTE parse rules — see internal/assembler/graph.go)
		// is reproducibly validated and a regression cannot silently slip on
		// a moving :latest tag. Override via FACTVAULT_TEST_POSTGRES_IMAGE.
		image = "pgvector/pgvector:pg16"
	}
	slash := strings.LastIndexByte(image, '/')
	colon := strings.LastIndexByte(image, ':')
	if colon > slash {
		return image[:colon], image[colon+1:]
	}
	return image, "latest"
}

func restrictedRoleDSN(applicationName string) (string, error) {
	parsed, err := neturl.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	parsed.User = neturl.UserPassword(restrictedRoleUser, restrictedRolePassword)
	query := parsed.Query()
	if applicationName != "" {
		query.Set("application_name", applicationName)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func runDockerCommand(args ...string) error {
	cmd := exec.Command("docker", args...) //nolint:gosec // G204: command is fixed, args are constrained and validated by test environment
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerMappedPort(containerName, containerPort string) (string, error) {
	cmd := exec.Command("docker", "port", containerName, containerPort) //nolint:gosec // G204: command is fixed; container identifiers are sourced from test fixtures and validated
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
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	return fmt.Errorf("retry exhausted after %d attempts: %w", attempts, lastErr)
}
