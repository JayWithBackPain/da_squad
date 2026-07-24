package report

import (
	"context"
	"fmt"
	"time"

	"github.com/jay/da-agents/internal/llm"
	"github.com/jay/da-agents/internal/memory"
	"github.com/jay/da-agents/internal/prompt"
	"github.com/jay/da-agents/internal/worker"
)

// Agent produces a single consolidated analysis from all MetricResults.
type Agent struct {
	LLM    *llm.Client
	Memory *memory.Store
}

type Output struct {
	ReportDate string
	Report     *llm.ReportOutput
	Metrics    []worker.MetricResult
}

func (a *Agent) Run(ctx context.Context, reportDate string, metrics []worker.MetricResult) (*Output, error) {
	if reportDate == "" {
		reportDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	guidelines, err := a.Memory.ListActiveGuidelines(ctx)
	if err != nil {
		return nil, fmt.Errorf("load guidelines: %w", err)
	}
	system, user := prompt.BuildReportPrompt(reportDate, guidelines, metrics)
	rep, err := a.LLM.GenerateReport(ctx, system, user)
	if err != nil {
		return nil, fmt.Errorf("generate report: %w", err)
	}
	// Ensure failed metrics from workers are reflected even if model omits them.
	var failed []string
	seen := map[string]bool{}
	for _, f := range rep.FailedMetrics {
		seen[f] = true
		failed = append(failed, f)
	}
	for _, m := range metrics {
		if m.Error != "" && !seen[m.Name] {
			failed = append(failed, m.Name)
		}
	}
	rep.FailedMetrics = failed
	return &Output{
		ReportDate: reportDate,
		Report:     rep,
		Metrics:    metrics,
	}, nil
}
