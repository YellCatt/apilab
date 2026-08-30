// Package trace 基于 OpenTelemetry 提供链路追踪能力。
//
// 埋点策略是"能自动的绝不手写"：
//   - HTTP 层：middleware.RequestLog 为每个请求创建根 span；
//   - 数据库层：otelgorm 插件自动为每条 SQL 创建子 span，repository 里不需要任何埋点代码；
//   - 业务函数：middleware.Span 一行手动埋点，用来补齐 service 层的函数级耗时。
//
// span 不走 OTLP，而是由 eventExporter 转成一个 span 两条 model.TraceEvent（start/end），
// 交给既有的 service.TraceService 缓冲并批量转发。
// 因此采集端的协议与字段完全不变，改造只发生在产生 span 的这一侧。
package trace

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// instrumentationName 是本服务 tracer 的名称，出现在 span 的 instrumentation 元数据里。
const instrumentationName = "github.com/YellCatt/apilab"

// slowSpanThreshold 耗时超过该值的 span 按 warn 处理，与访问日志的慢请求判定保持一致。
const slowSpanThreshold = 500 * time.Millisecond

// 自定义 span 属性：补上 OTel 语义约定里没有、但采集端一直在用的字段。
var (
	// AttrRequestID 把应用层的 X-Request-ID 挂到 span 上，便于与访问日志互查。
	AttrRequestID = attribute.Key("apilab.request_id")
	// AttrHTTPQuery URL 查询串。不拼进 span 名是为了避免高基数。
	AttrHTTPQuery = attribute.Key("apilab.http.query")
	// AttrRemoteAddr 客户端地址。
	AttrRemoteAddr = attribute.Key("apilab.remote_addr")
	// AttrUserAgent 客户端 UA。
	AttrUserAgent = attribute.Key("apilab.user_agent")
	// AttrResponseBytes 响应体字节数。
	AttrResponseBytes = attribute.Key("apilab.http.response_bytes")
)

// 语义约定里的属性键。这里直接用字符串而不用 semconv 常量，
// 免得 semconv 每次升级都要跟着改一遍版本化包名。
const (
	keyHTTPMethod  = attribute.Key("http.method")
	keyHTTPRoute   = attribute.Key("http.route")
	keyHTTPTarget  = attribute.Key("http.target")
	keyHTTPStatus  = attribute.Key("http.status_code")
	keyDBSystem    = attribute.Key("db.system")
	keyDBStatement = attribute.Key("db.statement")
)

// Reporter 上报 trace 事件的抽象，由 service.TraceService 实现。
// 接口定义在本包可避免 trace 反向依赖 service 包。
type Reporter interface {
	Report(events []model.TraceEvent)
}

// Options 链路追踪初始化参数。
type Options struct {
	// ServiceName 服务名，写入 resource，采集端据此区分服务。
	ServiceName string
	// Reporter 事件上报器；为 nil 时整条链路追踪能力关闭（span 变成 no-op，零开销）。
	Reporter Reporter
	// BatchTimeout span 攒够多久导出一次。它只负责把 span 转成事件，
	// 真正的批量发送仍由 service.TraceService 按其 flush_interval 控制。
	BatchTimeout time.Duration
	// MaxQueueSize 导出队列容量。满了会丢弃 span，流量大的场景需要调高。
	MaxQueueSize int
}

// 全局状态。provider 供 Shutdown 使用，reporter 供 IsEnabled 判断。
// Init/shutdown 写、IsEnabled 读，加锁是为了热加载或并发测试时不出现 data race。
var (
	stateMu  sync.RWMutex
	provider *sdktrace.TracerProvider
	reporter Reporter
)

// Init 初始化全局 TracerProvider。
//
// 返回的函数用于程序退出前把队列里剩余的 span 全部导出。
// reporter 为 nil 时不注册任何 provider，otel.Tracer 会返回 no-op 实现，
// 各埋点处无需判空即可安全调用。
func Init(opts Options) (func(), error) {
	if opts.Reporter == nil {
		logger.Debug("未配置 Reporter，链路追踪保持关闭")
		return func() {}, nil
	}

	name := opts.ServiceName
	if name == "" {
		name = "apilab"
	}
	batchTimeout := opts.BatchTimeout
	if batchTimeout <= 0 {
		batchTimeout = time.Second
	}
	maxQueueSize := opts.MaxQueueSize
	if maxQueueSize <= 0 {
		maxQueueSize = 4096
	}

	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", name)))
	if err != nil {
		return func() {}, err
	}

	p := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(
			&eventExporter{reporter: opts.Reporter},
			sdktrace.WithBatchTimeout(batchTimeout),
			sdktrace.WithMaxQueueSize(maxQueueSize),
		)),
	)
	stateMu.Lock()
	provider = p
	reporter = opts.Reporter
	stateMu.Unlock()

	otel.SetTracerProvider(p)
	// 启用标准传播：上游带 traceparent 时沿用其 trace_id，实现跨服务串联。
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Debug("链路追踪已启用",
		zap.String("service_name", name),
		zap.Duration("batch_timeout", batchTimeout),
		zap.Int("max_queue_size", maxQueueSize),
	)
	return shutdown, nil
}

