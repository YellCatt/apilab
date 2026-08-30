// Package repository 定义了数据访问层，封装数据库操作。
package repository

import (
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// UserRepository 用户数据访问接口，定义了用户的增删改查操作。
type UserRepository interface {
	Create(user *model.User) error
	GetByID(id uint) (*model.User, error)
	GetAll() ([]model.User, error)
	Update(user *model.User) error
	Delete(id uint) error
}

// userRepository UserRepository 的默认实现，基于 GORM 操作 SQLite。
type userRepository struct {
	db *gorm.DB // GORM 数据库连接实例
}

// NewUserRepository 创建一个新的用户数据访问实例。
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// Create 创建新用户记录。
func (r *userRepository) Create(user *model.User) error {
	start := time.Now()
	tx := r.db.Create(user)
	logger.Debug("db: insert user",
		zap.String("name", user.Name),
		zap.Int("age", user.Age),
		zap.Duration("cost", time.Since(start)),
		zap.Int64("rows", tx.RowsAffected),
		zap.Error(tx.Error),
	)
	return tx.Error
}

// GetByID 根据 ID 查询用户，返回 nil,nil 表示未找到记录。
func (r *userRepository) GetByID(id uint) (*model.User, error) {
	var user model.User
	start := time.Now()
	tx := r.db.First(&user, id)
	logger.Debug("db: select user by id",
		zap.Uint("id", id),
		zap.Duration("cost", time.Since(start)),
		zap.Bool("found", tx.Error == nil),
		zap.Error(dbError(tx.Error)),
	)
	if tx.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if tx.Error != nil {
		return nil, tx.Error
	}
	return &user, nil
}

// GetAll 查询所有用户记录。
func (r *userRepository) GetAll() ([]model.User, error) {
	var users []model.User
	start := time.Now()
	tx := r.db.Find(&users)
	logger.Debug("db: select all users",
		zap.Duration("cost", time.Since(start)),
		zap.Int("rows", len(users)),
		zap.Error(tx.Error),
	)
	return users, tx.Error
}

// Update 更新指定用户的所有字段。
func (r *userRepository) Update(user *model.User) error {
	start := time.Now()
	tx := r.db.Save(user)
	logger.Debug("db: update user",
		zap.Uint("id", user.ID),
		zap.String("name", user.Name),
		zap.Int("age", user.Age),
		zap.Duration("cost", time.Since(start)),
		zap.Int64("rows", tx.RowsAffected),
		zap.Error(tx.Error),
	)
	return tx.Error
}

// Delete 根据 ID 删除用户记录。
func (r *userRepository) Delete(id uint) error {
	start := time.Now()
	tx := r.db.Delete(&model.User{}, id)
	logger.Debug("db: delete user",
		zap.Uint("id", id),
		zap.Duration("cost", time.Since(start)),
		zap.Int64("rows", tx.RowsAffected),
		zap.Error(tx.Error),
	)
	return tx.Error
}

// dbError 过滤掉“记录不存在”这种预期内的错误，避免调试日志里出现噪音。
func dbError(err error) error {
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	return err
}
