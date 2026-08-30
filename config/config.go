// Package config 负责配置文件的加载、数据库连接初始化与日志目录创建。
package config

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// LogConfig 日志配置，包括日志目录、日志级别、输出模式与级别白名单。
type LogConfig struct {
	Path  string `yaml:"path"`  // 日志文件存放目录
	Level string `yaml:"level"` // 日志输出级别（debug/info/warn/error）
	Mode  string `yaml:"mode"`  // 输出模式（single/split/range），缺省为 single
	// Levels 级别白名单，非空时优先于 Level，例如 []string{"warn", "error"}。
	Levels         []string `yaml:"levels"`
	DisableConsole bool     `yaml:"disable_console"` // 是否关闭控制台输出
}

// CollectorConfig 采集端配置，用于批量上报 trace 事件。
type CollectorConfig struct {
	URL           string `yaml:"url"`            // 采集端接收地址（如 OTEL Collector / 日志采集服务）
	BatchSize     int    `yaml:"batch_size"`     // 缓冲达到该数量时立即批量上报
	FlushInterval string `yaml:"flush_interval"` // 定时刷新间隔（如 30s）
}

var cfg Config // 全局配置实例，由 LoadConfig 加载

// LoadConfig 从 config/config.yaml 加载配置文件。
// 如果配置文件不存在，则自动创建默认配置文件。
func LoadConfig() {
	configPath := "config/config.yaml"

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Println("未找到配置文件，正在创建默认配置...")
		if err := createDefaultConfig(configPath); err != nil {
			log.Fatalf("创建默认配置失败: %v", err)
		}
	}

	file, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("读取配置文件失败: %v", err)
	}

	err = yaml.Unmarshal(file, &cfg)
	if err != nil {
		log.Fatalf("解析配置文件失败: %v", err)
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
			Path:           "./logs",
			Level:          "info",
			Mode:           "single",
			Levels:         nil,
			DisableConsole: false,
		},
		Collector: CollectorConfig{
			URL:           "http://localhost:4318/api/traces",
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

// GetLogMode 返回日志输出模式（single/split/range），未配置时由 logger 兜底为 single。
func GetLogMode() string {
	return cfg.Log.Mode
}

// GetLogLevels 返回日志级别白名单，非空时优先于 GetLogLevel。
func GetLogLevels() []string {
	return cfg.Log.Levels
}

// IsLogConsoleDisabled 返回是否关闭控制台日志输出。
func IsLogConsoleDisabled() bool {
	return cfg.Log.DisableConsole
}

// IsDebugEnabled 判断当前配置是否输出 debug 级别日志，用于决定 SQL 等高频调试日志是否开启。
// levels 白名单非空时以它为准，否则看 level 是否为 debug。
func IsDebugEnabled() bool {
	if len(cfg.Log.Levels) > 0 {
		for _, lv := range cfg.Log.Levels {
			if strings.EqualFold(strings.TrimSpace(lv), "debug") {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.Log.Level), "debug")
}

// GetCollectorURL 返回采集端接收地址，未配置时默认 http://localhost:4318/api/traces。
func GetCollectorURL() string {
	if cfg.Collector.URL == "" {
		return "http://localhost:4318/api/traces"
	}
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
