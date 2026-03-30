package database

import (
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/bhrajate/censorhub/internal/infrastructure/config"
)

// NewMySQL 创建 MySQL 连接
func NewMySQL(cfg *config.Config, logger *zap.Logger) (*gorm.DB, func(), error) {
	logLevel := gormlogger.Warn
	if cfg.Log.Level == "debug" {
		logLevel = gormlogger.Info
	}

	db, err := gorm.Open(mysql.Open(cfg.Database.DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	if cfg.Database.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)
	} else {
		sqlDB.SetConnMaxLifetime(time.Hour)
	}

	logger.Info("MySQL connected", zap.String("dsn", maskDSN(cfg.Database.DSN)))

	cleanup := func() {
		if err := sqlDB.Close(); err != nil {
			logger.Error("failed to close MySQL", zap.Error(err))
		}
	}

	return db, cleanup, nil
}

// maskDSN 隐藏 DSN 中的密码
func maskDSN(dsn string) string {
	// 简单处理：只显示前 20 个字符
	if len(dsn) > 20 {
		return dsn[:20] + "..."
	}
	return dsn
}
