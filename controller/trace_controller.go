package controller

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/middleware"
	"github.com/YellCatt/apilab/model"
	"github.com/YellCatt/apilab/service"
	"go.uber.org/zap"
)

// TraceController Trace 事件上报相关的 HTTP 请求处理器。
type TraceController struct {
	service service.TraceService // Trace 上报业务逻辑实例
}

// NewTraceController 创建一个新的 Trace 控制器实例。
func NewTraceController(service service.TraceService) *TraceController {
	return &TraceController{service: service}
}

// Report 处理 POST /api/traces/report 请求，接收 trace 事件并交给服务层缓冲、批量转发采集端。
func (c *TraceController) Report(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	fields := middleware.Fields(r)
	logger.Debug("收到 Trace 上报请求", append(fields, zap.Int64("content_length", r.ContentLength))...)

	var req model.TraceReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("Trace 上报请求体无效", append(fields, zap.Error(err))...)
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Events) == 0 {
		logger.Warn("Trace 上报请求中没有任何事件", fields...)
		http.Error(w, "events must not be empty", http.StatusBadRequest)
		return
	}
	logger.Debug("Trace 事件接收成功", append(fields, zap.Int("count", len(req.Events)))...)

	c.service.Report(req.Events)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    0,
		"message": "ok",
		"count":   len(req.Events),
	}); err != nil {
		logger.Error("序列化 Trace 上报响应失败", append(fields, zap.Int("count", len(req.Events)), zap.Error(err))...)
		return
	}
	logger.Debug("Trace 上报响应已发送",
		append(fields,
			zap.Int("count", len(req.Events)),
			zap.Duration("handler_cost", time.Since(start)),
		)...)
}
