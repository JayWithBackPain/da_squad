package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	appruntime "github.com/jay/da-agents/internal/runtime"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Product  string         `yaml:"-"`
	Database DatabaseConfig `yaml:"database"`
	Gemini   GeminiConfig   `yaml:"gemini"`
	Slack    SlackConfig    `yaml:"slack"`
	Server   ServerConfig   `yaml:"server"`
	Analyze  AnalyzeConfig  `yaml:"analyze"`
}

type DatabaseConfig struct {
	Redshift DBConn `yaml:"redshift"`
}

type DBConn struct {
	Driver  string `yaml:"driver"`
	ConnStr string `yaml:"conn_str"`
}

type GeminiConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

type SlackConfig struct {
	BotToken      string `yaml:"bot_token"`
	SigningSecret string `yaml:"signing_secret"`
	ChannelID     string `yaml:"channel_id"`
}

type ServerConfig struct {
	ListenAddr string `yaml:"listen_addr"`
}

type AnalyzeConfig struct {
	QueryDir          string `yaml:"query_dir"`
	ReportDate        string `yaml:"report_date"`
	Workers           int    `yaml:"workers"`
	MaxRowsPerQuery   int    `yaml:"max_rows_per_query"`
	KnowledgePath     string `yaml:"knowledge_path"`
	Timezone          string `yaml:"timezone"`
	MaxInputTokens    int    `yaml:"max_input_tokens"`
	MaxInputBytes     int    `yaml:"max_input_bytes"`
	MaxKnowledgeBytes int    `yaml:"max_knowledge_bytes"`
	MaxKnowledgeItems int    `yaml:"max_knowledge_items"`
}

// Load reads config/<product>/config.yaml relative to runtime.RootDir.
func Load(product string) (*Config, error) {
	if product == "" {
		product = "goodnight"
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(product) {
		return nil, fmt.Errorf("invalid product name %q", product)
	}
	path, err := appruntime.Resolve("config", product, "config.yaml")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		example, _ := appruntime.Resolve("config", "default", "config.example.yaml")
		return nil, fmt.Errorf("config not found at %s (copy %s to config.yaml): %w", path, example, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Product = product
	applyEnvOverrides(&cfg)
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if cfg.Product == "" {
		cfg.Product = "goodnight"
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		cfg.Gemini.APIKey = v
	}
	if v := os.Getenv("SLACK_BOT_TOKEN"); v != "" {
		cfg.Slack.BotToken = v
	}
	if v := os.Getenv("SLACK_SIGNING_SECRET"); v != "" {
		cfg.Slack.SigningSecret = v
	}
	if v := os.Getenv("SLACK_CHANNEL_ID"); v != "" {
		cfg.Slack.ChannelID = v
	}
	if v := os.Getenv("REDSHIFT_CONN_STR"); v != "" {
		cfg.Database.Redshift.ConnStr = v
	}
	if v := os.Getenv("DA_AGENT_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Analyze.Workers = n
		}
	}
	if cfg.Database.Redshift.Driver == "" {
		cfg.Database.Redshift.Driver = "postgres"
	}
	if cfg.Gemini.Model == "" {
		cfg.Gemini.Model = "gemini-2.0-flash"
	}
	if cfg.Server.ListenAddr == "" {
		cfg.Server.ListenAddr = ":8080"
	}
	if cfg.Analyze.Workers <= 0 {
		cfg.Analyze.Workers = 4
	}
	if cfg.Analyze.MaxRowsPerQuery <= 0 {
		cfg.Analyze.MaxRowsPerQuery = 200
	}
	if cfg.Analyze.Timezone == "" {
		cfg.Analyze.Timezone = "Asia/Taipei"
	}
	if cfg.Analyze.MaxInputTokens == 0 {
		cfg.Analyze.MaxInputTokens = 12000
	}
	if cfg.Analyze.MaxInputBytes == 0 {
		cfg.Analyze.MaxInputBytes = 60000
	}
	if cfg.Analyze.MaxKnowledgeBytes == 0 {
		cfg.Analyze.MaxKnowledgeBytes = 8000
	}
	if cfg.Analyze.MaxKnowledgeItems == 0 {
		cfg.Analyze.MaxKnowledgeItems = 12
	}
	if cfg.Analyze.KnowledgePath == "" {
		cfg.Analyze.KnowledgePath = "knowledge/" + cfg.Product + "/catalog.yaml"
	}
	if cfg.Analyze.KnowledgePath == "-" {
		cfg.Analyze.KnowledgePath = ""
	}
	if cfg.Analyze.QueryDir == "" {
		cfg.Analyze.QueryDir = "queries/" + cfg.Product
	}
}

func (c *Config) Validate() error {
	if err := c.ValidateAnalyzeSettings(); err != nil {
		return err
	}
	var missing []string
	if strings.TrimSpace(c.Database.Redshift.ConnStr) == "" {
		missing = append(missing, "database.redshift.conn_str (or REDSHIFT_CONN_STR)")
	}
	if strings.TrimSpace(c.Gemini.APIKey) == "" {
		missing = append(missing, "gemini.api_key (or GEMINI_API_KEY)")
	}
	if strings.TrimSpace(c.Slack.BotToken) == "" {
		missing = append(missing, "slack.bot_token (or SLACK_BOT_TOKEN)")
	}
	if strings.TrimSpace(c.Slack.ChannelID) == "" {
		missing = append(missing, "slack.channel_id (or SLACK_CHANNEL_ID)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateServer requires fields needed by the Slack interactivity server.
func (c *Config) ValidateServer() error {
	var missing []string
	if strings.TrimSpace(c.Database.Redshift.ConnStr) == "" {
		missing = append(missing, "database.redshift.conn_str (or REDSHIFT_CONN_STR)")
	}
	if strings.TrimSpace(c.Gemini.APIKey) == "" {
		missing = append(missing, "gemini.api_key (or GEMINI_API_KEY)")
	}
	if strings.TrimSpace(c.Slack.BotToken) == "" {
		missing = append(missing, "slack.bot_token (or SLACK_BOT_TOKEN)")
	}
	if strings.TrimSpace(c.Slack.SigningSecret) == "" {
		missing = append(missing, "slack.signing_secret (or SLACK_SIGNING_SECRET)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config for server: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateAnalyzeSettings is usable before opening any external connections.
func (c *Config) ValidateAnalyzeSettings() error {
	if _, err := time.LoadLocation(c.Analyze.Timezone); err != nil {
		return fmt.Errorf("analyze.timezone: %w", err)
	}
	if c.Analyze.ReportDate != "" {
		if _, err := time.Parse("2006-01-02", c.Analyze.ReportDate); err != nil {
			return fmt.Errorf("analyze.report_date: %w", err)
		}
	}
	if c.Analyze.MaxInputTokens <= 0 || c.Analyze.MaxInputBytes <= 0 || c.Analyze.MaxKnowledgeBytes <= 0 || c.Analyze.MaxKnowledgeItems <= 0 {
		return fmt.Errorf("analyze budgets must be positive")
	}
	if c.Analyze.MaxKnowledgeBytes > c.Analyze.MaxInputBytes {
		return fmt.Errorf("knowledge byte budget must not exceed total input byte budget")
	}
	return nil
}
