// Package controller 定义了 HTTP 请求处理器（Controller 层），负责解析请求并调用 Service 层。
//
// HTTP 层的 span 由 middleware.RequestLog 统一创建，本层不再单独埋点；
// handler 只需把 context 原样传给 Service，下游的 span 便会自动挂到这个根 span 之下。
package controller

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/middleware"
	"github.com/YellCatt/apilab/model"
	"github.com/YellCatt/apilab/service"
	"go.uber.org/zap"
)

// UserController 用户相关的 HTTP 请求处理器。
type UserController struct {
	service service.UserService // 用户业务逻辑实例
}

// NewUserController 创建一个新的用户控制器实例。
func NewUserController(service service.UserService) *UserController {
	return &UserController{service: service}
}

// CreateUser 处理 POST /api/users 请求，创建新用户。
func (c *UserController) CreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	fields := middleware.Fields(r)
	logger.Debug("收到创建用户请求", fields...)

	var req model.CreateUserRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		logger.Warn("创建用户的请求体无效", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logger.Debug("创建用户请求体解析完成",
		append(fields, zap.String("name", req.Name), zap.Int("age", req.Age))...)

	user, err := c.service.CreateUser(ctx, &req)
	if err != nil {
		logger.Error("创建用户失败", append(fields, zap.String("name", req.Name), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(user); err != nil {
		logger.Error("序列化创建用户响应失败", append(fields, zap.Uint("id", user.ID), zap.Error(err))...)
		return
	}
	logger.Debug("用户创建成功", append(fields, zap.Uint("id", user.ID), zap.String("name", user.Name))...)
}

// GetUserByID 处理 GET /api/users/{id} 请求，根据 ID 查询用户。
func (c *UserController) GetUserByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := r.PathValue("id")
	fields := append(middleware.Fields(r), zap.String("id_param", idStr))
	logger.Debug("收到查询用户请求", fields...)

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		logger.Warn("用户 ID 无效", append(fields, zap.Error(err))...)
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	user, err := c.service.GetUserByID(ctx, uint(id))
	if err != nil {
		logger.Error("查询用户失败", append(fields, zap.Uint64("id", id), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		logger.Debug("用户不存在", append(fields, zap.Uint64("id", id))...)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(user); err != nil {
		logger.Error("序列化用户响应失败", append(fields, zap.Uint("id", user.ID), zap.Error(err))...)
		return
	}
	logger.Debug("用户查询成功", append(fields, zap.Uint("id", user.ID), zap.String("name", user.Name))...)
}

// GetAllUsers 处理 GET /api/users 请求，查询所有用户。
func (c *UserController) GetAllUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	fields := middleware.Fields(r)
	logger.Debug("收到查询用户列表请求", fields...)

	users, err := c.service.GetAllUsers(ctx)
	if err != nil {
		logger.Error("查询用户列表失败", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(users); err != nil {
		logger.Error("序列化用户列表响应失败", append(fields, zap.Int("count", len(users)), zap.Error(err))...)
		return
	}
	logger.Debug("用户列表查询成功", append(fields, zap.Int("count", len(users)))...)
}

// UpdateUser 处理 PUT /api/users/{id} 请求，更新指定用户。
func (c *UserController) UpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := r.PathValue("id")
	fields := append(middleware.Fields(r), zap.String("id_param", idStr))
	logger.Debug("收到更新用户请求", fields...)

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		logger.Warn("用户 ID 无效", append(fields, zap.Error(err))...)
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	var req model.UpdateUserRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		logger.Warn("更新用户的请求体无效", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logger.Debug("更新用户请求体解析完成",
		append(fields, zap.Uint64("id", id), zap.String("name", req.Name), zap.Int("age", req.Age))...)

	user, err := c.service.UpdateUser(ctx, uint(id), &req)
	if err != nil {
		logger.Error("更新用户失败", append(fields, zap.Uint64("id", id), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		logger.Debug("用户不存在，未执行更新", append(fields, zap.Uint64("id", id))...)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(user); err != nil {
		logger.Error("序列化更新用户响应失败", append(fields, zap.Uint("id", user.ID), zap.Error(err))...)
		return
	}
	logger.Debug("用户更新成功", append(fields, zap.Uint("id", user.ID), zap.String("name", user.Name), zap.Int("age", user.Age))...)
}

// DeleteUser 处理 DELETE /api/users/{id} 请求，删除指定用户。
func (c *UserController) DeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := r.PathValue("id")
	fields := append(middleware.Fields(r), zap.String("id_param", idStr))
	logger.Debug("收到删除用户请求", fields...)

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		logger.Warn("用户 ID 无效", append(fields, zap.Error(err))...)
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	err = c.service.DeleteUser(ctx, uint(id))
	if err != nil {
		logger.Error("删除用户失败", append(fields, zap.Uint64("id", id), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	logger.Debug("用户删除成功", append(fields, zap.Uint64("id", id))...)
}
