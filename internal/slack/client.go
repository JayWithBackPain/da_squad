package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jay/da-agents/internal/llm"
)

const (
	ActionPositive   = "feedback_positive"
	ActionCorrection = "feedback_correction"
	CallbackCorrect  = "correction_modal"
	BlockCorrection  = "correction_input_block"
	ActionCorrectionInput = "correction_text"
)

// Client posts messages and opens modals via Slack Web API.
type Client struct {
	BotToken   string
	ChannelID  string
	HTTPClient *http.Client
}

func New(botToken, channelID string) *Client {
	return &Client{
		BotToken:  botToken,
		ChannelID: channelID,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type PostResult struct {
	Channel string
	TS      string
}

func (c *Client) PostReport(ctx context.Context, reportDate string, rep *llm.ReportOutput) (*PostResult, error) {
	blocks := BuildReportBlocks(reportDate, rep)
	payload := map[string]any{
		"channel": c.ChannelID,
		"text":    fmt.Sprintf("Daily data report %s", reportDate),
		"blocks":  blocks,
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		TS    string `json:"ts"`
		Channel string `json:"channel"`
	}
	if err := c.api(ctx, "chat.postMessage", payload, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("chat.postMessage: %s", resp.Error)
	}
	return &PostResult{Channel: resp.Channel, TS: resp.TS}, nil
}

func (c *Client) OpenCorrectionModal(ctx context.Context, triggerID, channelID, messageTS string) error {
	view := map[string]any{
		"type": "modal",
		"callback_id": CallbackCorrect,
		"private_metadata": mustJSON(map[string]string{
			"channel_id": channelID,
			"message_ts": messageTS,
		}),
		"title": map[string]string{"type": "plain_text", "text": "糾正 / 補充規則"},
		"submit": map[string]string{"type": "plain_text", "text": "送出"},
		"close":  map[string]string{"type": "plain_text", "text": "取消"},
		"blocks": []map[string]any{
			{
				"type":     "input",
				"block_id": BlockCorrection,
				"label":    map[string]string{"type": "plain_text", "text": "請描述要記住的規則"},
				"element": map[string]any{
					"type":      "plain_text_input",
					"action_id": ActionCorrectionInput,
					"multiline": true,
					"placeholder": map[string]string{
						"type": "plain_text",
						"text": "例如：DAU 不要跟 MAU 混著講",
					},
				},
			},
		},
	}
	payload := map[string]any{
		"trigger_id": triggerID,
		"view":       view,
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := c.api(ctx, "views.open", payload, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("views.open: %s", resp.Error)
	}
	return nil
}

func (c *Client) PostEphemeral(ctx context.Context, channelID, userID, text string) error {
	payload := map[string]any{
		"channel": channelID,
		"user":    userID,
		"text":    text,
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := c.api(ctx, "chat.postEphemeral", payload, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("chat.postEphemeral: %s", resp.Error)
	}
	return nil
}

func (c *Client) api(ctx context.Context, method string, payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.BotToken)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack %s http %d: %s", method, resp.StatusCode, string(raw))
	}
	return json.Unmarshal(raw, out)
}

// BuildReportBlocks builds Block Kit for the daily report.
func BuildReportBlocks(reportDate string, rep *llm.ReportOutput) []map[string]any {
	var insights strings.Builder
	for _, i := range rep.Insights {
		insights.WriteString("• ")
		insights.WriteString(i)
		insights.WriteString("\n")
	}
	if insights.Len() == 0 {
		insights.WriteString("_(no insights)_")
	}

	var anomalies strings.Builder
	if len(rep.Anomalies) == 0 {
		anomalies.WriteString("_(none)_")
	} else {
		for _, a := range rep.Anomalies {
			anomalies.WriteString(fmt.Sprintf("• *%s*: %s\n", a.Metric, a.Detail))
			// Do not render investigation_sql; next steps stay in detail text only.
		}
	}

	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]string{"type": "plain_text", "text": fmt.Sprintf("Daily Data Report — %s", reportDate)},
		},
		{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": "*Summary*\n" + rep.Summary},
		},
		{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": "*Insights*\n" + insights.String()},
		},
		{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": "*Anomalies / next steps*\n" + anomalies.String()},
		},
	}
	if len(rep.FailedMetrics) > 0 {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": "*Failed metrics*\n• " + strings.Join(rep.FailedMetrics, "\n• "),
			},
		})
	}
	blocks = append(blocks,
		map[string]any{"type": "divider"},
		map[string]any{
			"type": "actions",
			"elements": []map[string]any{
				{
					"type": "button",
					"text": map[string]string{"type": "plain_text", "text": "準確"},
					"style": "primary",
					"action_id": ActionPositive,
				},
				{
					"type": "button",
					"text": map[string]string{"type": "plain_text", "text": "糾正 / 補充規則"},
					"style": "danger",
					"action_id": ActionCorrection,
				},
			},
		},
	)
	return blocks
}

// VerifySignature validates Slack request signing.
func VerifySignature(signingSecret string, header http.Header, body []byte) error {
	ts := header.Get("X-Slack-Request-Timestamp")
	sig := header.Get("X-Slack-Signature")
	if ts == "" || sig == "" {
		return fmt.Errorf("missing slack signature headers")
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("bad timestamp")
	}
	if abs(time.Now().Unix()-unix) > 60*5 {
		return fmt.Errorf("timestamp too old")
	}
	base := fmt.Sprintf("v0:%s:%s", ts, string(body))
	mac := hmac.New(sha256.New, []byte(signingSecret))
	_, _ = mac.Write([]byte(base))
	expect := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expect), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
