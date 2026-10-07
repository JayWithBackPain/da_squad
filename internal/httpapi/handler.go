package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/jay/da-agents/internal/pipeline"
	"github.com/jay/da-agents/internal/slack"
)

// Handler serves Slack interactivity endpoints using shared pipeline feedback logic.
type Handler struct {
	SigningSecret string
	Feedback      *pipeline.FeedbackDeps
	// EnqueueCorrection hands a correction off for background processing so the
	// modal can be acked within Slack's 3s window. If nil, the correction is
	// processed in a local goroutine instead.
	EnqueueCorrection func(ctx context.Context, job pipeline.CorrectionJob) error
	EnqueueReplay     func(ctx context.Context, job pipeline.ReplayJob) error
}

func (h *Handler) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/slack/interactions", h.HandleInteractions)
	mux.HandleFunc("/slack/events", h.HandleEvents)
	return mux
}

func (h *Handler) HandleInteractions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := slack.VerifySignature(h.SigningSecret, r.Header, body); err != nil {
		log.Printf("slack signature: %v", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	payloadStr := vals.Get("payload")
	if payloadStr == "" {
		// Some setups send raw JSON
		payloadStr = string(body)
	}

	var payload interactionPayload
	if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	ctx, requestCancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
	defer requestCancel()
	switch payload.Type {
	case "block_actions":
		if len(payload.Actions) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		action := payload.Actions[0].ActionID
		userID := payload.User.ID
		channelID := payload.Channel.ID
		messageTS := payload.Message.TS
		switch action {
		case slack.ActionReplayNext:
			job := pipeline.ReplayJob{RunID: payload.Actions[0].Value, Mode: "next", Reviewer: userID, ChannelID: channelID}
			if h.EnqueueReplay == nil {
				http.Error(w, "replay is not configured", http.StatusServiceUnavailable)
				return
			}
			if err := h.EnqueueReplay(ctx, job); err != nil {
				log.Printf("enqueue replay: %v", err)
				http.Error(w, "retry replay action", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		case slack.ActionPositive:
			if err := recordPositive(h.Feedback, ctx, pipeline.CorrectionJob{RunID: payload.Actions[0].Value, EventID: payload.Actions[0].ActionTS, UserID: userID, MessageTS: messageTS, ChannelID: channelID}); err != nil {
				log.Printf("record positive: %v", err)
				http.Error(w, "internal", http.StatusInternalServerError)
				return
			}
			notifyCtx, notifyCancel := context.WithTimeout(ctx, 400*time.Millisecond)
			_ = h.Feedback.Slack.PostEphemeral(notifyCtx, channelID, userID, "已記錄：這份報告標記為準確。")
			notifyCancel()
			w.WriteHeader(http.StatusOK)
			return
		case slack.ActionCorrection:
			if err := h.Feedback.Slack.OpenCorrectionModal(ctx, payload.TriggerID, channelID, messageTS, payload.Actions[0].Value); err != nil {
				log.Printf("open modal: %v", err)
				http.Error(w, "internal", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		default:
			w.WriteHeader(http.StatusOK)
			return
		}
	case "view_submission":
		if payload.View.CallbackID != slack.CallbackCorrect {
			w.WriteHeader(http.StatusOK)
			return
		}
		rawText := extractCorrectionText(payload.View.State)
		meta := map[string]string{}
		_ = json.Unmarshal([]byte(payload.View.PrivateMetadata), &meta)
		job := pipeline.CorrectionJob{
			RawText:   rawText,
			RunID:     meta["run_id"],
			EventID:   payload.View.ID,
			UserID:    payload.User.ID,
			MessageTS: meta["message_ts"],
			ChannelID: meta["channel_id"],
		}

		// Do not acknowledge a modal until its original text is durable.
		// Redshift lock/query latency may exceed Slack's deadline: surface a
		// retryable modal error, never silently accept an unsaved correction.
		saveCtx, cancel := context.WithTimeout(ctx, 1800*time.Millisecond)
		feedbackID, saveErr := h.Feedback.SaveFeedback(saveCtx, "correction", job)
		cancel()
		if saveErr != nil {
			log.Printf("save correction: %v", saveErr)
			modalError(w, "回饋尚未確認保存，請稍後重試；輸入內容會保留")
			return
		}
		job.FeedbackID = feedbackID
		job.RawText = "" // The durable record is the sole source used by distillation.
		// Distillation (Gemini + Redshift writes) is too slow for Slack's 3s modal
		// deadline, so we hand it off and ack immediately. The user is told the
		// result via an ephemeral message once the background job finishes.
		if h.EnqueueCorrection != nil {
			enqueueCtx, enqueueCancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := h.EnqueueCorrection(enqueueCtx, job)
			enqueueCancel()
			if err != nil {
				log.Printf("enqueue correction feedback_id=%s remains pending: %v", feedbackID, err)

				// Original feedback is already durable. Leave pending for the
				// retry CLI instead of asking PO to submit another correction.

			}
		} else {
			// Local / fallback: no async transport, run in a goroutine with a
			// background context so it survives after this request returns.
			go func() {
				processCtx, processCancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer processCancel()
				_ = h.Feedback.ProcessCorrection(processCtx, job)
			}()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response_action":"clear"}`))
		return
	default:
		w.WriteHeader(http.StatusOK)
	}
}

type interactionPayload struct {
	Type      string `json:"type"`
	TriggerID string `json:"trigger_id"`
	User      struct {
		ID string `json:"id"`
	} `json:"user"`
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
	Message struct {
		TS string `json:"ts"`
	} `json:"message"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
		ActionTS string `json:"action_ts"`
	} `json:"actions"`
	View struct {
		ID              string          `json:"id"`
		CallbackID      string          `json:"callback_id"`
		PrivateMetadata string          `json:"private_metadata"`
		State           json.RawMessage `json:"state"`
	} `json:"view"`
}

func extractCorrectionText(state json.RawMessage) string {
	var s struct {
		Values map[string]map[string]struct {
			Value string `json:"value"`
		} `json:"values"`
	}
	if err := json.Unmarshal(state, &s); err != nil {
		return ""
	}
	if block, ok := s.Values[slack.BlockCorrection]; ok {
		if el, ok := block[slack.ActionCorrectionInput]; ok {
			return el.Value
		}
	}
	return ""
}

func modalError(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"response_action": "errors", "errors": map[string]string{slack.BlockCorrection: text}})
}
func recordPositive(d *pipeline.FeedbackDeps, ctx context.Context, job pipeline.CorrectionJob) error {
	_, err := d.SaveFeedback(ctx, "positive", job)
	return err
}
