package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Gemini generateContent REST API.
type Client struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

type ReportOutput struct {
	Summary       string         `json:"summary"`
	Insights      []string       `json:"insights"`
	Anomalies     []Anomaly      `json:"anomalies"`
	FailedMetrics []string       `json:"failed_metrics"`
}

type Anomaly struct {
	Metric            string `json:"metric"`
	Detail            string `json:"detail"`
	InvestigationSQL  string `json:"investigation_sql"`
}

type GuidelineDraft struct {
	Category string `json:"category"`
	RuleText string `json:"rule_text"`
}

func New(apiKey, model string) *Client {
	if model == "" {
		model = "gemini-2.0-flash"
	}
	return &Client{
		APIKey: apiKey,
		Model:  model,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *Client) GenerateJSON(ctx context.Context, system, user string) (string, error) {
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		c.Model, c.APIKey,
	)
	body := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": system}},
		},
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": user}},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0.2,
			"responseMimeType": "application/json",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini status %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode gemini response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("gemini error: %s", parsed.Error.Message)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned empty candidates")
	}
	return strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text), nil
}

func (c *Client) GenerateReport(ctx context.Context, system, user string) (*ReportOutput, error) {
	text, err := c.GenerateJSON(ctx, system, user)
	if err != nil {
		return nil, err
	}
	var out ReportOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("parse report json: %w; body=%s", err, truncate(text, 300))
	}
	return &out, nil
}

func (c *Client) DistillGuideline(ctx context.Context, system, user string) (*GuidelineDraft, error) {
	text, err := c.GenerateJSON(ctx, system, user)
	if err != nil {
		return nil, err
	}
	var out GuidelineDraft
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("parse guideline json: %w; body=%s", err, truncate(text, 300))
	}
	out.Category = strings.TrimSpace(out.Category)
	out.RuleText = strings.TrimSpace(out.RuleText)
	if out.RuleText == "" {
		return nil, fmt.Errorf("empty rule_text from model")
	}
	switch out.Category {
	case "metric_logic", "formatting", "context", "investigation":
	default:
		out.Category = "context"
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
