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

type Feedback struct {
	ID                 int
	RawText            string
	FeedbackType       string // positive | correction
	SlackUserID        string
	SlackMessageTS     string
	DerivedGuidelineID sql.NullInt64
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

func (s *Store) InsertGuideline(ctx context.Context, category, ruleText, source string) (int, error) {
	// Avoid INSERT...RETURNING for broader Redshift compatibility; resolve id after insert.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO da_squad.agent_guidelines (category, rule_text, is_active, source)
		VALUES ($1, $2, TRUE, $3)`, category, ruleText, source)
	if err != nil {
		return 0, fmt.Errorf("insert guideline: %w", err)
	}
	var id int
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM da_squad.agent_guidelines
		WHERE category = $1 AND rule_text = $2 AND source = $3
		ORDER BY id DESC
		LIMIT 1`, category, ruleText, source).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("resolve guideline id: %w", err)
	}
	return id, nil
}

func (s *Store) InsertFeedback(ctx context.Context, feedbackType, rawText, slackUserID, slackMessageTS string, guidelineID *int) (int, error) {
	var derived any
	if guidelineID != nil {
		derived = *guidelineID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO da_squad.agent_feedback (raw_text, feedback_type, slack_user_id, slack_message_ts, derived_guideline_id)
		VALUES ($1, $2, $3, $4, $5)`, rawText, feedbackType, slackUserID, slackMessageTS, derived)
	if err != nil {
		return 0, fmt.Errorf("insert feedback: %w", err)
	}
	var id int
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM da_squad.agent_feedback
		WHERE feedback_type = $1
		  AND NVL(slack_user_id, '') = NVL($2, '')
		  AND NVL(slack_message_ts, '') = NVL($3, '')
		ORDER BY id DESC
		LIMIT 1`, feedbackType, slackUserID, slackMessageTS).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("resolve feedback id: %w", err)
	}
	return id, nil
}
