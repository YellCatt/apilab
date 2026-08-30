package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.uber.org/zap"
)

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

	collectorURL string        // 采集端接收地址
	batchSize    int           // 批量上报阈值
	client       *http.Client  // HTTP 客户端
	stopCh       chan struct{} // 停止信号
	doneCh       chan struct{} // 后台协程退出确认
}

// NewTraceService 创建一个新的 Trace 上报服务，并启动后台定时刷新协程。
func NewTraceService(collectorURL string, batchSize int, flushInterval time.Duration) TraceService {
	s := &traceService{
		collectorURL: collectorURL,
		batchSize:    batchSize,
		client:       &http.Client{Timeout: 10 * time.Second},
		buffer:       make([]model.TraceEvent, 0, batchSize),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	logger.Debug("trace service starting",
		zap.String("collector_url", collectorURL),
		zap.Int("batch_size", batchSize),
		zap.Duration("flush_interval", flushInterval),
	)
	go s.flushLoop(flushInterval)
	return s
}

// Report 接收 trace 事件：先逐条写入本地日志，再追加到缓冲。
// 一旦缓冲数量达到 batchSize，立即取出一批发送给采集端。
func (s *traceService) Report(events []model.TraceEvent) {
	if len(events) == 0 {
		return
	}

	// 整合到本地日志，保证即便采集端不可用也有本地可查的记录
	for _, e := range events {
		logger.Info("trace event",
			zap.String("trace_id", e.TraceID),
			zap.String("span_id", e.SpanID),
			zap.String("parent_span_id", e.ParentSpanID),
			zap.Time("timestamp", e.Timestamp),
			zap.String("level", e.Level),
			zap.String("module", e.Module),
			zap.String("event", e.Event),
			zap.String("message", e.Message),
			zap.Any("params", e.Params),
			zap.String("error_message", e.ErrorMessage),
		)
	}

	s.mu.Lock()
	s.buffer = append(s.buffer, events...)
	buffered := len(s.buffer)
	// 攒够 batchSize 立即取出发送，不足部分由定时协程兜底
	var batches [][]model.TraceEvent
	for len(s.buffer) >= s.batchSize {
		batches = append(batches, s.buffer[:s.batchSize])
		s.buffer = append([]model.TraceEvent(nil), s.buffer[s.batchSize:]...)
	}
	remaining := len(s.buffer)
	s.mu.Unlock()

	logger.Debug("trace events buffered",
		zap.Int("received", len(events)),
		zap.Int("buffered", buffered),
		zap.Int("remaining", remaining),
		zap.Int("batches", len(batches)),
		zap.Int("batch_size", s.batchSize),
	)

	for _, batch := range batches {
		s.flush(batch)
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
	s.mu.Unlock()

	if len(batch) == 0 {
		logger.Debug("trace flush skipped, buffer empty")
		return
	}
	logger.Debug("trace flush triggered by timer", zap.Int("count", len(batch)), zap.String("url", s.collectorURL))
	s.flush(batch)
}

// flush 将一批事件 POST 到采集端。发送失败时记录错误日志并丢弃该批，避免阻塞调用方。
func (s *traceService) flush(events []model.TraceEvent) {
	if len(events) == 0 {
		return
	}

	start := time.Now()
	body, err := json.Marshal(model.TraceReportRequest{Events: events})
	if err != nil {
		logger.Error("failed to marshal trace events", zap.Error(err), zap.Int("count", len(events)))
		return
	}
	logger.Debug("trace batch encoded",
		zap.Int("count", len(events)), zap.Int("bytes", len(body)), zap.Duration("cost", time.Since(start)))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.collectorURL, bytes.NewReader(body))
	if err != nil {
		logger.Error("failed to build collector request", zap.Error(err), zap.Int("count", len(events)))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		logger.Error("failed to send trace events to collector",
			zap.Error(err), zap.Int("count", len(events)), zap.String("url", s.collectorURL))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		logger.Error("collector returned error status",
			zap.Int("status", resp.StatusCode), zap.Int("count", len(events)),
			zap.String("url", s.collectorURL), zap.Duration("cost", time.Since(start)))
		return
	}

	logger.Debug("collector response received",
		zap.Int("status", resp.StatusCode), zap.Int("count", len(events)),
		zap.Duration("cost", time.Since(start)))
	logger.Info("trace events flushed to collector",
		zap.Int("count", len(events)), zap.String("url", s.collectorURL), zap.Duration("cost", time.Since(start)))
}

// Stop 停止定时刷新协程并刷新剩余缓冲，用于程序退出前的优雅关闭。
func (s *traceService) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
	}
	logger.Debug("trace service stopping, flushing remaining buffer")
	close(s.stopCh)
	<-s.doneCh
	logger.Debug("trace service stopped")
}
