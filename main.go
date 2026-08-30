// Package main 是 apilab 服务的入口，负责组装各层组件并启动 HTTP 服务器。
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/YellCatt/apilab/config"
	"github.com/YellCatt/apilab/controller"
	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/repository"
	"github.com/YellCatt/apilab/router"
	"github.com/YellCatt/apilab/service"

	"go.uber.org/zap"
)

// main 程序入口：加载配置、初始化日志与数据库、组装依赖、启动 HTTP 服务。
func main() {
	config.LoadConfig()

	if err := config.InitDirectories(); err != nil {
		log.Fatalf("failed to init directories: %v", err)
	}

	logOpts := logger.Options{
		Dir:            config.GetLogPath(),
		Level:          config.GetLogLevel(),
		Mode:           config.GetLogMode(),
		Levels:         config.GetLogLevels(),
		DisableConsole: config.IsLogConsoleDisabled(),
	}
	if err := logger.Init(logOpts); err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer logger.Close()

	// 启动参数落一份调试日志，排查环境问题时不必再翻配置文件。
	logger.Debug("configuration loaded",
		zap.Int("server.port", config.GetServerPort()),
		zap.String("database.path", config.GetDatabasePath()),
		zap.String("log.path", logOpts.Dir),
		zap.String("log.level", logOpts.Level),
		zap.String("log.mode", logOpts.Mode),
		zap.Strings("log.levels", logOpts.Levels),
		zap.Bool("log.disable_console", logOpts.DisableConsole),
		zap.String("collector.url", config.GetCollectorURL()),
		zap.Int("collector.batch_size", config.GetCollectorBatchSize()),
		zap.Duration("collector.flush_interval", config.GetCollectorFlushInterval()),
	)

	db := config.NewDatabase()

	userRepo := repository.NewUserRepository(db)
	userService := service.NewUserService(userRepo)
	userController := controller.NewUserController(userService)

	statusService := service.NewStatusService()
	statusController := controller.NewStatusController(statusService)

	traceService := service.NewTraceService(config.GetCollectorURL(), config.GetCollectorBatchSize(), config.GetCollectorFlushInterval())
	defer traceService.Stop()
	traceController := controller.NewTraceController(traceService)

	logger.Debug("dependencies initialized",
		zap.String("db_path", config.GetDatabasePath()),
		zap.String("collector_url", config.GetCollectorURL()),
	)

	r := router.NewRouter(userController, statusController, traceController)

	port := config.GetServerPort()
	addr := fmt.Sprintf(":%d", port)
	logger.Debug("routes registered",
		zap.Strings("routes", []string{
			"GET /health",
			"GET /status",
			"POST /api/users",
			"GET /api/users",
			"GET /api/users/{id}",
			"PUT /api/users/{id}",
			"DELETE /api/users/{id}",
			"POST /api/traces/report",
			"GET /swagger/doc.json",
			"GET /swagger/",
		}),
	)
	logger.Info("server starting", zap.String("addr", addr), zap.Int("port", port))
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Fatal("server exited", zap.Error(err))
	}
	logger.Debug("server shutdown complete")
}
