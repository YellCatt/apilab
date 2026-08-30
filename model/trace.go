// Package model 定义了应用程序中使用的数据模型（数据库实体及请求/响应结构）。
package model

import "time"

// TraceEvent 一条 trace 事件日志，对应采集端定义的字段。
type TraceEvent struct {
	ServiceName  string                 `json:"service_name,omitempty"`   // 产生该事件的服务名
	TraceID      string                 `json:"trace_id"`                 // 链路 ID
	SpanID       string                 `json:"span_id"`                  // 当前 Span ID
	ParentSpanID string                 `json:"parent_span_id,omitempty"` // 父 Span ID（空表示根）
	Timestamp    time.Time              `json:"timestamp"`                // 事件发生时间（RFC3339）
	Level        string                 `json:"level"`                    // 日志级别（debug/info/warn/error）
	Module       string                 `json:"module"`                   // 所属模块
	Event        string                 `json:"event"`                    // 事件名称
	Message      string                 `json:"message"`                  // 事件描述
	Params       map[string]interface{} `json:"params,omitempty"`         // 附加参数
	ErrorMessage string                 `json:"error_message,omitempty"`  // 错误信息
	URL          string                 `json:"url,omitempty"`            // 所属请求 URL，采集端据此按接口归类链路
}

// TraceReportRequest 上报 trace 事件的请求体。
type TraceReportRequest struct {
	ServiceName string       `json:"service_name,omitempty"` // 整批事件共属的服务名，事件未带 service_name 时用它兜底
	URL         string       `json:"url,omitempty"`          // 整批事件共属的请求 URL，事件未带 url 时用它兜底
	Events      []TraceEvent `json:"events"`
}
