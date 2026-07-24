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
const systemRole = `資深數據分析 Agent。依 guidelines + 指標快照產出每日洞察 JSON。
規則：
1) 內文繁中；禁止編造數字；禁止輸出 SQL／程式碼。
2) 所有快照交叉參照成一篇敘事；supporting（人均／中位／分位等）用來解釋 primary，勿各說各話。
3) 交叉參照時預設直接寫結論與數字，不必點名指標；僅在必要時才簡短說明參考了哪個 metrics（用業務含義，勿提 .sql／檔名）。
4) 日期：報告日＝「昨日」；前一日＝「前日（yyyy/m/d）」；報告日-7＝「上週同日（yyyy/m/d）」；其他＝「相對語（yyyy/m/d）」。少寫裸日期。
5) 歸因只談產品／用戶／市場／營運行為。禁止歸因到系統故障、ETL、管線延遲、資料遺失、排程失敗等；此類僅能由人工判斷，模型最多寫「需人工進一步確認資料完整性」而不下系統結論。
6) 只輸出合法 JSON。`

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
