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

-- -------------------------------------------
-- 测试数据
-- -------------------------------------------
INSERT INTO `sensitive_words` (`text`, `category`, `level`, `status`, `tag`) VALUES
-- 涉政类 (politics)
('颠覆政权',     'politics', 4, 1, '政治安全'),
('分裂国家',     'politics', 4, 1, '政治安全'),
('游行示威',     'politics', 3, 1, '集会'),
('反政府',       'politics', 3, 1, ''),
('政治敏感',     'politics', 2, 1, ''),

-- 色情类 (porn)
('色情视频',     'porn', 4, 1, '色情'),
('裸聊',         'porn', 4, 1, '色情'),
('约炮',         'porn', 3, 1, '低俗'),
('成人网站',     'porn', 3, 1, ''),
('性交易',       'porn', 4, 1, '违法'),

-- 广告类 (ad)
('加微信',       'ad', 1, 1, '引流'),
('免费领取',     'ad', 1, 1, '营销'),
('日赚万元',     'ad', 2, 1, '诈骗引流'),
('兼职刷单',     'ad', 3, 1, '诈骗'),
('代开发票',     'ad', 3, 1, '违法广告'),

-- 暴力类 (violence)
('杀人',         'violence', 4, 1, ''),
('砍人',         'violence', 3, 1, ''),
('炸弹制作',     'violence', 4, 1, '危险信息'),
('枪支买卖',     'violence', 4, 1, '违法'),
('自杀方法',     'violence', 4, 1, '危险信息'),

-- 辱骂类 (abuse)
('傻逼',         'abuse', 2, 1, '脏话'),
('滚蛋',         'abuse', 1, 1, '脏话'),
('废物',         'abuse', 1, 1, '侮辱'),
('去死',         'abuse', 2, 1, '恶意'),
('人渣',         'abuse', 2, 1, '侮辱'),

-- 自定义类 (custom) - 含已禁用的词条
('竞品公司名',   'custom', 1, 1, '品牌保护'),
('内部代号',     'custom', 1, 0, '内部'),
('测试敏感词',   'custom', 1, 0, '测试')
;
