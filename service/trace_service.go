package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.uber.org/zap"
)

// 发送失败的原因分类，直接体现在日志的 reason 字段里，便于检索与告警。
const (
	reasonConnectRefused = "connect_refused" // 采集端未启动或地址/端口写错
	reasonTimeout        = "timeout"         // 采集端响应过慢或网络不通
	reasonDNS            = "dns_failed"      // 采集端域名无法解析
	reasonNetwork        = "network_error"   // 其它网络层错误
	reasonRejected       = "rejected"        // 采集端连通但返回了 4xx/5xx
	reasonEncodeFailed   = "encode_failed"   // 事件序列化失败（几乎不会触发）
	reasonUnknown        = "unknown"
)

// errorLogEvery 连续失败时，每累计这么多次补一条 Error，其余记 Warn，避免刷屏。
const errorLogEvery = 10

// maxErrorBody 采集端错误响应体最多记录这么多字节，防止日志被大响应撑爆。
const maxErrorBody = 512

// eventLevelError 事件级别里的错误档，与 trace 包 newEvent 写入的取值一致。
// 该级别的事件即使在 info 配置下也要逐条可见。
const eventLevelError = "error"

// TraceService Trace 事件上报业务逻辑接口。
type TraceService interface {
	// Report 接收一批 trace 事件：写入本地日志并入缓冲，攒够一批或到定时刷新时批量发给采集端。
	Report(events []model.TraceEvent)
	// Stop 停止后台刷新协程，并刷新剩余缓冲数据。
	Stop()
}

// traceService TraceService 的默认实现，内部维护一个内存缓冲队列。
type traceService struct {
	mu     sync.Mutex
	buffer []model.TraceEvent // 待上报的缓冲队列

	// statMu 保护下方的失败统计。flush 可能被 Report（HTTP 协程）与 flushLoop（后台协程）
	// 并发调用，所以统计不能复用保护 buffer 的 mu（那会把 HTTP 发送挡在锁里）。
	statMu   sync.Mutex
	failures int    // 连续失败次数，成功一次即清零
	dropped  uint64 // 累计因发送失败而丢弃的事件数

	collectorURL string        // 采集端接收地址
	serviceName  string        // 本服务名，事件未带 serviceName 时用它兜底
	batchSize    int           // 批量上报阈值
	client       *http.Client  // HTTP 客户端
	stopCh       chan struct{} // 停止信号
	doneCh       chan struct{} // 后台协程退出确认

	// wg 跟踪 Report 丢到后台的异步 flush，Stop 时统一等待，避免进程退出掐断发送。
	wg      sync.WaitGroup
	stopped bool // Stop 是否已调用（受 mu 保护），用于避免 Stop 之后又 Add 到 wg

	received uint64 // 累计写入缓冲的事件数（受 mu 保护）
}

