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
	Summary       string    `json:"summary"`
	Insights      []string  `json:"insights"`
	Anomalies     []Anomaly `json:"anomalies"`
	FailedMetrics []string  `json:"failed_metrics"`
}

type Anomaly struct {
	Metric           string `json:"metric"`
	Detail           string `json:"detail"`
	InvestigationSQL string `json:"investigation_sql"`
}

type GuidelineDraft struct {
	Category string `json:"category"`
	RuleText string `json:"rule_text"`
}

// reportResponseSchema forces Gemini to return a single ReportOutput object
// (not an array). Uses the OpenAPI subset accepted by generateContent.
var reportResponseSchema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"summary":  map[string]any{"type": "STRING"},
		"insights": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
		"anomalies": map[string]any{
			"type": "ARRAY",
			"items": map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"metric":            map[string]any{"type": "STRING"},
					"detail":            map[string]any{"type": "STRING"},
					"investigation_sql": map[string]any{"type": "STRING"},
				},
				"required": []string{"metric", "detail"},
			},
		},
		"failed_metrics": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
	},
	"required": []string{"summary", "insights", "anomalies", "failed_metrics"},
}

// guidelineResponseSchema forces DistillGuideline output into a single object.
var guidelineResponseSchema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"category":  map[string]any{"type": "STRING", "enum": []string{"metric_logic", "formatting", "context", "investigation"}},
		"rule_text": map[string]any{"type": "STRING"},
	},
	"required": []string{"category", "rule_text"},
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

// GenerateJSON calls generateContent with JSON mime type. When responseSchema
// is non-nil it is sent so the model is constrained to that exact shape.
func (c *Client) GenerateJSON(ctx context.Context, system, user string, responseSchema any) (string, error) {
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		c.Model, c.APIKey,
	)
	genConfig := map[string]any{
		"temperature":      0.2,
		"responseMimeType": "application/json",
	}
	if responseSchema != nil {
		genConfig["responseSchema"] = responseSchema
	}
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
		"generationConfig": genConfig,
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
	text, err := c.GenerateJSON(ctx, system, user, reportResponseSchema)
	if err != nil {
		return nil, err
	}
	var out ReportOutput
	if err := json.Unmarshal([]byte(text), &out); err == nil {
		return &out, nil
	}
	// Tolerate the model wrapping the single report in an array [ {...} ].
	var arr []ReportOutput
	if err := json.Unmarshal([]byte(text), &arr); err == nil && len(arr) > 0 {
		return &arr[0], nil
	}
	return nil, fmt.Errorf("parse report json: unexpected structure; body=%s", truncate(text, 300))
}

func (c *Client) DistillGuideline(ctx context.Context, system, user string) (*GuidelineDraft, error) {
	text, err := c.GenerateJSON(ctx, system, user, guidelineResponseSchema)
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
