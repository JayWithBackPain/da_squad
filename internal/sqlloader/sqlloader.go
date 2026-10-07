package sqlloader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Query holds a named SQL file loaded from disk.
type Query struct {
	Name        string // filename without .sql
	Path        string
	SQL         string   // executable SQL (metadata comment lines preserved; harmless to Redshift)
	Description string   // -- @desc:
	Role        string   // -- @role: primary | supporting | context
	Supports    []string // -- @supports: metric_a, metric_b
}

// LoadDir loads all *.sql files from dir (non-recursive).
func LoadDir(dir string) ([]Query, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read query dir %s: %w", dir, err)
	}
	var out []Query
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		raw := strings.TrimSpace(string(b))
		if raw == "" {
			continue
		}
		meta, sqlBody := ParseMeta(raw)
		if strings.TrimSpace(sqlBody) == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(meta.Role))
		if role == "" {
			role = "primary"
		}
		out = append(out, Query{
			Name:        strings.TrimSuffix(name, filepath.Ext(name)),
			Path:        path,
			SQL:         sqlBody,
			Description: meta.Description,
			Role:        role,
			Supports:    meta.Supports,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) == 0 {
		return nil, fmt.Errorf("no .sql files found in %s", dir)
	}
	return out, nil
}

// Meta is optional header metadata from SQL comment directives.
type Meta struct {
	Description string
	Role        string
	Supports    []string
}

// ParseMeta reads leading -- @key: value lines and returns remaining SQL.
func ParseMeta(raw string) (Meta, string) {
	var meta Meta
	lines := strings.Split(raw, "\n")
	i := 0
	for ; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			break
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "--"))
		if !strings.HasPrefix(body, "@") {
			// Ordinary SQL comment before the query; keep with SQL body.
			break
		}
		key, val, ok := strings.Cut(body, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "@desc", "@description":
			meta.Description = val
		case "@role":
			meta.Role = val
		case "@supports":
			meta.Supports = splitCSV(val)
		}
	}
	return meta, strings.TrimSpace(strings.Join(lines[i:], "\n"))
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
