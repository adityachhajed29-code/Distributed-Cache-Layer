package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresPersistence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := fmt.Sprintf("test:%d", time.Now().UnixNano())
	defer db.ExecContext(ctx, "DELETE FROM cache_records WHERE key=$1", key)
	rows, err := db.WriteBatch(ctx, []Record{{Key: key, Val: []byte{0, 1, 2}}, {Key: key, Val: []byte("new")}})
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Version != 1 || rows[1].Version != 2 {
		t.Fatalf("unexpected versions %+v", rows)
	}
	actual, err := db.Get(ctx, key)
	if err != nil || string(actual.Val) != "new" {
		t.Fatalf("read failed %+v %v", actual, err)
	}
	operation := Record{Key: key, Val: []byte("once"), OperationID: key + ":operation"}
	first, err := db.Write(ctx, operation)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM cache_write_receipts WHERE operation_id=$1", operation.OperationID)
	newer, err := db.Write(ctx, Record{Key: key, Val: []byte("latest")})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := db.Write(ctx, operation)
	if err != nil || replay.Version != first.Version {
		t.Fatalf("retry changed version: %+v %v", replay, err)
	}
	current, err := db.Get(ctx, key)
	if err != nil || current.Version != newer.Version || string(current.Val) != "latest" {
		t.Fatalf("replay overwrote newer data: %+v %v", current, err)
	}
	if _, err = db.Write(ctx, Record{Key: key, Deleted: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Get(ctx, key); err != sql.ErrNoRows {
		t.Fatalf("deleted record visible: %v", err)
	}
}

// All shards can initialize a new database namespace simultaneously.
func TestConcurrentSchemaInitialization(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("startup_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	var workers sync.WaitGroup
	for i := 0; i < 5; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			db, err := Open(ctx, parsed.String())
			if err != nil {
				t.Error(err)
				return
			}
			db.Close()
		}()
	}
	workers.Wait()
}
