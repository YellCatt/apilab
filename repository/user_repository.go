// Package repository 定义了数据访问层，封装数据库操作。
package repository

import (
	"context"
	"time"

	"github.com/YellCatt/apilab/logger"
	"github.com/YellCatt/apilab/middleware"
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
	defer middleware.Span(ctx)()

	start := time.Now()
	tx := r.sqlExec(ctx, "sql: INSERT INTO users", func() *gorm.DB {
		return r.db.Create(user)
	})

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
	defer middleware.Span(ctx)()

	var user model.User
	start := time.Now()
	tx := r.sqlExec(ctx, "sql: SELECT * FROM users WHERE id = ?", func() *gorm.DB {
		return r.db.First(&user, id)
	})

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
	defer middleware.Span(ctx)()

	var users []model.User
	start := time.Now()
	tx := r.sqlExec(ctx, "sql: SELECT * FROM users", func() *gorm.DB {
		return r.db.Find(&users)
	})

	logger.Debug("数据库：查询全部用户",
		zap.Duration("cost", time.Since(start)),
		zap.Int("rows", len(users)),
		zap.Error(tx.Error),
	)
	return users, tx.Error
}

// Update 更新指定用户的所有字段。
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	defer middleware.Span(ctx)()

	start := time.Now()
	tx := r.sqlExec(ctx, "sql: UPDATE users", func() *gorm.DB {
		return r.db.Save(user)
	})

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
	defer middleware.Span(ctx)()

	start := time.Now()
	tx := r.sqlExec(ctx, "sql: DELETE FROM users WHERE id = ?", func() *gorm.DB {
		return r.db.Delete(&model.User{}, id)
	})

	logger.Debug("数据库：删除用户",
		zap.Uint("id", id),
		zap.Duration("cost", time.Since(start)),
		zap.Int64("rows", tx.RowsAffected),
		zap.Error(tx.Error),
	)
	return tx.Error
}

// sqlExec 执行一条 SQL，并把它包成一个独立的 span。
// 这样每个方法体里只剩一行调用，SQL 耗时与语句都能在采集端看到。
func (r *userRepository) sqlExec(ctx context.Context, name string, exec func() *gorm.DB) *gorm.DB {
	end := middleware.SpanNamed(ctx, name)
	tx := exec()
	end()
	return tx
}

// dbError 过滤掉“记录不存在”这种预期内的错误，避免调试日志里出现噪音。
func dbError(err error) error {
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	return err
}
