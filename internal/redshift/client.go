package redshift

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Client wraps a Redshift (postgres-compatible) connection pool.
type Client struct {
	db *sql.DB
}

func Open(driver, connStr string, maxOpen int) (*Client, error) {
	if driver == "" {
		driver = "postgres"
	}
	db, err := sql.Open(driver, connStr)
	if err != nil {
		return nil, fmt.Errorf("open redshift: %w", err)
	}
	if maxOpen <= 0 {
		maxOpen = 4
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping redshift: %w", err)
	}
	return &Client{db: db}, nil
}

func (c *Client) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// QueryRows executes SQL and returns column names + row maps (stringified values).
func (c *Client) QueryRows(ctx context.Context, query string, maxRows int) (columns []string, rows []map[string]string, err error) {
	if maxRows <= 0 {
		maxRows = 200
	}
	r, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("query: %w", err)
	}
	defer r.Close()

	cols, err := r.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("columns: %w", err)
	}
	columns = cols

	for r.Next() {
		if len(rows) >= maxRows {
			break
		}
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := r.Scan(ptrs...); err != nil {
			return columns, rows, fmt.Errorf("scan: %w", err)
		}
		m := make(map[string]string, len(cols))
		for i, col := range cols {
			m[col] = stringify(raw[i])
		}
		rows = append(rows, m)
	}
	if err := r.Err(); err != nil {
		return columns, rows, err
	}
	return columns, rows, nil
}

func stringify(v any) string {
	if v == nil {
		return "NULL"
	}
	switch t := v.(type) {
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339)
	default:
		return fmt.Sprint(t)
	}
}

// ToMarkdown renders a compact markdown section for LLM context.
func ToMarkdown(name string, columns []string, rows []map[string]string) string {
	var b strings.Builder
	if name != "" {
		b.WriteString("### ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString(TableMarkdown(columns, rows))
	return b.String()
}

// TableMarkdown renders only the table body (no heading).
func TableMarkdown(columns []string, rows []map[string]string) string {
	var b strings.Builder
	if len(columns) == 0 {
		b.WriteString("(no columns)\n")
		return b.String()
	}
	b.WriteString("| ")
	b.WriteString(strings.Join(columns, " | "))
	b.WriteString(" |\n| ")
	for i := range columns {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("---")
	}
	b.WriteString(" |\n")
	for _, row := range rows {
		b.WriteString("| ")
		for i, col := range columns {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(escapeCell(row[col]))
		}
		b.WriteString(" |\n")
	}
	b.WriteString(fmt.Sprintf("\n_rows=%d_\n", len(rows)))
	return b.String()
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
