// Package middleware 提供 HTTP 请求级别的调试能力：请求 ID 注入与访问日志。
//
// 中间件以 http.HandlerFunc 为单位实现，可直接注册到 http.ServeMux。
// 请求 ID 会写入响应头 X-Request-ID 并放进 context，各层日志带上它即可按一次调用串联。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.uber.org/zap"
)

// RequestIDHeader 请求 ID 的 HTTP 头名称。上游传了就沿用，方便跨服务串联同一次调用。
const RequestIDHeader = "X-Request-ID"

// slowRequestThreshold 请求耗时超过该值时按慢请求处理，用 Warn 级别输出。
const slowRequestThreshold = 500 * time.Millisecond

// skipTracePaths 这些路径不生成 trace 事件：
//   - /health 是探活接口，外部通常每 10s 一次，上报会把采集端刷爆；
//   - /swagger/ 是静态文档；
//   - /api/traces/report 是上报入口，它自身产生的事件会再次进入缓冲形成自反馈。
var skipTracePaths = []string{"/health", "/swagger/", "/api/traces/report"}

// requestIDKey 是请求 ID 在 context 中的私有键类型，避免与其它包的键冲突。
type requestIDKey struct{}

// Reporter 上报 trace 事件的抽象。由 service.TraceService 实现，
// 接口定义在 middleware 包可避免中间件直接依赖 service 包。
type Reporter interface {
	Report(events []model.TraceEvent)
}

// RequestLog 为请求注入 ID，并在进入/离开处理器时打印调试与访问日志。
// reporter 非 nil 时，请求结束还会生成一条 trace 事件交给它缓冲、批量转发采集端。
func RequestLog(next http.HandlerFunc, reporter Reporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := requestIDFromHeader(r)
		w.Header().Set(RequestIDHeader, id)
		// 放进 context 后，后续各层都能用 RequestIDFrom 取出同一个 ID。
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))

		start := time.Now()
		logger.Debug("请求开始处理",
			zap.String("request_id", id),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("query", r.URL.RawQuery),
			zap.String("remote_addr", r.RemoteAddr),
			zap.Int64("content_length", r.ContentLength),
			zap.String("user_agent", r.UserAgent()),
		)

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)

		cost := time.Since(start)
		fields := []zap.Field{
			zap.String("request_id", id),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", rec.status),
			zap.Duration("cost", cost),
			zap.Int("bytes", rec.bytes),
		}
		switch {
		case rec.status >= http.StatusInternalServerError:
			logger.Error("请求处理失败", fields...)
		case cost >= slowRequestThreshold:
			logger.Warn("慢请求", fields...)
		case rec.status >= http.StatusBadRequest:
			logger.Warn("请求被拒绝", fields...)
		default:
			logger.Info("请求处理完成", fields...)
		}

		if reporter != nil && !shouldSkipTrace(r.URL.Path) {
			event := buildTraceEvent(r, id, rec.status, cost, rec.bytes)
			// 异步上报：flush 走到网络 IO，不能让它拖慢请求响应。
			go reporter.Report([]model.TraceEvent{event})
		}
	}
}

// shouldSkipTrace 判断该路径是否需要生成 trace 事件。
func shouldSkipTrace(path string) bool {
	for _, prefix := range skipTracePaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// buildTraceEvent 把一次请求的处理结果封装成 trace 事件。
// trace_id 沿用请求 ID，这样采集端与本地日志能按同一 ID 对上。
func buildTraceEvent(r *http.Request, requestID string, status int, cost time.Duration, bytes int) model.TraceEvent {
	query := r.URL.RawQuery
	message := r.Method + " " + r.URL.Path + " " + strconv.Itoa(status)
	if query != "" {
		message += "?" + query
	}

	event := model.TraceEvent{
		TraceID:   requestID,
		SpanID:    newSpanID(),
		Timestamp: time.Now(),
		Level:     traceLevel(status, cost),
		Module:    moduleFromPath(r.URL.Path),
		Event:     "http.request",
		Message:   message,
		Params: map[string]interface{}{
			"request_id":     requestID,
			"method":         r.Method,
			"path":           r.URL.Path,
			"query":          query,
			"status":         status,
			"cost_ms":        math.Round(float64(cost.Microseconds())/100) / 10,
			"response_bytes": bytes,
			"remote_addr":    r.RemoteAddr,
			"user_agent":     r.UserAgent(),
		},
	}
	if status >= http.StatusInternalServerError {
		event.ErrorMessage = http.StatusText(status)
	}
	return event
}

// traceLevel 按响应状态码与耗时给出事件级别，与访问日志的级别判定保持一致。
func traceLevel(status int, cost time.Duration) string {
	switch {
	case status >= http.StatusInternalServerError:
		return "error"
	case status >= http.StatusBadRequest, cost >= slowRequestThreshold:
		return "warn"
	default:
		return "info"
	}
}

// moduleFromPath 从路径推导模块名：/api/users/{id} -> users，/status -> status。
func moduleFromPath(path string) string {
	module := strings.TrimPrefix(path, "/api/")
	module = strings.TrimPrefix(module, "/")
	if i := strings.Index(module, "/"); i >= 0 {
		module = module[:i]
	}
	if module == "" {
		return "http"
	}
	return module
}

// RequestIDFrom 取出本请求的 ID；请求未经 RequestLog 中间件时返回 "-"。
func RequestIDFrom(r *http.Request) string {
	if r == nil {
		return "-"
	}
	if id, ok := r.Context().Value(requestIDKey{}).(string); ok && id != "" {
		return id
	}
	return "-"
}

// Fields 返回一组通用请求字段，供各层日志复用。
// 返回的切片容量被截断，调用方 append 时必定复制，避免多处 append 互相覆盖。
func Fields(r *http.Request) []zap.Field {
	id := RequestIDFrom(r)
	fields := []zap.Field{
		zap.String("request_id", id),
	}
	if r != nil {
		fields = append(fields,
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
		)
	}
	return fields[:len(fields):len(fields)]
}

// requestIDFromHeader 优先沿用上游传来的请求 ID，没有则现场生成一个。
func requestIDFromHeader(r *http.Request) string {
	if r != nil {
		if id := r.Header.Get(RequestIDHeader); id != "" {
			return id
		}
	}
	return newRequestID()
}

// newRequestID 生成 12 字节随机数（24 个十六进制字符）作为请求 ID。
// 随机数不可用时退化为时间戳，保证日志里始终有个可串联的值。
func newRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// newSpanID 生成 8 字节随机数（16 个十六进制字符）作为 Span ID。
// span 只需要在一次请求内唯一，长度取 request ID 的一半即可。
func newSpanID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// responseRecorder 包住 ResponseWriter，记录实际写出的状态码与字节数。
type responseRecorder struct {
	http.ResponseWriter
	status      int  // 响应状态码，未显式调用 WriteHeader 时视为 200
	bytes       int  // 响应体字节数
	wroteHeader bool // 是否已经写过响应头，重复调用只记录第一次
}

// WriteHeader 记录状态码后透传给底层 ResponseWriter。
func (rec *responseRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status = code
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(code)
}

// Write 统计响应体大小后透传给底层 ResponseWriter。
func (rec *responseRecorder) Write(b []byte) (int, error) {
	rec.wroteHeader = true
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// Flush 在底层 writer 支持时透传 flush，避免影响流式响应。
func (rec *responseRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
