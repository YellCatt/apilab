package config

import (
	"fmt"
	"log"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// slowSQLThreshold 超过该耗时的 SQL 会被 GORM 判定为慢查询。
const slowSQLThreshold = 200 * time.Millisecond

// NewDatabase 根据配置创建并返回一个 SQLite 数据库连接，同时自动迁移 User 模型。
func NewDatabase() *gorm.DB {
	start := time.Now()
	logger.Debug("正在打开数据库", zap.String("path", cfg.Database.Path))

	db, err := gorm.Open(sqlite.Open(cfg.Database.Path), &gorm.Config{
		// SQL 日志接到 zap：调试级别开启时打印全部 SQL，否则只打印慢查询与错误。
		Logger: newGormLogger(),
	})
	if err != nil {
		logger.Error("打开数据库失败",
			zap.String("path", cfg.Database.Path), zap.Duration("cost", time.Since(start)), zap.Error(err))
		log.Fatalf("打开数据库失败: %v", err)
	}
	logger.Debug("数据库已打开",
		zap.String("path", cfg.Database.Path), zap.Duration("cost", time.Since(start)))

	migrateStart := time.Now()
	err = db.AutoMigrate(&model.User{})
	if err != nil {
		logger.Error("数据库迁移失败",
			zap.Duration("cost", time.Since(migrateStart)), zap.Error(err))
		log.Fatalf("数据库迁移失败: %v", err)
	}
	logger.Debug("数据库迁移完成",
		zap.String("model", "User"), zap.Duration("cost", time.Since(migrateStart)))

	return db
}

// newGormLogger 按日志配置决定 GORM 的输出粒度，并把输出转发到 zap 的 Debug 级别。
func newGormLogger() gormlogger.Interface {
	level := gormlogger.Warn
	if IsDebugEnabled() {
		level = gormlogger.Info
	}
	return gormlogger.New(gormLogWriter{}, gormlogger.Config{
		SlowThreshold:             slowSQLThreshold,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// gormLogWriter 实现 gorm logger.Writer，把 GORM 的输出写成一条 zap 调试日志。
type gormLogWriter struct{}

// Printf 接收 GORM 的格式化输出并落到 zap。
func (gormLogWriter) Printf(format string, args ...interface{}) {
	logger.Debug("SQL 语句", zap.String("detail", fmt.Sprintf(format, args...)))
}