// IsEnabled 判断链路追踪是否已启用。未启用时不应创建 span。
func IsEnabled() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return reporter != nil
}

// shutdown 关闭 TracerProvider，把队列里剩余的 span 导出。
// 必须在上报器关闭之前调用，否则最后一批 span 会来不及转成事件。
func shutdown() {
	stateMu.Lock()
	p := provider
	provider = nil
	reporter = nil
	stateMu.Unlock()
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		logger.Error("关闭 TracerProvider 失败", zap.Error(err))
	}
}

// eventExporter 把 OTel span 转成 model.TraceEvent 交给 Reporter。
// 它实现了 sdktrace.SpanExporter 接口。
type eventExporter struct {
	reporter Reporter
}

// ExportSpans 把一批 span 转成事件上报。
// 每个 span 展开成 start、end 两条，与改造前的数据格式保持一致，采集端无需改动。
func (e *eventExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	// 先按 trace 收集根 span 的 URL：子 span（SQL、业务函数）自身没有 URL 信息，
	// 但没有它采集端就无法把这条链路归到具体接口，所以由根 span 向下继承。
	urls := make(map[string]string, len(spans))
	for _, s := range spans {
		if u := httpURL(s, attribute.NewSet(s.Attributes()...)); u != "" {
			urls[s.SpanContext().TraceID().String()] = u
		}
	}

	events := make([]model.TraceEvent, 0, len(spans)*2)
	for _, s := range spans {
		url := urls[s.SpanContext().TraceID().String()]
		events = append(events, newEvent(s, "start", url), newEvent(s, "end", url))
	}

	logger.Debug("导出 span 批次", zap.Int("spans", len(spans)), zap.Int("events", len(events)))
	e.reporter.Report(events)
	return nil
}

// Shutdown 导出器本身的关闭逻辑，资源已由 provider 统一释放。
func (e *eventExporter) Shutdown(context.Context) error {
	return nil
}

// newEvent 把一个 span 转成一条 start 或 end 事件。
// url 为空时退回 span 自身的 URL（HTTP 根 span 自带 http.target/route），子 span 通常为空。
func newEvent(s sdktrace.ReadOnlySpan, event, url string) model.TraceEvent {
	cost := s.EndTime().Sub(s.StartTime())
	ts := s.StartTime()
	if event == "end" {
		ts = s.EndTime()
	}
	// 属性集合只构造一次，后面几个判定函数都复用它。
	set := attribute.NewSet(s.Attributes()...)

	if url == "" {
		url = httpURL(s, set)
	}
	module := rawModule(s, set)
	te := model.TraceEvent{
		TraceID:   s.SpanContext().TraceID().String(),
		SpanID:    s.SpanContext().SpanID().String(),
		Timestamp: ts,
		Level:     spanLevel(s, set, cost),
		Module:    module,
		Event:     event,
		Message:   spanMessage(s, set),
		Params:    map[string]interface{}{},
		URL:       url,
	}
	for _, kv := range s.Attributes() {
		te.Params[string(kv.Key)] = attributeValue(kv.Value)
	}

	// 根 span 的父 ID 是无效值（全 0），转成空串避免采集端显示成一串 0。
	if pid := s.Parent().SpanID(); pid.IsValid() {
		te.ParentSpanID = pid.String()
	}

	if event == "end" && cost > 0 {
		te.Params["cost_ms"] = math.Round(float64(cost.Microseconds())/100) / 10
	}

	switch {
	case s.Status().Code == codes.Error && s.Status().Description != "":
		te.ErrorMessage = s.Status().Description
	case statusCodeOf(set) >= http.StatusInternalServerError:
		te.ErrorMessage = http.StatusText(statusCodeOf(set))
	}
	return te
}

// attributeValue 把 OTel 属性值转成可 JSON 序列化的普通值。
func attributeValue(v attribute.Value) interface{} {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.SLICE:
		return v.AsSlice()
	default:
		// STRING 以及其它不常见类型统一取字符串表示，保证一定能序列化。
		return v.Emit()
	}
}

// spanMessage 生成事件描述：SQL 用语句、HTTP 用方法+路径，其余用 span 名。
func spanMessage(s sdktrace.ReadOnlySpan, set attribute.Set) string {
	if stmt := attrString(set, keyDBStatement); stmt != "" {
		return "sql: " + stmt
	}
	if method := attrString(set, keyHTTPMethod); method != "" {
		msg := method + " " + targetOf(s, set)
		if sc := statusCodeOf(set); sc > 0 {
			msg += " " + strconv.Itoa(sc)
		}
		return msg
	}
	return s.Name()
}

