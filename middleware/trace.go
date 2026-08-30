// Package middleware 提供 trace 上下文传递与 span 辅助函数。
//
// TraceContext 会注入到请求的 context 中，controller/service/repository 各层
// 通过 StartSpan 创建子 span 并自动上报 start/end 事件，采集端从而得到完整的
// 调用链与分步耗时。
package middleware

import (
	"context"
	"math"
	"time"

	"github.com/YellCatt/apilab/model"
)

// traceContextKey 是 TraceContext 在 context 中的私有键类型。
type traceContextKey struct{}

// TraceContext 保存一次请求的根 trace 信息以及上报器。
type TraceContext struct {
	TraceID    string
	SpanID     string
	Reporter   Reporter
	StartTime  time.Time
	RemoteAddr string
	UserAgent  string
}

// WithTraceContext 把 TraceContext 写入 context。
func WithTraceContext(ctx context.Context, tc *TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// TraceContextFrom 从 context 中取出 TraceContext；不存在时返回 nil。
func TraceContextFrom(ctx context.Context) *TraceContext {
	if ctx == nil {
		return nil
	}
	if tc, ok := ctx.Value(traceContextKey{}).(*TraceContext); ok {
		return tc
	}
	return nil
}

// EndSpanFunc 用于结束一个 span；调用时会自动上报 end 事件。
type EndSpanFunc func(params ...map[string]interface{})

// StartSpan 创建一个子 span，立即上报 start 事件，并返回结束函数。
// 调用方在业务逻辑结束后执行返回的函数，即可上报 end 事件。
//
// 示例：
//
//	ctx, end := middleware.StartSpan(ctx, "service", "user.get_all", "查询全部用户", nil)
//	defer end()
//	users, err := s.repo.GetAll(ctx)
func StartSpan(ctx context.Context, module, event, message string, params map[string]interface{}) (context.Context, EndSpanFunc) {
	tc := TraceContextFrom(ctx)
	if tc == nil || tc.Reporter == nil {
		// 没有 trace 上下文时返回一个空结束函数，避免调用方写 if 判断。
		return ctx, func(...map[string]interface{}) {}
	}

	spanID := newSpanID()
	start := time.Now()

	startParams := map[string]interface{}{}
	if params != nil {
		for k, v := range params {
			startParams[k] = v
		}
	}

	startEvent := model.TraceEvent{
		TraceID:      tc.TraceID,
		SpanID:       spanID,
		ParentSpanID: tc.SpanID,
		Timestamp:    start,
		Level:        "info",
		Module:       module,
		Event:        event,
		Message:      message,
		Params:       startParams,
	}
	go tc.Reporter.Report([]model.TraceEvent{startEvent})

	// 子 span 的上下文：当前 span 变成后续调用的父 span。
	childCtx := WithTraceContext(ctx, &TraceContext{
		TraceID:    tc.TraceID,
		SpanID:     spanID,
		Reporter:   tc.Reporter,
		StartTime:  start,
		RemoteAddr: tc.RemoteAddr,
		UserAgent:  tc.UserAgent,
	})

	return childCtx, func(extra ...map[string]interface{}) {
		cost := time.Since(start)
		endParams := map[string]interface{}{
			"cost_ms": math.Round(float64(cost.Microseconds())/100) / 10,
		}
		for _, p := range extra {
			for k, v := range p {
				endParams[k] = v
			}
		}

		endEvent := model.TraceEvent{
			TraceID:      tc.TraceID,
			SpanID:       spanID,
			ParentSpanID: tc.SpanID,
			Timestamp:    time.Now(),
			Level:        "info",
			Module:       module,
			Event:        event,
			Message:      message,
			Params:       endParams,
		}
		go tc.Reporter.Report([]model.TraceEvent{endEvent})
	}
}

// ReportEvent 上报一个瞬时事件（无 start/end 语义），用于标记某个时间点发生的事情。
func ReportEvent(ctx context.Context, module, event, message string, params map[string]interface{}) {
	tc := TraceContextFrom(ctx)
	if tc == nil || tc.Reporter == nil {
		return
	}
	go tc.Reporter.Report([]model.TraceEvent{{
		TraceID:   tc.TraceID,
		SpanID:    newSpanID(),
		Timestamp: time.Now(),
		Level:     "info",
		Module:    module,
		Event:     event,
		Message:   message,
		Params:    params,
	}})
}
