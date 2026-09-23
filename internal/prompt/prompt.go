package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/jay/da-agents/internal/memory"
	"github.com/jay/da-agents/internal/worker"
)

// Standing rules live in systemInstruction (sent once per call but kept short).
// User message carries only: date map, compact catalog, guidelines, metric tables, JSON schema.
const systemRole = `你是資深數據分析 Agent，根據 guidelines 與指標快照產出每日洞察。
- 因產品具有高度的 weekly 週期性，分析單日表現時必須搭配 vs_7d_avg_pct，不能單靠DoD說明好壞
- 聚焦顯著指標，交叉分析各快照；supporting 解釋 primary，context 僅作背景。
- 只描述數據支持的事實與指標關聯，不編造數字、結果或原因。
- 使用繁中簡潔條列；時間優先使用昨日、前日、上週同日及 DoD/WoW/MoM。
- 所有數值必須與指標名稱一起使用 Markdown inline code 格式呈現，例如 metric:value。
- 禁止歸因系統、ETL、資料遺失或排程問題；必要時僅寫「需確認資料」。
- 只輸出合法 JSON，不含額外文字。`

// BuildReportPrompt assembles the one-shot report prompt.
func BuildReportPrompt(reportDate string, guidelines []memory.Guideline, metrics []worker.MetricResult) (system, user string) {
	system = systemRole
	labels := metricLabels(metrics)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("report_date=%s\n", reportDate))
	if rd, prev, wow, ok := relativeDates(reportDate); ok {
		b.WriteString(fmt.Sprintf("dates: 昨日=%s; 前日=%s; 上週同日=%s\n", rd, prev, wow))
	}
	b.WriteString("catalog:\n")
	for _, m := range metrics {
		role := m.Role
		if role == "" {
			role = "primary"
		}
		line := fmt.Sprintf("- %s [%s]", labels[m.Name], role)
		if len(m.Supports) > 0 {
			line += " → " + joinLabels(m.Supports, labels)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\nguidelines:\n")
	if len(guidelines) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, g := range guidelines {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", g.Category, g.RuleText))
		}
	}

	b.WriteString("\ndata:\n")
	for _, m := range metrics {
		title := labels[m.Name]
		role := m.Role
		if role == "" {
			role = "primary"
		}
		b.WriteString(fmt.Sprintf("### %s (%s)\n", title, role))
		if len(m.Supports) > 0 {
			b.WriteString("supports: ")
			b.WriteString(joinLabels(m.Supports, labels))
			b.WriteString("\n")
		}
		if m.Error != "" {
			b.WriteString("ERROR: ")
			b.WriteString(m.Error)
			b.WriteString("\n\n")
			continue
		}
		// Prefer table body already in Markdown; strip old ### filename header if present.
		b.WriteString(stripLeadingHeading(m.Markdown))
		b.WriteString("\n")
	}

	b.WriteString(`json:
{"summary":"…","insights":["…"],"anomalies":[{"metric":"業務指標名","detail":"…","investigation_sql":""}],"failed_metrics":[]}
只輸出單一 JSON 物件，不要包成陣列。
investigation_sql 必須為 ""。
`)
	user = b.String()
	return system, user
}

func metricLabels(metrics []worker.MetricResult) map[string]string {
	out := make(map[string]string, len(metrics))
	for _, m := range metrics {
		out[m.Name] = metricLabel(m)
	}
	return out
}

func metricLabel(m worker.MetricResult) string {
	if strings.TrimSpace(m.Description) != "" {
		return strings.TrimSpace(m.Description)
	}
	return strings.ReplaceAll(m.Name, "_", " ")
}

func joinLabels(names []string, labels map[string]string) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if lb, ok := labels[n]; ok {
			parts = append(parts, lb)
		} else {
			parts = append(parts, strings.ReplaceAll(n, "_", " "))
		}
	}
	return strings.Join(parts, "、")
}

func stripLeadingHeading(md string) string {
	lines := strings.Split(md, "\n")
	i := 0
	if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "###") {
		i++
	}
	// Drop legacy meta lines written by worker (description/role/supports); catalog already covers them.
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "- description:") || strings.HasPrefix(t, "- role:") || strings.HasPrefix(t, "- supports:") {
			i++
			continue
		}
		if t == "" {
			i++
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines[i:], "\n")) + "\n"
}

func relativeDates(reportDate string) (report, prev, wow string, ok bool) {
	t, err := time.Parse("2006-01-02", reportDate)
	if err != nil {
		return "", "", "", false
	}
	fmtDate := func(d time.Time) string {
		return fmt.Sprintf("%d/%d/%d", d.Year(), int(d.Month()), d.Day())
	}
	return fmtDate(t), fmtDate(t.AddDate(0, 0, -1)), fmtDate(t.AddDate(0, 0, -7)), true
}

// BuildDistillPrompt asks the model to turn Slack feedback into a guideline.
func BuildDistillPrompt(rawFeedback string) (system, user string) {
	system = `將 Slack 口語回饋轉成 Agent 準則。只輸出 JSON：{"category":"metric_logic|formatting|context|investigation","rule_text":"繁中祈使句"}`
	user = fmt.Sprintf("feedback:\n%s", rawFeedback)
	return system, user
}
