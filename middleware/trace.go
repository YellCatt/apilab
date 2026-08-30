// Package middleware 提供 trace 上下文传递与自动 span 埋点。
//
// 设计目标：调用方只需要一行 defer middleware.Span(ctx)(), 其余全部自动完成：
//   - trace_id 随 context 自动向下传递；
//   - span 名称自动取调用者函数名；
//   - 父子关系自动取自当前 span 栈；
//   - 函数返回时 defer 自动上报 end 事件并计算耗时。
package middleware

import (
	"context"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/YellCatt/apilab/model"
)

// traceContextKey 是 TraceContext 在 context 中的私有键类型。
type traceContextKey struct{}

// TraceContext 保存一次请求的根 trace 信息与 span 栈。
//
// 它以指针形式存放在 context 中：Span 直接对栈做 push/pop，
// 因此调用方无需接收新的 context，也就省掉了层层赋值的样板代码。
type TraceContext struct {
	TraceID   string   // 链路 ID，一次请求内所有 span 共用
	Reporter  Reporter // 事件上报器
	StartTime time.Time

	mu    sync.Mutex
	spans []string // span ID 栈，栈顶即"当前 span"
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

// Span 开启一个 span 并返回它的结束函数。
//
// span 名称自动取调用者函数名，父子关系自动取自当前 span 栈。
// 因此埋点只需要一行，且必须紧跟在函数开头：
//
//	func (s *userService) GetAllUsers(ctx context.Context) ([]model.User, error) {
//		defer middleware.Span(ctx)()
//		// ... 业务逻辑
//	}
//
// 函数返回时 defer 触发，自动上报 end 事件并带上耗时。
// 没有 trace 上下文（如 reporter 为 nil、或路径被跳过）时返回一个空函数，调用方无需判空。
func Span(ctx context.Context) func() {
	return SpanNamed(ctx, callerName(2))
}

// SpanNamed 与 Span 相同，但允许自定义 span 名称。
// 适合 SQL 这类没有独立函数的场景：
//
//	end := middleware.SpanNamed(ctx, "sql: SELECT * FROM users")
//	tx := r.db.Find(&users)
//	end()
func SpanNamed(ctx context.Context, name string) func() {
	tc := TraceContextFrom(ctx)
	if tc == nil || tc.Reporter == nil {
		return func() {}
	}

	spanID := newSpanID()
	parent := tc.push(spanID) // 入栈的同时拿到父 span ID
	start := time.Now()
	module := moduleFromFunc(name)

	go tc.Reporter.Report([]model.TraceEvent{{
		TraceID:      tc.TraceID,
		SpanID:       spanID,
		ParentSpanID: parent,
		Timestamp:    start,
		Level:        "info",
		Module:       module,
		Event:        "start",
		Message:      name,
		Params:       map[string]interface{}{"name": name},
	}})

	// once 保证重复调用 end 只上报一次，调用方多写一次 defer 也不会产生脏数据。
	var once sync.Once
	return func() {
		once.Do(func() {
			cost := time.Since(start)
			tc.pop()
			go tc.Reporter.Report([]model.TraceEvent{{
				TraceID:      tc.TraceID,
				SpanID:       spanID,
				ParentSpanID: parent,
				Timestamp:    time.Now(),
				Level:        "info",
				Module:       module,
				Event:        "end",
				Message:      name,
				Params: map[string]interface{}{
					"name":    name,
					"cost_ms": math.Round(float64(cost.Microseconds())/100) / 10,
				},
			}})
		})
	}
}

// push 把 spanID 压栈，返回它应该认的父 span ID（即入栈前的栈顶，没有则为空）。
func (tc *TraceContext) push(spanID string) string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	parent := ""
	if n := len(tc.spans); n > 0 {
		parent = tc.spans[n-1]
	}
	tc.spans = append(tc.spans, spanID)
	return parent
}

// pop 弹出栈顶 span。
func (tc *TraceContext) pop() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if n := len(tc.spans); n > 0 {
		tc.spans = tc.spans[:n-1]
	}
}

// pushRoot 由中间件调用，把根 span 压栈，使 controller 的 parent 指向它。
func (tc *TraceContext) pushRoot(spanID string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.spans = append(tc.spans, spanID)
}

// callerName 取第 skip 层调用者的函数名，用于自动生成 span 名称。
// 形如 github.com/x/apilab/service.(*userService).GetAllUsers 会被精简成
// service.userService.GetAllUsers。
func callerName(skip int) string {
	pc, _, _, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "unknown"
	}
	name := fn.Name()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	// 去掉指针接收者的括号与星号：(service.(*userService).GetAllUsers -> service.userService.GetAllUsers)
	name = strings.ReplaceAll(name, "(*", "")
	name = strings.ReplaceAll(name, ")", "")
	return name
}

// moduleFromFunc 从 "service.userService.GetAllUsers" 中取首段作为模块名。
func moduleFromFunc(name string) string {
	if i := strings.Index(name, "."); i > 0 {
		return name[:i]
	}
	return "app"
}
