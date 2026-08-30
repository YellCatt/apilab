// Package service 定义了业务逻辑层，协调 repository 与 controller 之间的交互。
package service

import (
	"context"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/middleware"
	"github.com/YellCatt/apilab/model"
	"github.com/YellCatt/apilab/repository"
	"go.uber.org/zap"
)

// UserService 用户业务逻辑接口。
type UserService interface {
	CreateUser(ctx context.Context, req *model.CreateUserRequest) (*model.User, error)
	GetUserByID(ctx context.Context, id uint) (*model.User, error)
	GetAllUsers(ctx context.Context) ([]model.User, error)
	UpdateUser(ctx context.Context, id uint, req *model.UpdateUserRequest) (*model.User, error)
	DeleteUser(ctx context.Context, id uint) error
}

// userService UserService 的默认实现。
type userService struct {
	repo repository.UserRepository // 用户数据访问实例
}

// NewUserService 创建一个新的用户业务逻辑实例。
func NewUserService(repo repository.UserRepository) UserService {
	return &userService{repo: repo}
}

// CreateUser 创建新用户。
func (s *userService) CreateUser(ctx context.Context, req *model.CreateUserRequest) (*model.User, error) {
	ctx, end := middleware.StartSpan(ctx, "service", "user.create", "创建用户", nil)
	defer end(map[string]interface{}{"name": req.Name, "age": req.Age})

	logger.Debug("服务层：创建用户", zap.String("name", req.Name), zap.Int("age", req.Age))

	user := &model.User{
		Name: req.Name,
		Age:  req.Age,
	}
	err := s.repo.Create(ctx, user)
	if err != nil {
		logger.Error("服务层：创建用户失败", zap.String("name", req.Name), zap.Error(err))
		return nil, err
	}
	logger.Debug("服务层：用户创建成功", zap.Uint("id", user.ID))
	return user, nil
}

// GetUserByID 根据 ID 查询用户。
func (s *userService) GetUserByID(ctx context.Context, id uint) (*model.User, error) {
	ctx, end := middleware.StartSpan(ctx, "service", "user.get_by_id", "根据 ID 查询用户", map[string]interface{}{"id": id})
	defer end()

	logger.Debug("服务层：查询用户", zap.Uint("id", id))

	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		logger.Error("服务层：查询用户失败", zap.Uint("id", id), zap.Error(err))
		return nil, err
	}
	if user == nil {
		logger.Debug("服务层：用户不存在", zap.Uint("id", id))
		return nil, nil
	}
	return user, nil
}

// GetAllUsers 查询所有用户。
func (s *userService) GetAllUsers(ctx context.Context) ([]model.User, error) {
	ctx, end := middleware.StartSpan(ctx, "service", "user.get_all", "查询全部用户", nil)
	defer end()

	logger.Debug("服务层：查询用户列表")

	users, err := s.repo.GetAll(ctx)
	if err != nil {
		logger.Error("服务层：查询用户列表失败", zap.Error(err))
		return nil, err
	}
	logger.Debug("服务层：用户列表查询完成", zap.Int("count", len(users)))
	return users, nil
}

// UpdateUser 更新指定用户，仅更新请求中非空的字段。
func (s *userService) UpdateUser(ctx context.Context, id uint, req *model.UpdateUserRequest) (*model.User, error) {
	ctx, end := middleware.StartSpan(ctx, "service", "user.update", "更新用户", map[string]interface{}{"id": id})
	defer end(map[string]interface{}{"name": req.Name, "age": req.Age})

	logger.Debug("服务层：更新用户",
		zap.Uint("id", id), zap.String("name", req.Name), zap.Int("age", req.Age))

	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		logger.Error("服务层：更新前加载用户失败", zap.Uint("id", id), zap.Error(err))
		return nil, err
	}
	if user == nil {
		logger.Debug("服务层：用户不存在，跳过更新", zap.Uint("id", id))
		return nil, nil
	}

	// 记录将要写入的字段与原值，便于回溯“传了但没生效”或“值被覆盖”的情况。
	applied := make([]string, 0, 2)
	oldName, oldAge := user.Name, user.Age
	if req.Name != "" {
		user.Name = req.Name
		applied = append(applied, "name")
	}
	if req.Age != 0 {
		user.Age = req.Age
		applied = append(applied, "age")
	}
	logger.Debug("服务层：更新字段解析完成",
		zap.Uint("id", id), zap.Strings("fields", applied),
		zap.String("old_name", oldName), zap.Int("old_age", oldAge))

	err = s.repo.Update(ctx, user)
	if err != nil {
		logger.Error("服务层：更新用户失败", zap.Uint("id", id), zap.Error(err))
		return nil, err
	}
	logger.Debug("服务层：用户更新成功", zap.Uint("id", id), zap.Strings("fields", applied))
	return user, nil
}

// DeleteUser 根据 ID 删除用户。
func (s *userService) DeleteUser(ctx context.Context, id uint) error {
	ctx, end := middleware.StartSpan(ctx, "service", "user.delete", "删除用户", map[string]interface{}{"id": id})
	defer end()

	logger.Debug("服务层：删除用户", zap.Uint("id", id))

	err := s.repo.Delete(ctx, id)
	if err != nil {
		logger.Error("服务层：删除用户失败", zap.Uint("id", id), zap.Error(err))
		return err
	}
	logger.Debug("服务层：用户删除成功", zap.Uint("id", id))
	return nil
}
