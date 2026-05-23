package workers_test

import (
	"os"
	"testing"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestMain(m *testing.M) {
	testdb.StartContainer()
	code := m.Run()
	testdb.StopContainer()
	os.Exit(code)
}
