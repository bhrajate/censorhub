package mysql

import (
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AutoMigrate 自动迁移数据库表结构
func AutoMigrate(db *gorm.DB, logger *zap.Logger) error {
	logger.Info("Running database migration...")
	if err := db.AutoMigrate(&SensitiveWordModel{}); err != nil {
		return err
	}
	logger.Info("Database migration completed")
	return nil
}
