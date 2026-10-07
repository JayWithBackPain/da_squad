package report

import (
	"context"
	"fmt"

	"github.com/jay/da-agents/internal/contextcompiler"
	"github.com/jay/da-agents/internal/knowledge"
	"github.com/jay/da-agents/internal/llm"
	"github.com/jay/da-agents/internal/memory"
	"github.com/jay/da-agents/internal/worker"
)

type Generator interface {
	CountReportTokens(context.Context, string, string) (int, error)
	GenerateReport(context.Context, string, string) (*llm.ReportOutput, error)
}
type Store interface {
	ListActiveGuidelines(context.Context) ([]memory.Guideline, error)
	SavePayload(context.Context, string, string, any) error
	SetRunStatus(context.Context, string, string, int, string) error
}
type Agent struct {
	LLM            Generator
	Memory         Store
	Knowledge      *knowledge.Catalog
	Limits         contextcompiler.Limits
	MaxInputTokens int
}
type Output struct {
	RunID, ReportDate string
	Report            *llm.ReportOutput
	Metrics           []worker.MetricResult
}

func (a *Agent) Run(ctx context.Context, runID, date string, metrics []worker.MetricResult) (*Output, error) {
	guidelines, err := a.Memory.ListActiveGuidelines(ctx)
	if err != nil {
		return nil, fmt.Errorf("load guidelines: %w", err)
	}
	compiled, err := contextcompiler.Build(date, guidelines, metrics, a.Knowledge, a.Limits)
	if err != nil {
		return nil, err
	}
	compiled.GenerationConfig = llm.ReportGenerationConfig()
	// Persist compiled input before counting or generation; failed API calls are
	// still replayable and no mandatory evidence is silently truncated.
	if err = a.Memory.SavePayload(ctx, runID, "context", compiled); err != nil {
		return nil, err
	}
	tokens, err := a.LLM.CountReportTokens(ctx, compiled.System, compiled.User)
	if err != nil {
		return nil, fmt.Errorf("count input tokens: %w", err)
	}
	compiled.InputTokens = tokens
	if err = a.Memory.SavePayload(ctx, runID, "context", compiled); err != nil {
		return nil, err
	}
	if err = a.Memory.SetRunStatus(ctx, runID, "context_ready", tokens, ""); err != nil {
		return nil, err
	}
	if a.MaxInputTokens <= 0 || tokens > a.MaxInputTokens {
		return nil, fmt.Errorf("input token budget exceeded: %d > %d", tokens, a.MaxInputTokens)
	}
	rep, err := a.LLM.GenerateReport(ctx, compiled.System, compiled.User)
	if err != nil {
		return nil, fmt.Errorf("generate report: %w", err)
	}
	if rep == nil {
		return nil, fmt.Errorf("empty report")
	}
	seen := map[string]bool{}
	failed := []string{}
	for _, f := range rep.FailedMetrics {
		if !seen[f] {
			seen[f] = true
			failed = append(failed, f)
		}
	}
	for _, m := range metrics {
		if m.Error != "" && !seen[m.Name] {
			seen[m.Name] = true
			failed = append(failed, m.Name)
		}
	}
	rep.FailedMetrics = failed
	if err = a.Memory.SavePayload(ctx, runID, "report", rep); err != nil {
		return nil, err
	}
	validationErr := Validate(rep, metrics)
	validation := map[string]any{"passed": validationErr == nil, "checks": []string{"required fields", "investigation plans", "exact source evidence references"}}
	if validationErr != nil {
		validation["error"] = validationErr.Error()
	}
	if err = a.Memory.SavePayload(ctx, runID, "validation", validation); err != nil {
		return nil, err
	}
	if validationErr != nil {
		return nil, fmt.Errorf("report validation: %w", validationErr)
	}
	if err = a.Memory.SetRunStatus(ctx, runID, "generated", tokens, ""); err != nil {
		return nil, err
	}
	return &Output{RunID: runID, ReportDate: date, Report: rep, Metrics: metrics}, nil
}
