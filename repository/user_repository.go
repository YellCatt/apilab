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
	ctx, end := middleware.StartSpan(ctx, "repository", "repo.user.create", "插入用户记录", map[string]interface{}{"name": user.Name})
	defer end(map[string]interface{}{"rows_affected": 1})

	start := time.Now()
	sqlCtx, endSQL := middleware.StartSpan(ctx, "sql", "sql.insert", "INSERT INTO users", nil)
	tx := r.db.Create(user)
	endSQL(map[string]interface{}{"rows_affected": tx.RowsAffected})
	_ = sqlCtx // 避免未使用变量提示

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
	ctx, end := middleware.StartSpan(ctx, "repository", "repo.user.get_by_id", "按 ID 查询用户", map[string]interface{}{"id": id})
	defer end()

	var user model.User
	start := time.Now()
	sqlCtx, endSQL := middleware.StartSpan(ctx, "sql", "sql.select", "SELECT * FROM users WHERE id = ?", map[string]interface{}{"id": id})
	tx := r.db.First(&user, id)
	found := tx.Error == nil
	endSQL(map[string]interface{}{"found": found})
	_ = sqlCtx

	logger.Debug("数据库：按 ID 查询用户",
		zap.Uint("id", id),
		zap.Duration("cost", time.Since(start)),
		zap.Bool("found", found),
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
	ctx, end := middleware.StartSpan(ctx, "repository", "repo.user.get_all", "查询全部用户", nil)
	defer end()

	var users []model.User
	start := time.Now()
	sqlCtx, endSQL := middleware.StartSpan(ctx, "sql", "sql.select", "SELECT * FROM users", nil)
	tx := r.db.Find(&users)
	endSQL(map[string]interface{}{"rows": len(users)})
	_ = sqlCtx

	logger.Debug("数据库：查询全部用户",
		zap.Duration("cost", time.Since(start)),
		zap.Int("rows", len(users)),
		zap.Error(tx.Error),
	)
	return users, tx.Error
}

// Update 更新指定用户的所有字段。
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	ctx, end := middleware.StartSpan(ctx, "repository", "repo.user.update", "更新用户记录", map[string]interface{}{"id": user.ID})
	defer end(map[string]interface{}{"rows_affected": 1})

	start := time.Now()
	sqlCtx, endSQL := middleware.StartSpan(ctx, "sql", "sql.update", "UPDATE users", map[string]interface{}{"id": user.ID})
	tx := r.db.Save(user)
	endSQL(map[string]interface{}{"rows_affected": tx.RowsAffected})
	_ = sqlCtx

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
	ctx, end := middleware.StartSpan(ctx, "repository", "repo.user.delete", "删除用户记录", map[string]interface{}{"id": id})
	defer end(map[string]interface{}{"rows_affected": 1})

	start := time.Now()
	sqlCtx, endSQL := middleware.StartSpan(ctx, "sql", "sql.delete", "DELETE FROM users WHERE id = ?", map[string]interface{}{"id": id})
	tx := r.db.Delete(&model.User{}, id)
	endSQL(map[string]interface{}{"rows_affected": tx.RowsAffected})
	_ = sqlCtx

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
