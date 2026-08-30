// Package config 负责配置文件的加载、数据库连接初始化与日志目录创建。
package config

import (
	"log"
	"path/filepath"
	"time"

	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用程序的根配置结构，聚合了服务、数据库、日志和采集端配置。
type Config struct {
	Server    ServerConfig    `yaml:"server"`    // 服务端配置
	Database  DatabaseConfig  `yaml:"database"`  // 数据库配置
	Log       LogConfig       `yaml:"log"`       // 日志配置
	Collector CollectorConfig `yaml:"collector"` // 采集端配置
}

// ServerConfig 服务端相关配置，包括监听端口。
type ServerConfig struct {
	Port int `yaml:"port"` // 服务监听端口
}

// DatabaseConfig 数据库配置，包括数据库文件路径。
type DatabaseConfig struct {
	Path string `yaml:"path"` // 数据库文件路径
}

// LogConfig 日志配置，包括日志目录和日志级别。
type LogConfig struct {
	Path  string `yaml:"path"`  // 日志文件存放目录
	Level string `yaml:"level"` // 日志输出级别（debug/info/warn/error）
}

// CollectorConfig 采集端配置，用于批量上报 trace 事件。
type CollectorConfig struct {
	URL           string `yaml:"url"`           // 采集端接收地址（如 OTEL Collector / 日志采集服务）
	BatchSize     int    `yaml:"batch_size"`    // 缓冲达到该数量时立即批量上报
	FlushInterval string `yaml:"flush_interval"` // 定时刷新间隔（如 30s）
}

var cfg Config // 全局配置实例，由 LoadConfig 加载

// LoadConfig 从 config/config.yaml 加载配置文件。
// 如果配置文件不存在，则自动创建默认配置文件。
func LoadConfig() {
	configPath := "config/config.yaml"

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Println("config file not found, creating default config...")
		if err := createDefaultConfig(configPath); err != nil {
			log.Fatalf("failed to create default config: %v", err)
		}
	}

	file, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("failed to read config file: %v", err)
	}

	err = yaml.Unmarshal(file, &cfg)
	if err != nil {
		log.Fatalf("failed to parse config file: %v", err)
	}
}

// createDefaultConfig 创建默认配置文件并写入指定路径。
func createDefaultConfig(path string) error {
	defaultCfg := Config{
		Server: ServerConfig{
			Port: 8084,
		},
		Database: DatabaseConfig{
			Path: "./data.db",
		},
		Log: LogConfig{
			Path:  "./logs",
			Level: "info",
		},
		Collector: CollectorConfig{
			URL:           "http://localhost:4318/v1/traces",
			BatchSize:     1000,
			FlushInterval: "30s",
		},
	}

	data, err := yaml.Marshal(&defaultCfg)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// GetServerPort 返回当前服务配置的监听端口号。
func GetServerPort() int {
	return cfg.Server.Port
}

// GetDatabasePath 返回数据库文件的存储路径。
func GetDatabasePath() string {
	return cfg.Database.Path
}

// GetLogPath 返回日志文件的存储目录。
func GetLogPath() string {
	return cfg.Log.Path
}

// GetLogLevel 返回当前配置的日志级别。
func GetLogLevel() string {
	return cfg.Log.Level
}

// GetCollectorURL 返回采集端接收地址。
func GetCollectorURL() string {
	return cfg.Collector.URL
}

// GetCollectorBatchSize 返回批量上报的缓冲阈值，未配置时默认 1000。
func GetCollectorBatchSize() int {
	if cfg.Collector.BatchSize <= 0 {
		return 1000
	}
	return cfg.Collector.BatchSize
}

// GetCollectorFlushInterval 返回定时刷新间隔，未配置或解析失败时默认 30 秒。
func GetCollectorFlushInterval() time.Duration {
	d, err := time.ParseDuration(cfg.Collector.FlushInterval)
	if err != nil || d <= 0 {
		return 30 * time.Second
	}
	return d
}

// InitDirectories 初始化所需的日志目录和数据库目录。
func InitDirectories() error {
	if err := os.MkdirAll(cfg.Log.Path, 0755); err != nil {
		return err
	}

	dbDir := filepath.Dir(cfg.Database.Path)
	if dbDir != "." && dbDir != "" {
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			return err
		}
	}

	return nil
}
