# DA Agents

以 PO feedback 持續改善的 Data Analyst Agent。沿用 Redshift → Lambda → Gemini → Slack，不增加向量資料庫或訓練平台。

日常流程：**查數 → 保存快照 → 挑選相關知識 → 檢查 Prompt 預算 → 生成及驗證 → Slack → 保存 feedback → PO 審查 → 更新版本**。

## 從這裡開始

- [工作流程](docs/WORKFLOW.md)：每日分析、feedback 審查、短版知識編輯與重試。
- [開發規範](docs/DEVELOPMENT.md)：SQL 契約、資料模型、Prompt、驗證與測試。
- [部署](docs/DEPLOY.md)：migration、設定、Lambda／Slack 啟用與回滾。
- [下一階段](docs/ROADMAP.md)：待開發的評測與模型訓練邊界。

Go 1.26.4；GoLand 開啟此專案原目錄。先依部署文件安裝資料表與 config，再執行：

```bash
make check PRODUCT=goodnight
make analyze PRODUCT=goodnight
make server PRODUCT=goodnight
make feedback-list PRODUCT=goodnight
```

`check` 不連線 DB／Gemini／Slack；`analyze` 會查 DB、呼叫 Gemini 並發 Slack。`feedback-list` 只讀 DB。若 go 不在 PATH，可指定 `GO=/Users/jay/sdk/go1.26.4/bin/go`。


## 正式環境：日常使用與知識改善

角色分工：PO 在 Slack 閱讀報告、補充事實與確認業務口徑；DA／維護者整理 catalog、記錄審查與部署；Agent 執行查數、保存與驗證；Gemini 生成報告並提煉候選知識。PO 與模型的互動由 Agent 轉送，不需要直接呼叫模型 API。

```mermaid
sequenceDiagram
    actor PO as PO／業務負責人
    actor DA as DA／維護者
    participant Agent as DA Agent／執行系統
    participant Model as AI Model／Gemini
    participant Slack as Slack
    participant DB as Redshift

    Note over Agent,DB: EventBridge 每日觸發分析，不等待前一日 PO 審查
    Agent->>DB: 查詢當日 metrics 與 active guidelines
    DB-->>Agent: 查詢結果與原則
    Note over Agent: 載入部署包內 approved catalog，組合受預算限制的 context
    Agent->>DB: 保存 input 與 context 快照
    Agent->>Model: 計算 input tokens
    Model-->>Agent: Token 數量
    Agent->>Model: 預算通過後，請求分析與 investigation plan
    Model-->>Agent: 結構化報告與 evidence
    Agent->>DB: 保存報告與驗證結果
    alt 驗證通過
        Agent->>Slack: 發送報告與 feedback 按鈕
        Slack-->>PO: 閱讀報告
        alt 報告準確
            PO->>Slack: 按「準確」
            Slack->>Agent: 傳送 positive feedback
            Agent->>DB: 保存評價並關聯 run
        else 需要糾正或補充
            PO->>Slack: 糾正表單或報告 thread 文字回覆
            Slack->>Agent: 傳送 correction feedback
            Agent->>DB: 先保存原始文字
            Agent->>Model: 背景提煉短版候選知識
            Model-->>Agent: Candidate，不代表已核准
            Agent->>DB: 保存 candidate
            DA->>Agent: 使用 feedback CLI 查看候選
            Agent-->>DA: 原文與候選
            DA->>PO: 確認定義、適用範圍與商業事實
            PO-->>DA: 核准或拒絕納入知識
            alt 核准納入
                Note over DA: 修改 catalog.yaml、增加版本、保留 feedback source、執行檢查
                DA->>Agent: 使用 feedback CLI 記錄 approved review
                Agent->>DB: 保存 review，feedback 標記 incorporated
                DA->>Agent: 部署包含新版 catalog 的 Lambda
                Note over Agent: 下一次日常分析使用新版部署知識
            else 拒絕納入
                DA->>Agent: 使用 feedback CLI 記錄 rejected 與理由
                Agent->>DB: 保留原文及拒絕紀錄
            end
        end
    else 驗證失敗
        Agent->>DB: 保存 failed 狀態，不發正常日報
        DA->>Agent: 檢查快照、修正問題後明確重試
    end
```

日常分析目前讀取**部署包內的 YAML catalog**；`knowledge-publish` 的 Redshift 發布版供 replay 使用，不會直接替換正式日報的 catalog。候選不會自動修改 YAML 或變成 active guideline；PO 若同時負責維護，也可以兼任圖中的 DA 角色。AI 呼叫或提煉失敗時保留可用紀錄，依 [工作流程](docs/WORKFLOW.md) 的 recovery 指令處理。

## Replay：逐日回放、審查與補齊知識

先確認核心 metrics 的定義，其餘業務關係可以隨案例逐步補充。啟動一次後，每一天都等待 PO 審查與明確的「下一天」操作；有新知識時才需要編輯、審核與發布，不需要每天重新啟動 replay。

