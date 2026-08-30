// Package service 定义了业务逻辑层，协调 repository 与 controller 之间的交互。
package service

import (
	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"github.com/YellCatt/apilab/repository"
	"go.uber.org/zap"
)

// UserService 用户业务逻辑接口。
type UserService interface {
	CreateUser(req *model.CreateUserRequest) (*model.User, error)
	GetUserByID(id uint) (*model.User, error)
	GetAllUsers() ([]model.User, error)
	UpdateUser(id uint, req *model.UpdateUserRequest) (*model.User, error)
	DeleteUser(id uint) error
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
func (s *userService) CreateUser(req *model.CreateUserRequest) (*model.User, error) {
	logger.Debug("service: creating user", zap.String("name", req.Name), zap.Int("age", req.Age))

	user := &model.User{
		Name: req.Name,
		Age:  req.Age,
	}
	err := s.repo.Create(user)
	if err != nil {
		logger.Error("service: create user failed", zap.String("name", req.Name), zap.Error(err))
		return nil, err
	}
	logger.Debug("service: user created", zap.Uint("id", user.ID))
	return user, nil
}

// GetUserByID 根据 ID 查询用户。
func (s *userService) GetUserByID(id uint) (*model.User, error) {
	logger.Debug("service: getting user", zap.Uint("id", id))

	user, err := s.repo.GetByID(id)
	if err != nil {
		logger.Error("service: get user failed", zap.Uint("id", id), zap.Error(err))
		return nil, err
	}
	if user == nil {
		logger.Debug("service: user not found", zap.Uint("id", id))
		return nil, nil
	}
	return user, nil
}

// GetAllUsers 查询所有用户。
func (s *userService) GetAllUsers() ([]model.User, error) {
	logger.Debug("service: listing users")

	users, err := s.repo.GetAll()
	if err != nil {
		logger.Error("service: list users failed", zap.Error(err))
		return nil, err
	}
	logger.Debug("service: users listed", zap.Int("count", len(users)))
	return users, nil
}

// UpdateUser 更新指定用户，仅更新请求中非空的字段。
func (s *userService) UpdateUser(id uint, req *model.UpdateUserRequest) (*model.User, error) {
	logger.Debug("service: updating user",
		zap.Uint("id", id), zap.String("name", req.Name), zap.Int("age", req.Age))

	user, err := s.repo.GetByID(id)
	if err != nil {
		logger.Error("service: load user before update failed", zap.Uint("id", id), zap.Error(err))
		return nil, err
	}
	if user == nil {
		logger.Debug("service: user not found, skip update", zap.Uint("id", id))
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
	logger.Debug("service: update fields resolved",
		zap.Uint("id", id), zap.Strings("fields", applied),
		zap.String("old_name", oldName), zap.Int("old_age", oldAge))

	err = s.repo.Update(user)
	if err != nil {
		logger.Error("service: update user failed", zap.Uint("id", id), zap.Error(err))
		return nil, err
	}
	logger.Debug("service: user updated", zap.Uint("id", id), zap.Strings("fields", applied))
	return user, nil
}

// DeleteUser 根据 ID 删除用户。
func (s *userService) DeleteUser(id uint) error {
	logger.Debug("service: deleting user", zap.Uint("id", id))

	err := s.repo.Delete(id)
	if err != nil {
		logger.Error("service: delete user failed", zap.Uint("id", id), zap.Error(err))
		return err
	}
	logger.Debug("service: user deleted", zap.Uint("id", id))
	return nil
}
