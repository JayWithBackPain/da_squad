# Report Prompt 結構（精簡版）

每日只送一次 Gemini。固定規則放 **system**；**user** 只帶當日變動資料。

```mermaid
flowchart TB
  subgraph system [system 短規則約半頁]
    R1[繁中／禁 SQL／禁編造]
    R2[交叉參照勿各說各話]
    R3[必要時才點名 metrics]
    R4[昨日／前日／上週同日]
    R5[歸因禁系統與 ETL]
  end
  subgraph user [user 當日資料]
    D[report_date 與日期對照]
    C[catalog 一行一個指標]
    G[active guidelines]
    T[各指標資料表]
    J[JSON schema 一行]
  end
  system --> Gemini
  user --> Gemini
```

| 區塊 | 內容 | 是否每日變動 |
|------|------|--------------|
| system | 6 條站穩規則 | 否（程式常數） |
| dates | 昨日／前日／上週同日對照 | 是 |
| catalog | `指標名 [role] → 支援哪些指標` | 是 |
| guidelines | RDS active 規則 | 偶發 |
| data | 各表數字 | 是 |
| json | 輸出格式提醒 | 否（極短） |
