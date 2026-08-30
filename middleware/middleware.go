// Package middleware 提供 HTTP 请求级别的调试能力：请求 ID 注入与访问日志。
//
// 中间件以 http.HandlerFunc 为单位实现，可直接注册到 http.ServeMux。
// 请求 ID 会写入响应头 X-Request-ID 并放进 context，各层日志带上它即可按一次调用串联。
//
// HTTP 根 span 同样在这里创建：数据库层由 otelgorm 自动埋点、业务函数由 Span 埋点，
// 它们都从 context 里取父 span，因此会自动挂到这个根 span 之下。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// RequestIDHeader 请求 ID 的 HTTP 头名称。上游传了就沿用，方便跨服务串联同一次调用。
const RequestIDHeader = "X-Request-ID"

// slowRequestThreshold 请求耗时超过该值时按慢请求处理，用 Warn 级别输出。
const slowRequestThreshold = 500 * time.Millisecond

// skipTracePaths 这些路径不生成 trace span：
//   - /health 是探活接口，外部通常每 10s 一次，上报会把采集端刷爆；
//   - /swagger/ 是静态文档；
//   - /api/traces/report 是上报入口，它自身产生的事件会再次进入缓冲形成自反馈。
var skipTracePaths = []string{"/health", "/swagger/", "/api/traces/report"}

// requestIDKey 是请求 ID 在 context 中的私有键类型，避免与其它包的键冲突。
type requestIDKey struct{}

// RequestLog 为请求注入 ID、创建 HTTP 根 span，并在进入/离开处理器时打印调试与访问日志。
func RequestLog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := requestIDFromHeader(r)
		w.Header().Set(RequestIDHeader, id)
		// 放进 context 后，后续各层都能用 RequestIDFrom 取出同一个 ID。
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))

		start := time.Now()
		path := r.URL.Path
		query := r.URL.RawQuery

		logger.Debug("请求开始处理",
			zap.String("request_id", id),
			zap.String("method", r.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("remote_addr", r.RemoteAddr),
			zap.Int64("content_length", r.ContentLength),
			zap.String("user_agent", r.UserAgent()),
		)

		// 创建根 span 并把带 span 的 context 写回请求，
		// 这样下游的 SQL 与函数 span 才能挂到它下面。
		var span oteltrace.Span
		if trace.IsEnabled() && !shouldSkipTrace(path) {
			r = r.WithContext(startHTTPSpan(r, id, &span))
		}

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)

		cost := time.Since(start)
		fields := []zap.Field{
			zap.String("request_id", id),
			zap.String("method", r.Method),
			zap.String("path", path),
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

		// end 事件里要带上状态码与响应大小，所以必须在响应写出之后再结束 span。
		if span != nil {
			trace.EndHTTPSpan(span, rec.status, rec.bytes)
		}
	}
}

// startHTTPSpan 创建 HTTP 根 span，返回携带它的 context，并通过 out 回传 span 供结束时调用。
func startHTTPSpan(r *http.Request, requestID string, out *oteltrace.Span) context.Context {
	ctx, span := trace.StartHTTPSpan(r.Context(), spanName(r), trace.HTTPRequestInfo{
		Method:     r.Method,
		Target:     r.URL.Path,
		Route:      r.Pattern,
		Query:      r.URL.RawQuery,
		RemoteAddr: r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  requestID,
	})
	*out = span
	return ctx
}

// spanName 优先用路由模板（形如 "GET /api/users/{id}"）作为 span 名。
// 直接用 URL 路径会让每个不同的 id 都产生一种 span 名，属于高基数，会把采集端的聚合统计撑爆。
func spanName(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return r.Method + " " + r.URL.Path
}

// shouldSkipTrace 判断该路径是否需要生成 trace span。
func shouldSkipTrace(path string) bool {
	for _, prefix := range skipTracePaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
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
