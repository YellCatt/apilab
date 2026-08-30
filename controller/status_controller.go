package controller

import (
	"encoding/json"
	"net/http"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/middleware"
	"github.com/YellCatt/apilab/service"
	"go.uber.org/zap"
)

// StatusController 系统状态相关的 HTTP 请求处理器。
type StatusController struct {
	service service.StatusService // 系统状态业务逻辑实例
}

// NewStatusController 创建一个新的状态控制器实例。
func NewStatusController(service service.StatusService) *StatusController {
	return &StatusController{service: service}
}

// GetStatus 处理 GET /status 请求，返回系统运行状态。
func (c *StatusController) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx, end := middleware.StartSpan(r.Context(), "controller", "status.get", "处理系统状态查询请求", nil)
	defer end()

	fields := middleware.Fields(r)
	logger.Debug("收到系统状态查询请求", fields...)

	status, err := c.service.GetStatus(ctx)
	if err != nil {
		logger.Error("获取系统状态失败", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(status); err != nil {
		logger.Error("序列化系统状态响应失败", append(fields, zap.Error(err))...)
		return
	}
	logger.Debug("系统状态查询成功",
		append(fields,
			zap.Float64("cpu_usage", status.Cpu.Usage),
			zap.Float64("mem_usage", status.Memory.Usage),
			zap.Int("disk_count", len(status.Disk)),
			zap.Uint64("uptime", status.Uptime),
		)...)
}
