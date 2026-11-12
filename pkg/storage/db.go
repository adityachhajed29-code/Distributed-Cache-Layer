package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	_ "github.com/lib/pq"
	"time"
)

type Record struct {
	OperationID string `json:"-"`
	Key         string `json:"key"`
	Val         []byte `json:"val"`
	Version     int64  `json:"version"`
	Deleted     bool   `json:"deleted,omitempty"`
}

type Store interface {
	Get(context.Context, string) (Record, error)
	Write(context.Context, Record) (Record, error)
}

type DB struct{ *sql.DB }

func Open(ctx context.Context, dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS cache_records (
 key TEXT PRIMARY KEY, val BYTEA NOT NULL, version BIGINT NOT NULL DEFAULT 1,
 deleted BOOLEAN NOT NULL DEFAULT FALSE, updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS cache_write_receipts (operation_id TEXT PRIMARY KEY, record JSONB NOT NULL)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db}, nil
}

func (d *DB) Get(ctx context.Context, key string) (Record, error) {
	r := Record{Key: key}
	err := d.QueryRowContext(ctx, `SELECT val,version,deleted FROM cache_records WHERE key=$1 AND NOT deleted`, key).Scan(&r.Val, &r.Version, &r.Deleted)
	return r, err
}

// Version increments occur under PostgreSQL's row lock, including across shards.
func (d *DB) Write(ctx context.Context, r Record) (Record, error) {
	records, err := d.WriteBatch(ctx, []Record{r})
	if err != nil {
		return Record{}, err
	}
	return records[0], nil
}

// WriteBatch commits the ordered batch in one PostgreSQL transaction.
func (d *DB) WriteBatch(ctx context.Context, records []Record) ([]Record, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result := make([]Record, 0, len(records))
	for _, r := range records {
		if r.OperationID != "" {
			var encoded []byte
			err := tx.QueryRowContext(ctx, "SELECT record FROM cache_write_receipts WHERE operation_id=$1", r.OperationID).Scan(&encoded)
			if err == nil {
				var saved Record
				if err = json.Unmarshal(encoded, &saved); err != nil {
					return nil, err
				}
				result = append(result, saved)
				continue
			}
			if err != sql.ErrNoRows {
				return nil, err
			}
		}

		if r.Val == nil {
			r.Val = []byte{}
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO cache_records(key,val,deleted) VALUES($1,$2,$3)
 ON CONFLICT(key) DO UPDATE SET val=EXCLUDED.val,deleted=EXCLUDED.deleted,
 version=cache_records.version+1,updated_at=now() RETURNING version`, r.Key, r.Val, r.Deleted).Scan(&r.Version)
		if err != nil {
			return nil, err
		}
		if r.OperationID != "" {
			encoded, err := json.Marshal(r)
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO cache_write_receipts(operation_id,record) VALUES($1,$2)", r.OperationID, string(encoded)); err != nil {
				return nil, err
			}
		}
		result = append(result, r)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
