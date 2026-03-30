package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

// DB wraps a PostgreSQL connection pool.
type DB struct {
	conn *sql.DB
}

// Order is the domain entity persisted in the database.
type Order struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// NewDB opens a connection to PostgreSQL and initialises the schema.
// It retries up to 5 times with 2-second delays to tolerate slow pod startup.
func NewDB(dsn string) (*DB, error) {
	var (
		conn *sql.DB
		err  error
	)
	for attempt := 1; attempt <= 5; attempt++ {
		conn, err = sql.Open("postgres", dsn)
		if err != nil {
			return nil, fmt.Errorf("open db: %w", err)
		}
		if err = conn.Ping(); err == nil {
			break
		}
		log.Printf("db not ready (attempt %d/5): %v", attempt, err)
		conn.Close()
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("db ping after retries: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return db, nil
}

func (db *DB) initSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS orders (
			id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			title      TEXT        NOT NULL,
			status     TEXT        NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`)
	return err
}

// CreateOrder inserts a new order and returns the created record.
func (db *DB) CreateOrder(title string) (*Order, error) {
	var o Order
	err := db.conn.QueryRow(
		`INSERT INTO orders (title) VALUES ($1) RETURNING id, title, status, created_at`,
		title,
	).Scan(&o.ID, &o.Title, &o.Status, &o.CreatedAt)
	if err != nil {
		dbOperationsTotal.WithLabelValues("create", "error").Inc()
		return nil, err
	}
	dbOperationsTotal.WithLabelValues("create", "success").Inc()
	return &o, nil
}

// ListOrders returns all orders ordered by creation time descending.
func (db *DB) ListOrders() ([]Order, error) {
	rows, err := db.conn.Query(
		`SELECT id, title, status, created_at FROM orders ORDER BY created_at DESC`,
	)
	if err != nil {
		dbOperationsTotal.WithLabelValues("list", "error").Inc()
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.Title, &o.Status, &o.CreatedAt); err != nil {
			dbOperationsTotal.WithLabelValues("list", "error").Inc()
			return nil, err
		}
		orders = append(orders, o)
	}
	dbOperationsTotal.WithLabelValues("list", "success").Inc()
	return orders, rows.Err()
}

// GetOrder retrieves a single order by ID.
// Returns (nil, nil) when the order does not exist.
func (db *DB) GetOrder(id string) (*Order, error) {
	var o Order
	err := db.conn.QueryRow(
		`SELECT id, title, status, created_at FROM orders WHERE id = $1`, id,
	).Scan(&o.ID, &o.Title, &o.Status, &o.CreatedAt)
	if err == sql.ErrNoRows {
		dbOperationsTotal.WithLabelValues("get", "not_found").Inc()
		return nil, nil
	}
	if err != nil {
		dbOperationsTotal.WithLabelValues("get", "error").Inc()
		return nil, err
	}
	dbOperationsTotal.WithLabelValues("get", "success").Inc()
	return &o, nil
}

// DeleteOrder removes an order by ID.
// Returns false when the order did not exist.
func (db *DB) DeleteOrder(id string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM orders WHERE id = $1`, id)
	if err != nil {
		dbOperationsTotal.WithLabelValues("delete", "error").Inc()
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		dbOperationsTotal.WithLabelValues("delete", "not_found").Inc()
		return false, nil
	}
	dbOperationsTotal.WithLabelValues("delete", "success").Inc()
	return true, nil
}