```mermaid
sequenceDiagram
    actor PO as PO／指定 Reviewer
    actor DA as DA／維護者
    participant Agent as Replay Agent／執行系統
    participant Model as AI Model／Gemini
    participant Slack as Slack
    participant DB as Redshift

    DA->>Agent: replay-start：起日、終日、Reviewer 與 channel
    Agent->>DB: 建立 session，保存日期範圍與指定 Reviewer
    Note over Agent,DB: 未指定終日時，固定為啟動當下的昨天
    loop 逐日處理，失敗停在同一天，通過審查才推進
        Agent->>DB: 載入最新發布 catalog，鎖定本次知識版本
        DB-->>Agent: 發布版；尚無發布版則使用 bundled YAML
        Agent->>DB: Claim 當日工作並保存 attempt，防止重複執行
        Agent->>DB: 查詢指定歷史日期與所需 baseline
        DB-->>Agent: Metrics 與 guidelines
        Agent->>DB: 保存當日 evidence 與 context 快照
        Agent->>Model: 檢查 token 預算，再生成當日分析
        Model-->>Agent: 報告、調查計畫與 evidence
        Agent->>DB: 保存報告、驗證結果及執行狀態
        alt 資料、生成或驗證失敗
            Note over Agent: Session 停在 blocked；未知 Slack 傳送結果不自動重發
            DA->>Agent: 查核問題，必要時 recover，再 replay-resume
            Note over Agent: 重試同一天，不跳過失敗日期
        else 報告驗證通過
            Agent->>Slack: 發送一份歷史日報與「下一天」按鈕
            Agent->>DB: 保存 Slack 對應，session 等待審查
            Slack-->>PO: 閱讀該日報告
            alt 報告準確
                PO->>Slack: 按「準確」
                Slack->>Agent: Positive feedback
                Agent->>DB: 保存 positive 評價
            else 需要補充知識或糾正
                PO->>Slack: 糾正表單或該報告 thread 回覆
                Slack->>Agent: Correction feedback
                Agent->>DB: 先保存原文
                Agent->>Model: 提煉候選定義、關係或調查方法
                Model-->>Agent: Candidate
                Agent->>DB: 保存候選，尚不進 Prompt
                DA->>Agent: 查看候選 feedback
                Agent-->>DA: 原文與候選
                DA->>PO: 共建短版知識，確認事實與適用日期
                PO-->>DA: 決定是否納入
                alt 核准納入
                    Note over DA: 編輯 catalog.yaml、approved 狀態、feedback source 與新 version
                    DA->>Agent: knowledge-check 與 approved review
                    Agent->>DB: 保存審核，feedback 標記 incorporated
                    DA->>Agent: knowledge-publish
                    Agent->>DB: 保存完整 knowledge release，供下一天讀取
                else 拒絕納入
                    DA->>Agent: 記錄 rejected 與理由
                    Agent->>DB: 保留原文與審查紀錄
                end
            end
            PO->>Slack: 按「審查完成，下一天」
            Slack->>Agent: 背景執行下一天請求
            Agent->>DB: 核對指定 Reviewer、feedback 狀態與實際 catalog
            alt 門檻尚未滿足
                Agent->>Slack: 說明未完成的評價、審查或知識發布
                Slack-->>PO: 完成缺少的步驟，再按下一天
                Note over Agent: 日期保持不變
            else 門檻通過
                alt 已審查最後一天
                    Agent->>DB: Session 標記 complete，不再生成報告
                else 還有後續日期
                    Agent->>DB: 日期推進一天並 claim；重複點擊只接受一次
                    Note over Agent: 使用新版知識產生下一天報告
                end
            end
        end
    end
```

「下一天」門檻是**至少一則 positive 或 incorporated correction，且所有 correction 都已 incorporated／rejected**；已納入的知識必須存在於實際使用的 catalog。只有 rejected 不代表報告已準確，仍需補準確評價或其他已納入的回饋。Slack thread 收件需先啟用 Events API；本機 replay 另需可接收 Slack 請求的 server。

查詢進度用 `replay-status`，失敗恢復用 `replay-resume`，捨棄舊 session 用 `replay-cancel` 後另行 `replay-start`。取消會保留歷史報告、feedback 與已發布知識。這個流程改善 Agent 使用的知識，沒有更新模型權重，也尚未自動重新生成前一天報告做品質比較。完整指令見 [工作流程](docs/WORKFLOW.md)，環境設定見 [部署文件](docs/DEPLOY.md)。

## 保存與學習

原則仍讀自 `da_squad.agent_guidelines`。產品的 metric catalog／business knowledge 放在 `knowledge/<product>/catalog.yaml`，由 Git 保存版本。原始分析與 PO feedback 放在既有 Redshift：analysis_runs、analysis_run_payloads、feedback、feedback_reviews。逐日回放另使用 replay_sessions、replay_attempts、knowledge_releases。

每筆知識只描述一個關係或方法，每個文字欄位一行、最多 240 字。approved 且本次相關的知識才進 Prompt；候選 feedback 不會自動變成 active guideline。

資料與知識可累積，單次 Prompt 有 byte、項目與 token 上限。不臆測原因；異常需要 investigation plan 和可核對的 source evidence。程式檢查結構與引用，商業解讀仍需 PO 審查。

本版支援逐日歷史 replay：啟動一次，Slack 審查後按「下一天」。操作見 [WORKFLOW.md](docs/WORKFLOW.md)。回放校準的是 Agent 知識，沒有更新模型權重。示範 catalog 的業務內容為 draft，需 PO 核對後啟用。
