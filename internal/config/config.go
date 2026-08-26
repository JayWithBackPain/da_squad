package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	appruntime "github.com/jay/da-agents/internal/runtime"
)

type Config struct {
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
	QueryDir        string `yaml:"query_dir"`
	ReportDate      string `yaml:"report_date"`
	Workers         int    `yaml:"workers"`
	MaxRowsPerQuery int    `yaml:"max_rows_per_query"`
}

// Load reads config/<product>/config.yaml relative to runtime.RootDir.
func Load(product string) (*Config, error) {
	if product == "" {
		product = "goodnight"
	}
	path, err := appruntime.Resolve("config", product, "config.yaml")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		example := filepath.Join(filepath.Dir(path), "config.example.yaml")
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
	applyEnvOverrides(&cfg)
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
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
	if cfg.Analyze.QueryDir == "" {
		cfg.Analyze.QueryDir = "queries/goodnight"
	}
}

func (c *Config) Validate() error {
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
