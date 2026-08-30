package controller

import (
	"encoding/json"
	"net/http"

	"github.com/example/gapi/model"
	"github.com/example/gapi/service"
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
	var req model.TraceReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Events) == 0 {
		http.Error(w, "events must not be empty", http.StatusBadRequest)
		return
	}

	c.service.Report(req.Events)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    0,
		"message": "ok",
		"count":   len(req.Events),
	})
}
