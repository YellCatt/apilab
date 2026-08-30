// Package repository 定义了数据访问层，封装数据库操作。
//
// SQL 的链路埋点由 otelgorm 插件自动完成，本层没有任何埋点代码。
// 唯一的要求是把 context 交给 GORM（WithContext）：插件据此确定父 span，
// 少了它 SQL span 会脱离当前调用链，变成一条孤立的根 span。
package repository

import (
	"context"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/model"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// UserRepository 用户数据访问接口，定义了用户的增删改查操作。
type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	GetByID(ctx context.Context, id uint) (*model.User, error)
	GetAll(ctx context.Context) ([]model.User, error)
	Update(ctx context.Context, user *model.User) error
	Delete(ctx context.Context, id uint) error
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
func (r *userRepository) Create(ctx context.Context, user *model.User) error {
	logger.Debug("数据库：准备新增用户",
		zap.String("name", user.Name),
		zap.Int("age", user.Age),
	)
	start := time.Now()
	tx := r.db.WithContext(ctx).Create(user)

	logger.Debug("数据库：新增用户",
		zap.String("name", user.Name),
		zap.Int("age", user.Age),
		zap.Duration("cost", time.Since(start)),
		zap.Int64("rows", tx.RowsAffected),
		zap.Error(tx.Error),
	)
	return tx.Error
}

// GetByID 根据 ID 查询用户，返回 nil,nil 表示未找到记录。
func (r *userRepository) GetByID(ctx context.Context, id uint) (*model.User, error) {
	var user model.User
	logger.Debug("数据库：准备按 ID 查询用户", zap.Uint("id", id))
	start := time.Now()
	tx := r.db.WithContext(ctx).First(&user, id)

	logger.Debug("数据库：按 ID 查询用户",
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
func (r *userRepository) GetAll(ctx context.Context) ([]model.User, error) {
	var users []model.User
	logger.Debug("数据库：准备查询全部用户")
	start := time.Now()
	tx := r.db.WithContext(ctx).Find(&users)

	logger.Debug("数据库：查询全部用户",
		zap.Duration("cost", time.Since(start)),
		zap.Int("rows", len(users)),
		zap.Error(tx.Error),
	)
	return users, tx.Error
}

// Update 更新指定用户的所有字段。
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	logger.Debug("数据库：准备更新用户",
		zap.Uint("id", user.ID),
		zap.String("name", user.Name),
		zap.Int("age", user.Age),
	)
	start := time.Now()
	tx := r.db.WithContext(ctx).Save(user)

	logger.Debug("数据库：更新用户",
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
func (r *userRepository) Delete(ctx context.Context, id uint) error {
	logger.Debug("数据库：准备删除用户", zap.Uint("id", id))
	start := time.Now()
	tx := r.db.WithContext(ctx).Delete(&model.User{}, id)

	logger.Debug("数据库：删除用户",
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
