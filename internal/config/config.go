package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
	"go.uber.org/zap"
)

type Config struct {
	Server   ServerConfig   `toml:"server"`
	Database DatabaseConfig `toml:"database"`
	OpenAI   OpenAIConfig   `toml:"openai"`
	Logging  LoggingConfig  `toml:"logging"`
	Tracker  TrackerConfig  `toml:"tracker"`
}

type ServerConfig struct {
	Port int    `toml:"port"`
	Host string `toml:"host"`
}

type DatabaseConfig struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	Password string `toml:"password"`
	DBName   string `toml:"dbname"`
	SSLMode  string `toml:"sslmode"`
}

type OpenAIConfig struct {
	APIKey      string `toml:"api_key"`
	Model       string `toml:"model"`
	BaseURL     string `toml:"base_url"`
	MaxRetries  int    `toml:"max_retries"`
	TimeoutSec  int    `toml:"timeout_sec"`
}

type LoggingConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

type TrackerConfig struct {
	BatchSize        int `toml:"batch_size"`
	FlushIntervalSec int `toml:"flush_interval_sec"`
}

func Load(configPath string) (*Config, error) {
	config := &Config{
		Server: ServerConfig{
			Port: 9090,
			Host: "0.0.0.0",
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "",
			DBName:   "meep",
			SSLMode:  "disable",
		},
		OpenAI: OpenAIConfig{
			APIKey:     "",
			Model:      "text-embedding-3-small",
			BaseURL:    "https://api.openai.com/v1",
			MaxRetries: 3,
			TimeoutSec: 30,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Tracker: TrackerConfig{
			BatchSize:        50,
			FlushIntervalSec: 5,
		},
	}

	if configPath == "" {
		configPath = "config.toml"
	}

	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		if err := toml.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Database.Port < 1 || c.Database.Port > 65535 {
		return fmt.Errorf("invalid database port: %d", c.Database.Port)
	}

	if c.Database.User == "" {
		return fmt.Errorf("database user is required")
	}

	if c.Database.DBName == "" {
		return fmt.Errorf("database name is required")
	}

	if c.OpenAI.APIKey == "" {
		return fmt.Errorf("OpenAI API key is required")
	}

	if c.OpenAI.Model == "" {
		return fmt.Errorf("OpenAI model is required")
	}

	return nil
}

func (c *Config) DatabaseDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Host,
		c.Database.Port,
		c.Database.User,
		c.Database.Password,
		c.Database.DBName,
		c.Database.SSLMode,
	)
}

func (c *Config) ToZapConfig() *zap.Config {
	var zapConfig zap.Config
	if c.Logging.Format == "console" {
		zapConfig = zap.NewDevelopmentConfig()
	} else {
		zapConfig = zap.NewProductionConfig()
	}

	switch c.Logging.Level {
	case "debug":
		zapConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapConfig.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapConfig.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	return &zapConfig
}