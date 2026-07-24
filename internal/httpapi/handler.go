package httpapi

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jay/da-agents/internal/pipeline"
	"github.com/jay/da-agents/internal/slack"
)

// Handler serves Slack interactivity endpoints using shared pipeline feedback logic.
type Handler struct {
	SigningSecret string
	Feedback      *pipeline.FeedbackDeps
}

func (h *Handler) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/slack/interactions", h.HandleInteractions)
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

	ctx := r.Context()
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
		case slack.ActionPositive:
			if err := h.Feedback.RecordPositive(ctx, userID, messageTS); err != nil {
				log.Printf("record positive: %v", err)
				http.Error(w, "internal", http.StatusInternalServerError)
				return
			}
			_ = h.Feedback.Slack.PostEphemeral(ctx, channelID, userID, "已記錄：這份報告標記為準確。")
			w.WriteHeader(http.StatusOK)
			return
		case slack.ActionCorrection:
			if err := h.Feedback.Slack.OpenCorrectionModal(ctx, payload.TriggerID, channelID, messageTS); err != nil {
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
		id, err := h.Feedback.IngestCorrection(ctx, rawText, payload.User.ID, meta["message_ts"])
		if err != nil {
			log.Printf("ingest correction: %v", err)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response_action": "errors",
				"errors": map[string]string{
					slack.BlockCorrection: "寫入準則失敗，請稍後再試",
				},
			})
			return
		}
		channelID := meta["channel_id"]
		if channelID != "" {
			_ = h.Feedback.Slack.PostEphemeral(ctx, channelID, payload.User.ID,
				"已將回饋提煉為準則 #"+strconv.Itoa(id)+"，明天分析會套用。")
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
	} `json:"actions"`
	View struct {
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