// NewTraceService 创建一个新的 Trace 上报服务，并启动后台定时刷新协程。
// serviceName 是本服务名：事件未带 service_name 时用它兜底，也用于链路追踪的 service.name。
func NewTraceService(collectorURL, serviceName string, batchSize int, flushInterval time.Duration) TraceService {
	s := &traceService{
		collectorURL: collectorURL,
		serviceName:  serviceName,
		batchSize:    batchSize,
		client:       &http.Client{Timeout: 10 * time.Second},
		buffer:       make([]model.TraceEvent, 0, batchSize),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	logger.Debug("Trace 上报服务启动中",
		zap.String("collector_url", collectorURL),
		zap.String("service_name", serviceName),
		zap.Int("batch_size", batchSize),
		zap.Duration("flush_interval", flushInterval),
	)
	go s.flushLoop(flushInterval)
	return s
}

// Report 接收 trace 事件：先写本地日志，再追加到缓冲。
// 一旦缓冲数量达到 batchSize，立即取出若干批交给后台协程发送给采集端。
func (s *traceService) Report(events []model.TraceEvent) {
	if len(events) == 0 {
		return
	}

	// 事件没带服务名时用本服务配置的 service_name 兜底；
	// 外部上报方自带 service_name 时以它为准（例如网关转发别的服务的事件）。
	for i := range events {
		if events[i].ServiceName == "" {
			events[i].ServiceName = s.serviceName
		}
	}

	// 本地留痕，保证即便采集端不可用也有可查记录。
	logEvents(events)

	s.mu.Lock()
	s.buffer = append(s.buffer, events...)
	s.received += uint64(len(events))
	buffered := len(s.buffer)
	totalReceived := s.received
	// 攒够 batchSize 立即取出发送，不足部分由定时协程兜底
	var batches [][]model.TraceEvent
	for len(s.buffer) >= s.batchSize {
		batches = append(batches, s.buffer[:s.batchSize])
		s.buffer = append([]model.TraceEvent(nil), s.buffer[s.batchSize:]...)
	}
	remaining := len(s.buffer)
	stopped := s.stopped
	if !stopped {
		s.wg.Add(len(batches))
	}
	s.mu.Unlock()

	// 用 Info 级别：入缓冲是整条上报链路的起点，必须无条件可见，
	// 否则缓冲长期为空时无从判断是"没事件产生"还是"事件被吞了"。
	logger.Info("Trace 事件已写入缓冲",
		zap.Int("received", len(events)),
		zap.Uint64("total_received", totalReceived),
		zap.Int("buffered", buffered),
		zap.Int("remaining", remaining),
		zap.Int("batches", len(batches)),
		zap.Int("batch_size", s.batchSize),
	)

	if stopped {
		// 已停止：同步发送，否则 goroutine 会脱离 Stop 的等待范围、被进程退出掐断。
		for _, batch := range batches {
			s.flush(batch)
		}
		return
	}
	// 异步发送：flush 内部是带 10 秒超时的 HTTP 请求，采集端不通时会把调用方
	// （HTTP handler / OTel 导出协程）整个卡住，并发一高就耗尽连接。
	// 丢到后台后 Report 永不阻塞，失败照样由 reportFailure 记录。
	for _, batch := range batches {
		go func(b []model.TraceEvent) {
			defer s.wg.Done()
			s.flush(b)
		}(batch)
	}
}

// logEvents 为本批事件生成本地日志。
//
// 这里刻意不逐条打印：一个请求会展开成 6-22 条事件（每个 span 起止各一条），
// 逐条 Debug 在 debug 配置下等于把访问日志再放大一个数量级，且全是同步写盘。
// 改成"一条汇总 + 错误事件逐条"：正常情况下一批只有汇总那一行，
// 真正需要关注的错误事件又不会被汇总淹掉。
func logEvents(events []model.TraceEvent) {
	// 错误事件走 Error 级，不受 debug 开关影响。
	logErrorEvents(events)

	if !logger.DebugEnabled() {
		return
	}
	var errs, warns int
	traces := make(map[string]struct{}, 8)
	modules := make(map[string]int, 4)
	services := make(map[string]struct{}, 2)
	for _, e := range events {
		switch e.Level {
		case eventLevelError:
			errs++
		case "warn":
			warns++
		}
		traces[e.TraceID] = struct{}{}
		modules[e.Module]++
		services[e.ServiceName] = struct{}{}
	}
	logger.Debug("收到 Trace 事件",
		zap.Int("count", len(events)),
		zap.Int("traces", len(traces)),
		zap.Any("modules", modules),
		zap.Strings("services", keysOf(services)),
		zap.Int("warn", warns),
		zap.Int("error", errs),
		// 抽样一条，便于确认字段格式；完整内容以采集端为准。
		zap.String("sample", events[0].Message),
	)
}

// keysOf 取集合的键，转成 zap.Strings 可接受的切片，避免 map[struct{}] 序列化成空对象。
func keysOf(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	return keys
}

// logErrorEvents 把错误级别的事件逐条记 Error 日志。
// 这类事件数量极少但必须无条件可见，混在汇总里等于看不见。
func logErrorEvents(events []model.TraceEvent) {
	for _, e := range events {
		if e.Level != eventLevelError && e.ErrorMessage == "" {
			continue
		}
		logger.Error("Trace 事件标记为错误",
			zap.String("service_name", e.ServiceName),
			zap.String("trace_id", e.TraceID),
			zap.String("span_id", e.SpanID),
			zap.String("module", e.Module),
			zap.String("event", e.Event),
			zap.String("message", e.Message),
			zap.String("url", e.URL),
			zap.String("error_message", e.ErrorMessage),
			zap.Any("params", e.Params),
		)
	}
}

// flushLoop 定时协程：每隔 interval 刷新一次缓冲（若 buffer 非空）；收到停止信号时做最后一次刷新。
func (s *traceService) flushLoop(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.flushBuffer()
		case <-s.stopCh:
			s.flushBuffer()
			close(s.doneCh)
			return
		}
	}
}

