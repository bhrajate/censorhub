-- CensorHub 数据库初始化脚本
-- 数据库: MySQL 8.0+
-- 字符集: utf8mb4

-- 创建数据库（如不存在）
CREATE DATABASE IF NOT EXISTS `censorhub`
    DEFAULT CHARACTER SET utf8mb4
    DEFAULT COLLATE utf8mb4_unicode_ci;

USE `censorhub`;

-- -------------------------------------------
-- 敏感词表
-- -------------------------------------------
CREATE TABLE IF NOT EXISTS `sensitive_words` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    `text`       VARCHAR(255)    NOT NULL                COMMENT '敏感词文本',
    `category`   VARCHAR(50)     NOT NULL                COMMENT '分类: politics|porn|ad|violence|abuse|custom',
    `level`      TINYINT         NOT NULL DEFAULT 1      COMMENT '风险等级: 1-低 2-中 3-高 4-严重',
    `status`     TINYINT         NOT NULL DEFAULT 1      COMMENT '状态: 0-禁用 1-启用',
    `tag`        VARCHAR(100)    NOT NULL DEFAULT ''      COMMENT '自定义标签',
    `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT '创建时间',
    `updated_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3) COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_text` (`text`),
    KEY `idx_category` (`category`),
    KEY `idx_status` (`status`),
    KEY `idx_tag` (`tag`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='敏感词表';