// targetOf 取 HTTP 请求路径，优先用具体 URL，没有则退回路由模板。
func targetOf(s sdktrace.ReadOnlySpan, set attribute.Set) string {
	if t := attrString(set, keyHTTPTarget); t != "" {
		return t
	}
	if r := attrString(set, keyHTTPRoute); r != "" {
		return r
	}
	return s.Name()
}

// httpURL 取 HTTP span 所属的请求路径，非 HTTP span 返回空串。
// 只有根 span 带 http.method / http.route，因此它同时也是整条链路的 URL 来源。
func httpURL(s sdktrace.ReadOnlySpan, set attribute.Set) string {
	if attrString(set, keyHTTPMethod) == "" && attrString(set, keyHTTPRoute) == "" {
		return ""
	}
	return targetOf(s, set)
}

// attrString 取属性的字符串值，属性不存在或不是字符串时返回空串。
func attrString(set attribute.Set, k attribute.Key) string {
	v, ok := set.Value(k)
	if !ok {
		return ""
	}
	return v.AsString()
}

// rawModule 判断 span 自身的模块归属，按代码分层返回标准名称：
//   - sql：数据库操作（otelgorm 自动埋点）
//   - controller：HTTP 根 span（middleware.RequestLog 创建）
//   - service：业务函数 span（middleware.Span 埋点）
//   - repository：数据访问层（如果有独立 repository 函数埋点）
//   - app：兜底（无法识别时）
func rawModule(s sdktrace.ReadOnlySpan, set attribute.Set) string {
	if attrString(set, keyDBStatement) != "" || attrString(set, keyDBSystem) != "" {
		return "sql"
	}
	if attrString(set, keyHTTPMethod) != "" || attrString(set, keyHTTPRoute) != "" {
		return "controller"
	}
	return moduleFromName(s.Name())
}

// moduleFromName 从 span 名推导模块：
// "service.userService.GetAllUsers" -> "service"，
// "repository.userRepo.FindByID" -> "repository"，
// "gorm.Query" -> "gorm"。
func moduleFromName(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	if i := strings.Index(name, ":"); i > 0 {
		return name[:i]
	}
	return "app"
}

// spanLevel 按 span 状态、HTTP 状态码与耗时给出事件级别。
func spanLevel(s sdktrace.ReadOnlySpan, set attribute.Set, cost time.Duration) string {
	sc := statusCodeOf(set)
	switch {
	case s.Status().Code == codes.Error:
		return "error"
	case sc >= http.StatusInternalServerError:
		return "error"
	case sc >= http.StatusBadRequest:
		return "warn"
	case cost >= slowSpanThreshold:
		return "warn"
	default:
		return "info"
	}
}

// statusCodeOf 读取 span 上的 HTTP 状态码，不存在时返回 0。
func statusCodeOf(set attribute.Set) int {
	v, ok := set.Value(keyHTTPStatus)
	if !ok || v.Type() != attribute.INT64 {
		return 0
	}
	return int(v.AsInt64())
}

// Tracer 返回本服务的 tracer，供 middleware 创建 span。
// 未初始化时返回 no-op 实现，调用方无需判空。
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// HTTPRequestInfo 创建 HTTP 根 span 所需的请求侧信息。
type HTTPRequestInfo struct {
	Method     string // 请求方法
	Target     string // 实际请求的 URL 路径
	Route      string // 路由模板，如 "GET /api/users/{id}"
	Query      string // URL 查询串
	RemoteAddr string // 客户端地址
	UserAgent  string // 客户端 UA
	RequestID  string // 应用层请求 ID
}

// StartHTTPSpan 创建 HTTP 根 span 并写入请求侧属性。
// 请求期间产生的 SQL span 与函数 span 都会自动挂到它下面。
func StartHTTPSpan(ctx context.Context, name string, info HTTPRequestInfo) (context.Context, trace.Span) {
	ctx, span := Tracer().Start(ctx, name)
	span.SetAttributes(
		keyHTTPMethod.String(info.Method),
		keyHTTPTarget.String(info.Target),
		keyHTTPRoute.String(info.Route),
		AttrHTTPQuery.String(info.Query),
		AttrRemoteAddr.String(info.RemoteAddr),
		AttrUserAgent.String(info.UserAgent),
		AttrRequestID.String(info.RequestID),
	)
	return ctx, span
}

// EndHTTPSpan 记录响应状态码与字节数后结束 span。
// 5xx 会被标记为 error，采集端可直接按状态筛出失败链路。
func EndHTTPSpan(span trace.Span, status, bytes int) {
	if span == nil {
		return
	}
	span.SetAttributes(
		keyHTTPStatus.Int(status),
		AttrResponseBytes.Int(bytes),
	)
	if status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(status))
	}
	span.End()
}
