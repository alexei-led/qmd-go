package db_test

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/qmd-go/internal/db"
)

func TestOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	var mode string
	err = d.QueryRow("PRAGMA journal_mode").Scan(&mode)
	require.NoError(t, err)
	assert.Equal(t, "wal", mode)

	var fk int
	err = d.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	require.NoError(t, err)
	assert.Equal(t, 1, fk)

	var timeout int
	err = d.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	require.NoError(t, err)
	assert.Equal(t, 5000, timeout)
}

func TestOpen_InvalidPath(t *testing.T) {
	_, err := db.Open("/nonexistent/dir/test.db")
	assert.Error(t, err)
}

func TestVecAvailable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	// Without sqlite-vec compiled in, this should return false.
	available := db.VecAvailable(d)
	assert.False(t, available, "vec should not be available without sqlite-vec extension")
}

func TestConcurrentReads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	_, err = d.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, val TEXT)")
	require.NoError(t, err)
	_, err = d.Exec("INSERT INTO test (val) VALUES ('hello'), ('world'), ('foo')")
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, 10)

	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var count int
			errs[i] = d.QueryRow("SELECT count(*) FROM test").Scan(&count)
		}()
	}

	wg.Wait()
	for _, e := range errs {
		assert.NoError(t, e)
	}
}