// flushBuffer 取出缓冲中的全部事件并批量发送给采集端。
func (s *traceService) flushBuffer() {
	s.mu.Lock()
	batch := s.buffer
	s.buffer = nil
	totalReceived := s.received
	s.mu.Unlock()

	if len(batch) == 0 {
		// 带上累计入缓冲条数：看到 total_received 一直是 0 就能确认
		// 根本没有事件产生（而非缓冲被提前清空），直接去查上报侧。
		logger.Debug("缓冲为空，跳过刷新", zap.Uint64("total_received", totalReceived))
		return
	}
	logger.Debug("定时器触发批量刷新", zap.Int("count", len(batch)), zap.String("url", s.collectorURL))
	s.flush(batch)
}

// batchURL 推断这批事件共属的请求 URL：全部一致才提升到请求体顶层。
// 缓冲是跨请求合并的，混合批次返回空串，交由采集端按事件自身的 url 归类。
func batchURL(events []model.TraceEvent) string {
	if len(events) == 0 {
		return ""
	}
	url := events[0].URL
	for _, e := range events[1:] {
		if e.URL != url {
			return ""
		}
	}
	return url
}

// batchServiceName 与 batchURL 同理：整批来自同一服务时才提升到请求体顶层，
// 混合批次返回空串，采集端按事件自身的 service_name 归类。
func batchServiceName(events []model.TraceEvent) string {
	if len(events) == 0 {
		return ""
	}
	name := events[0].ServiceName
	for _, e := range events[1:] {
		if e.ServiceName != name {
			return ""
		}
	}
	return name
}

