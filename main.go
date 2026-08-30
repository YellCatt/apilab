// Package main 是 apilab 服务的入口，负责组装各层组件并启动 HTTP 服务器。
package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"

	"github.com/YellCatt/apilab/config"
	"github.com/YellCatt/apilab/controller"
	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/repository"
	"github.com/YellCatt/apilab/router"
	"github.com/YellCatt/apilab/service"
	"github.com/YellCatt/apilab/trace"

	"go.uber.org/zap"
)

// version 服务版本号。默认 dev，构建时可用 ldflags 覆盖：
//
//	go build -ldflags "-X main.version=v1.2.3" .
var version = "v1.0.0-20260830-1630"

// main 程序入口：加载配置、初始化日志与数据库、组装依赖、启动 HTTP 服务。
func main() {
	config.LoadConfig()

	if err := config.InitDirectories(); err != nil {
		log.Fatalf("初始化目录失败: %v", err)
	}

	logOpts := logger.Options{
		Dir:            config.GetLogPath(),
		Level:          config.GetLogLevel(),
		Mode:           config.GetLogMode(),
		Levels:         config.GetLogLevels(),
		DisableConsole: config.IsLogConsoleDisabled(),
	}
	if err := logger.Init(logOpts); err != nil {
		log.Fatalf("初始化日志组件失败: %v", err)
	}
	defer logger.Close()

	// 版本号用 Info 输出：无论日志级别如何配置，启动后都能一眼看到跑的是哪版。
	logger.Info("服务启动",
		zap.String("version", version),
		zap.String("go_version", runtime.Version()),
	)

	// 启动参数落一份调试日志，排查环境问题时不必再翻配置文件。
	logger.Debug("配置加载完成",
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

	traceService := service.NewTraceService(config.GetCollectorURL(), config.GetCollectorBatchSize(), config.GetCollectorFlushInterval())
	defer traceService.Stop()
	traceController := controller.NewTraceController(traceService)

	// 链路追踪必须在建库之前初始化：otelgorm 插件是在 gorm.Open 时注册的，
	// 顺序反了插件就会绑到 no-op tracer 上，SQL span 一条都收不到。
	// 关闭顺序也相反：先 shutdown 把剩余 span 导出，再停上报器把它们发出去。
	shutdownTrace, err := trace.Init(trace.Options{
		ServiceName: "apilab",
		Reporter:    traceService,
	})
	if err != nil {
		log.Fatalf("初始化链路追踪失败: %v", err)
	}
	defer shutdownTrace()

	db := config.NewDatabase()

	userRepo := repository.NewUserRepository(db)
	userService := service.NewUserService(userRepo)
	userController := controller.NewUserController(userService)

	statusService := service.NewStatusService()
	statusController := controller.NewStatusController(statusService)

	logger.Debug("依赖注入完成",
		zap.String("db_path", config.GetDatabasePath()),
		zap.String("collector_url", config.GetCollectorURL()),
	)

	// HTTP 根 span 由 middleware.RequestLog 创建，SQL span 由 otelgorm 自动生成，
	// 业务函数 span 由 middleware.Span 手动添加，三者按 context 自动串成一棵树。
	r := router.NewRouter(userController, statusController, traceController)

	port := config.GetServerPort()
	addr := fmt.Sprintf(":%d", port)
	logger.Debug("路由注册完成",
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
	logger.Info("HTTP 服务启动中", zap.String("addr", addr), zap.Int("port", port))
	if err := http.ListenAndServe(addr, r); err != nil {
		logger.Fatal("HTTP 服务异常退出", zap.Error(err))
	}
	logger.Debug("HTTP 服务已正常关闭")
}
