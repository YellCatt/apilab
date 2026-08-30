// Package main 是 apilab 服务的入口，负责组装各层组件并启动 HTTP 服务器。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

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

// shutdownTimeout 优雅关闭的最长等待时间：给在途请求留出收尾时间，
// 超时后强制断开，避免容器停了进程却迟迟不退、最终被 SIGKILL。
const shutdownTimeout = 30 * time.Second

// main 程序入口。启动错误一律从 run 返回，保证 run 里的 defer
// （shutdownTrace / traceService.Stop / logger.Close）有机会执行。
func main() {
	if err := run(); err != nil {
		// 走到这里 run 的 defer 已全部执行、日志文件已关闭，
		// 只能落 stderr（容器场景由 stdout/stderr 收集）。
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
		os.Exit(1)
	}
}

// run 加载配置、初始化日志与数据库、组装依赖，并阻塞到服务退出。
func run() error {
	config.LoadConfig()

	if err := config.InitDirectories(); err != nil {
		return fmt.Errorf("初始化目录失败: %w", err)
	}

	logOpts := logger.Options{
		Dir:            config.GetLogPath(),
		Level:          config.GetLogLevel(),
		Mode:           config.GetLogMode(),
		Levels:         config.GetLogLevels(),
		DisableConsole: config.IsLogConsoleDisabled(),
	}
	if err := logger.Init(logOpts); err != nil {
		return fmt.Errorf("初始化日志组件失败: %w", err)
	}
	defer logger.Close()

	// 版本号用 Info 输出：无论日志级别如何配置，启动后都能一眼看到跑的是哪版。
	logger.Info("服务启动",
		zap.String("version", version),
		zap.String("go_version", runtime.Version()),
	)

	// 启动参数落一份调试日志，排查环境问题时不必再翻配置文件。
	logger.Debug("配置加载完成",
		zap.String("service.name", config.GetServiceName()),
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

	traceService := service.NewTraceService(
		config.GetCollectorURL(), config.GetServiceName(),
		config.GetCollectorBatchSize(), config.GetCollectorFlushInterval(),
	)
	defer traceService.Stop()
	traceController := controller.NewTraceController(traceService)

	// 链路追踪必须在建库之前初始化：otelgorm 插件是在 gorm.Open 时注册的，
	// 顺序反了插件就会绑到 no-op tracer 上，SQL span 一条都收不到。
	// 关闭顺序也相反：先 shutdown 把剩余 span 导出，再停上报器把它们发出去。
	shutdownTrace, err := trace.Init(trace.Options{
		ServiceName: config.GetServiceName(),
		Reporter:    traceService,
	})
	if err != nil {
		err = fmt.Errorf("初始化链路追踪失败: %w", err)
		logger.Error("服务启动失败", zap.Error(err))
		return err
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

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
		// 慢客户端只发请求头不发包时，别让连接一直挂着占资源。
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("HTTP 服务启动中", zap.String("addr", addr), zap.Int("port", port))
	// ListenAndServe 会一直阻塞，放到后台协程，主协程转去等退出信号：
	// 这样收到 SIGTERM（Docker/K8s 停容器）能走 Shutdown，
	// defer 才会执行，缓冲里的 trace 事件与日志缓冲才不会丢。
	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
			return
		}
		serverErrCh <- nil
	}()
	logger.Debug("HTTP 服务启动完成，等待请求中", zap.String("addr", addr), zap.Int("port", port))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case sig := <-quit:
		logger.Info("收到退出信号，正在优雅关闭服务", zap.String("signal", sig.String()))
	case err := <-serverErrCh:
		if err == nil {
			return nil
		}
		err = fmt.Errorf("HTTP 服务异常退出: %w", err)
		logger.Error("服务运行失败", zap.Error(err))
		return err
	}

	// Shutdown 会先关闭监听、再等在途请求结束；剩余 trace 事件由 defer 兜底上报。
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("HTTP 服务关闭超时，已强制断开连接", zap.Error(err))
		return nil
	}
	logger.Info("HTTP 服务已正常关闭")
	return nil
}