// flush 将一批事件 POST 到采集端。
//
// 发送失败会记录错误日志（含失败原因分类与连续失败次数）并丢弃该批，避免阻塞调用方；
// 本地日志已在 Report 阶段落盘（汇总 + 错误逐条），丢弃只影响转发，不影响本地留痕。
func (s *traceService) flush(events []model.TraceEvent) {
	if len(events) == 0 {
		return
	}

	start := time.Now()
	// 事件自身的 service_name 与 url 由上报端/span 转换时补齐；
	// 这里把整批一致的字段提升到请求体顶层，采集端既能按顶层归类，也能按事件逐条归类。
	body, err := json.Marshal(model.TraceReportRequest{
		ServiceName: batchServiceName(events),
		URL:         batchURL(events),
		Events:      events,
	})
	if err != nil {
		s.reportFailure(reasonEncodeFailed, err, len(events), 0, "", time.Since(start))
		return
	}
	logger.Debug("Trace 批次序列化完成",
		zap.Int("count", len(events)), zap.Int("bytes", len(body)), zap.Duration("cost", time.Since(start)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.collectorURL, bytes.NewReader(body))
	if err != nil {
		s.reportFailure(reasonUnknown, err, len(events), 0, "", time.Since(start))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// 最常见的情况：8086 上没起服务 / 地址端口写错。这里明确区分原因并给出排查提示。
		s.reportFailure(classifySendError(err), err, len(events), 0, "", time.Since(start))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		// 采集端是连通的，但拒绝或处理失败：把响应体带上，否则无从判断是哪边的问题。
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		s.reportFailure(reasonRejected, nil, len(events), resp.StatusCode, string(snippet), time.Since(start))
		return
	}

	logger.Debug("已收到采集端响应",
		zap.Int("status", resp.StatusCode), zap.Int("count", len(events)),
		zap.Duration("cost", time.Since(start)))

	recoveredAfter := s.reportSuccess(len(events))
	fields := []zap.Field{
		zap.Int("count", len(events)),
		zap.String("url", s.collectorURL),
		zap.Duration("cost", time.Since(start)),
	}
	if recoveredAfter > 0 {
		// 之前失败过、现在通了，单独提示一条，免得让人以为一直在丢数据。
		fields = append(fields, zap.Int("recovered_after_failures", recoveredAfter))
	}
	logger.Info("Trace 事件已批量上报至采集端", fields...)
}

// reportFailure 记录一次发送失败：累计失败次数与丢弃条数，并按频率选择日志级别。
// 首次失败与每第 errorLogEvery 次失败用 Error（带堆栈），中间用 Warn，兼顾醒目与不刷屏。
func (s *traceService) reportFailure(reason string, err error, count, status int, respBody string, cost time.Duration) {
	s.statMu.Lock()
	s.failures++
	s.dropped += uint64(count)
	failures, dropped := s.failures, s.dropped
	s.statMu.Unlock()

	fields := []zap.Field{
		zap.String("url", s.collectorURL),
		zap.String("reason", reason),
		zap.String("hint", failureHint(reason)),
		zap.Int("count", count),
		zap.Int("consecutive_failures", failures),
		zap.Uint64("total_dropped", dropped),
		zap.Duration("cost", cost),
	}
	if status > 0 {
		fields = append(fields, zap.Int("status", status))
	}
	if respBody != "" {
		fields = append(fields, zap.String("response_body", respBody))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}

	if failures == 1 || failures%errorLogEvery == 0 {
		logger.Error("采集端不可达，Trace 事件已丢弃", fields...)
		return
	}
	logger.Warn("采集端不可达，Trace 事件已丢弃", fields...)
}

// reportSuccess 记录一次成功发送，返回此前连续失败的次数（0 表示之前一直是通的）。
func (s *traceService) reportSuccess(count int) int {
	s.statMu.Lock()
	previous := s.failures
	s.failures = 0
	s.statMu.Unlock()
	return previous
}

// failureStats 返回累计丢弃条数与当前连续失败次数，供 Stop 时汇总。
func (s *traceService) failureStats() (dropped uint64, failures int) {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	return s.dropped, s.failures
}

// classifySendError 把发送阶段的网络错误归类，日志里能一眼看出是“没起来”还是“太慢”。
func classifySendError(err error) string {
	if err == nil {
		return reasonUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return reasonTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return reasonTimeout
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return reasonDNS
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// dial 阶段失败基本等同于目标端口没服务：连不上或被直接拒绝。
		if opErr.Op == "dial" {
			return reasonConnectRefused
		}
		return reasonNetwork
	}
	return reasonUnknown
}

// failureHint 针对不同失败原因给出可执行的排查建议。
func failureHint(reason string) string {
	switch reason {
	case reasonConnectRefused:
		return "检查采集端是否已启动、url 的主机名与端口是否正确"
	case reasonTimeout:
		return "采集端响应超时，检查其负载或调大 collector 超时时间"
	case reasonDNS:
		return "采集端域名无法解析，检查 url 拼写与 DNS 配置"
	case reasonNetwork:
		return "网络层错误，检查本机与采集端之间的连通性"
	case reasonRejected:
		return "采集端已连通但拒绝了这批数据，核对接口路径与事件字段"
	case reasonEncodeFailed:
		return "事件序列化失败，检查事件字段是否为可序列化类型"
	default:
		return "未知错误，请结合 error 字段排查"
	}
}

// Stop 停止定时刷新协程并刷新剩余缓冲，用于程序退出前的优雅关闭。
// 可重复调用，第二次及以后直接返回。
func (s *traceService) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	logger.Debug("Trace 上报服务停止中，正在刷新剩余缓冲")
	close(s.stopCh)
	<-s.doneCh
	// 等 Report 丢出去的异步 flush 收尾，否则进程一退这些请求就被掐断。
	s.wg.Wait()

	// 退出时汇总一次：进程活着时失败日志可能被淹没，这里保证最后一定能看到结论。
	if dropped, failures := s.failureStats(); dropped > 0 || failures > 0 {
		logger.Error("Trace 上报服务停止，存在未送达的事件",
			zap.String("url", s.collectorURL),
			zap.Uint64("total_dropped", dropped),
			zap.Int("consecutive_failures", failures),
			zap.String("hint", "这些事件只存在于本地日志，未送达采集端"),
		)
	} else {
		logger.Debug("Trace 上报服务已停止")
	}
}
