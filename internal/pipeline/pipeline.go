package pipeline

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/jay/da-agents/internal/config"
	"github.com/jay/da-agents/internal/llm"
	"github.com/jay/da-agents/internal/memory"
	"github.com/jay/da-agents/internal/prompt"
	"github.com/jay/da-agents/internal/redshift"
	"github.com/jay/da-agents/internal/report"
	appruntime "github.com/jay/da-agents/internal/runtime"
	"github.com/jay/da-agents/internal/slack"
	"github.com/jay/da-agents/internal/sqlloader"
	"github.com/jay/da-agents/internal/worker"
)

// AnalyzeDeps holds shared clients for the daily analyze pipeline.
type AnalyzeDeps struct {
	Cfg      *config.Config
	Redshift *redshift.Client
	Memory   *memory.Store
	LLM      *llm.Client
	Slack    *slack.Client
}

// OpenAnalyze opens DB/LLM/Slack clients from config.
func OpenAnalyze(cfg *config.Config) (*AnalyzeDeps, error) {
	rs, err := redshift.Open(cfg.Database.Redshift.Driver, cfg.Database.Redshift.ConnStr, cfg.Analyze.Workers)
	if err != nil {
		return nil, err
	}
	// Guidelines / feedback live on the same Redshift as metric queries.
	mem, err := memory.Open(cfg.Database.Redshift.Driver, cfg.Database.Redshift.ConnStr)
	if err != nil {
		_ = rs.Close()
		return nil, err
	}
	return &AnalyzeDeps{
		Cfg:      cfg,
		Redshift: rs,
		Memory:   mem,
		LLM:      llm.New(cfg.Gemini.APIKey, cfg.Gemini.Model),
		Slack:    slack.New(cfg.Slack.BotToken, cfg.Slack.ChannelID),
	}, nil
}

func (d *AnalyzeDeps) Close() {
	if d == nil {
		return
	}
	_ = d.Redshift.Close()
	_ = d.Memory.Close()
}

// Run executes workers → report agent → Slack post.
func (d *AnalyzeDeps) Run(ctx context.Context) error {
	log.Printf("analyze start env=%s", appruntime.EnvName())

	queryDir := d.Cfg.Analyze.QueryDir
	if !filepath.IsAbs(queryDir) {
		resolved, err := appruntime.Resolve(splitPath(queryDir)...)
		if err != nil {
			return err
		}
		queryDir = resolved
	}

	queries, err := sqlloader.LoadDir(queryDir)
	if err != nil {
		return err
	}
	log.Printf("loaded %d sql files from %s workers=%d", len(queries), queryDir, d.Cfg.Analyze.Workers)

	pool := &worker.Pool{
		Client:  d.Redshift,
		Workers: d.Cfg.Analyze.Workers,
		MaxRows: d.Cfg.Analyze.MaxRowsPerQuery,
	}
	metrics := pool.Run(ctx, queries)
	log.Printf("worker pool finished metrics=%d", len(metrics))

	reportDate := d.Cfg.Analyze.ReportDate
	if reportDate == "" {
		reportDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	}
	agent := &report.Agent{LLM: d.LLM, Memory: d.Memory}
	out, err := agent.Run(ctx, reportDate, metrics)
	if err != nil {
		return err
	}

	post, err := d.Slack.PostReport(ctx, out.ReportDate, out.Report)
	if err != nil {
		return fmt.Errorf("slack post: %w", err)
	}
	log.Printf("slack posted channel=%s ts=%s summary_len=%d", post.Channel, post.TS, len(out.Report.Summary))
	return nil
}

func splitPath(p string) []string {
	clean := filepath.Clean(p)
	if clean == "." || clean == "" {
		return nil
	}
	var parts []string
	for clean != "." && clean != string(filepath.Separator) && clean != "" {
		base := filepath.Base(clean)
		parts = append([]string{base}, parts...)
		parent := filepath.Dir(clean)
		if parent == clean {
			break
		}
		clean = parent
	}
	return parts
}

// FeedbackDeps is used by Slack interactivity handlers.
type FeedbackDeps struct {
	Cfg    *config.Config
	Memory *memory.Store
	LLM    *llm.Client
	Slack  *slack.Client
}

func OpenFeedback(cfg *config.Config) (*FeedbackDeps, error) {
	mem, err := memory.Open(cfg.Database.Redshift.Driver, cfg.Database.Redshift.ConnStr)
	if err != nil {
		return nil, err
	}
	return &FeedbackDeps{
		Cfg:    cfg,
		Memory: mem,
		LLM:    llm.New(cfg.Gemini.APIKey, cfg.Gemini.Model),
		Slack:  slack.New(cfg.Slack.BotToken, cfg.Slack.ChannelID),
	}, nil
}

func (d *FeedbackDeps) Close() {
	if d == nil {
		return
	}
	_ = d.Memory.Close()
}

func (d *FeedbackDeps) RecordPositive(ctx context.Context, userID, messageTS string) error {
	_, err := d.Memory.InsertFeedback(ctx, "positive", "", userID, messageTS, nil)
	return err
}

// IngestCorrection distills Slack text into a guideline and stores both rows.
func (d *FeedbackDeps) IngestCorrection(ctx context.Context, rawText, userID, messageTS string) (guidelineID int, err error) {
	system, user := prompt.BuildDistillPrompt(rawText)
	draft, err := d.LLM.DistillGuideline(ctx, system, user)
	if err != nil {
		return 0, fmt.Errorf("distill: %w", err)
	}
	id, err := d.Memory.InsertGuideline(ctx, draft.Category, draft.RuleText, "slack_feedback")
	if err != nil {
		return 0, err
	}
	if _, err := d.Memory.InsertFeedback(ctx, "correction", rawText, userID, messageTS, &id); err != nil {
		return id, err
	}
	return id, nil
}
