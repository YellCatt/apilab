// Package controller 定义了 HTTP 请求处理器（Controller 层），负责解析请求并调用 Service 层。
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
	fields := middleware.Fields(r)
	logger.Debug("create user request", fields...)

	var req model.CreateUserRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		logger.Warn("invalid create user payload", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logger.Debug("create user payload decoded",
		append(fields, zap.String("name", req.Name), zap.Int("age", req.Age))...)

	user, err := c.service.CreateUser(&req)
	if err != nil {
		logger.Error("failed to create user", append(fields, zap.String("name", req.Name), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(user); err != nil {
		logger.Error("failed to encode created user", append(fields, zap.Uint("id", user.ID), zap.Error(err))...)
		return
	}
	logger.Debug("user created", append(fields, zap.Uint("id", user.ID), zap.String("name", user.Name))...)
}

// GetUserByID 处理 GET /api/users/{id} 请求，根据 ID 查询用户。
func (c *UserController) GetUserByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fields := append(middleware.Fields(r), zap.String("id_param", idStr))
	logger.Debug("get user request", fields...)

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		logger.Warn("invalid user id", append(fields, zap.Error(err))...)
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	user, err := c.service.GetUserByID(uint(id))
	if err != nil {
		logger.Error("failed to get user", append(fields, zap.Uint64("id", id), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		logger.Debug("user not found", append(fields, zap.Uint64("id", id))...)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(user); err != nil {
		logger.Error("failed to encode user", append(fields, zap.Uint("id", user.ID), zap.Error(err))...)
		return
	}
	logger.Debug("user fetched", append(fields, zap.Uint("id", user.ID), zap.String("name", user.Name))...)
}

// GetAllUsers 处理 GET /api/users 请求，查询所有用户。
func (c *UserController) GetAllUsers(w http.ResponseWriter, r *http.Request) {
	fields := middleware.Fields(r)
	logger.Debug("list users request", fields...)

	users, err := c.service.GetAllUsers()
	if err != nil {
		logger.Error("failed to list users", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(users); err != nil {
		logger.Error("failed to encode user list", append(fields, zap.Int("count", len(users)), zap.Error(err))...)
		return
	}
	logger.Debug("users listed", append(fields, zap.Int("count", len(users)))...)
}

// UpdateUser 处理 PUT /api/users/{id} 请求，更新指定用户。
func (c *UserController) UpdateUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fields := append(middleware.Fields(r), zap.String("id_param", idStr))
	logger.Debug("update user request", fields...)

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		logger.Warn("invalid user id", append(fields, zap.Error(err))...)
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	var req model.UpdateUserRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		logger.Warn("invalid update user payload", append(fields, zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	logger.Debug("update user payload decoded",
		append(fields, zap.Uint64("id", id), zap.String("name", req.Name), zap.Int("age", req.Age))...)

	user, err := c.service.UpdateUser(uint(id), &req)
	if err != nil {
		logger.Error("failed to update user", append(fields, zap.Uint64("id", id), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		logger.Debug("user not found, nothing updated", append(fields, zap.Uint64("id", id))...)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(user); err != nil {
		logger.Error("failed to encode updated user", append(fields, zap.Uint("id", user.ID), zap.Error(err))...)
		return
	}
	logger.Debug("user updated", append(fields, zap.Uint("id", user.ID), zap.String("name", user.Name), zap.Int("age", user.Age))...)
}

// DeleteUser 处理 DELETE /api/users/{id} 请求，删除指定用户。
func (c *UserController) DeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	fields := append(middleware.Fields(r), zap.String("id_param", idStr))
	logger.Debug("delete user request", fields...)

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		logger.Warn("invalid user id", append(fields, zap.Error(err))...)
		http.Error(w, "invalid user ID", http.StatusBadRequest)
		return
	}

	err = c.service.DeleteUser(uint(id))
	if err != nil {
		logger.Error("failed to delete user", append(fields, zap.Uint64("id", id), zap.Error(err))...)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	logger.Debug("user deleted", append(fields, zap.Uint64("id", id))...)
}
