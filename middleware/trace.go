// 本文件提供函数级 span 埋点。
//
// span 的创建、父子关系与上报全部交给 OpenTelemetry，本包只保留一层薄封装，
// 让业务代码仍然是"一行 defer"的写法：
//
//	func (s *userService) GetAllUsers(ctx context.Context) ([]model.User, error) {
//		ctx, end := middleware.Span(ctx)
//		defer end()
//		// ... 业务逻辑
//	}
//
// 注意必须接收返回的 ctx：SQL span 由 otelgorm 从 ctx 里取父 span，
// 只有把新 ctx 继续往下传，SQL 才会挂在当前函数 span 之下。
package middleware

import (
	"context"
	"runtime"
	"strings"
	"sync"

	"github.com/YellCatt/apilab/trace"
)

// Span 开启一个 span，返回携带它的 context 与结束函数。
//
// span 名称自动取调用者函数名，父子关系自动取自 context 中已有的 span。
// 因此埋点只需要两行，且必须紧跟在函数开头。
// 链路追踪未启用时返回原 ctx 与空函数，调用方无需判空。
func Span(ctx context.Context) (context.Context, func()) {
	return SpanNamed(ctx, callerName(2))
}

// SpanNamed 与 Span 相同，但允许自定义 span 名称。
// 适合 SQL 这类没有独立函数的场景：
//
//	ctx, end := middleware.SpanNamed(ctx, "sql: SELECT * FROM users")
//	tx := r.db.WithContext(ctx).Find(&users)
//	end()
func SpanNamed(ctx context.Context, name string) (context.Context, func()) {
	if !trace.IsEnabled() {
		return ctx, func() {}
	}

	// once 保证重复调用 end 只结束一次，调用方多写一次 defer 也不会产生脏数据。
	var once sync.Once
	ctx, span := trace.Tracer().Start(ctx, name)
	return ctx, func() {
		// span.End 是变参函数，需要包一层才能交给 sync.Once。
		once.Do(func() { span.End() })
	}
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
