package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

type Guideline struct {
	ID       int
	Category string
	RuleText string
	Source   string
}

// Store persists guidelines/feedback on Redshift (same cluster as metric queries).
type Store struct {
	db *sql.DB
}

func Open(driver, connStr string) (*Store, error) {
	if driver == "" {
		driver = "postgres"
	}
	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, fmt.Errorf("open memory store (redshift): %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping memory store (redshift): %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) ListActiveGuidelines(ctx context.Context) ([]Guideline, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, rule_text, NVL(source, 'manual')
		FROM da_squad.agent_guidelines
		WHERE is_active = TRUE
		ORDER BY category, id`)
	if err != nil {
		return nil, fmt.Errorf("list guidelines: %w", err)
	}
	defer rows.Close()

	var out []Guideline
	for rows.Next() {
		var g Guideline
		if err := rows.Scan(&g.ID, &g.Category, &g.RuleText, &g.Source); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
