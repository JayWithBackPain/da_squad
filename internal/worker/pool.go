package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jay/da-agents/internal/redshift"
	"github.com/jay/da-agents/internal/sqlloader"
)

// MetricResult is the output of one DA worker SQL job.
type MetricResult struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Role        string              `json:"role,omitempty"`     // primary | supporting | context
	Supports    []string            `json:"supports,omitempty"` // primary metric names this supports
	Markdown    string              `json:"markdown"`
	Columns     []string            `json:"columns,omitempty"`
	Rows        []map[string]string `json:"rows,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// Pool runs a fixed number of workers against pending SQL queries.
type Pool struct {
	Client  *redshift.Client
	Workers int
	MaxRows int
}

// Run digests all queries with N workers. Individual failures are recorded on MetricResult.Error.
func (p *Pool) Run(ctx context.Context, queries []sqlloader.Query) []MetricResult {
	n := p.Workers
	if n <= 0 {
		n = 4
	}
	jobs := make(chan sqlloader.Query)
	results := make(chan MetricResult, len(queries))

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range jobs {
				select {
				case <-ctx.Done():
					results <- MetricResult{
						Name: q.Name, Description: q.Description, Role: q.Role, Supports: q.Supports,
						Error: ctx.Err().Error(),
					}
				default:
					results <- p.runOne(ctx, q)
				}
			}
		}()
	}

	for _, q := range queries {
		jobs <- q
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]MetricResult, 0, len(queries))
	for r := range results {
		out = append(out, r)
	}
	return out
}

func (p *Pool) runOne(ctx context.Context, q sqlloader.Query) MetricResult {
	base := MetricResult{
		Name:        q.Name,
		Description: q.Description,
		Role:        q.Role,
		Supports:    append([]string(nil), q.Supports...),
	}
	cols, rows, err := p.Client.QueryRows(ctx, q.SQL, p.MaxRows)
	if err != nil {
		base.Error = fmt.Sprintf("%v", err)
		return base
	}
	base.Columns = cols
	base.Rows = rows
	base.Markdown = formatMetricMarkdown(q, cols, rows)
	return base
}

func formatMetricMarkdown(q sqlloader.Query, cols []string, rows []map[string]string) string {
	var b strings.Builder
	b.WriteString("### ")
	b.WriteString(q.Name)
	b.WriteString("\n")
	if q.Description != "" {
		b.WriteString("- description: ")
		b.WriteString(q.Description)
		b.WriteString("\n")
	}
	role := q.Role
	if role == "" {
		role = "primary"
	}
	b.WriteString("- role: ")
	b.WriteString(role)
	b.WriteString("\n")
	if len(q.Supports) > 0 {
		b.WriteString("- supports: ")
		b.WriteString(strings.Join(q.Supports, ", "))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(redshift.TableMarkdown(cols, rows))
	return b.String()
}
