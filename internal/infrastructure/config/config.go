package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Env       Env             `mapstructure:"-"`
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Cache     CacheConfig     `mapstructure:"cache"`
	Log       LogConfig       `mapstructure:"log"`
	Trace     TraceConfig     `mapstructure:"trace"`
	RateLimit RateLimitConfig `mapstructure:"ratelimit"`
	Auth      AuthConfig      `mapstructure:"auth"`
}

type ServerConfig struct {
	HTTP HTTPConfig `mapstructure:"http"`
	GRPC GRPCConfig `mapstructure:"grpc"`
}

type HTTPConfig struct {
	Addr         string        `mapstructure:"addr"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type GRPCConfig struct {
	Addr string `mapstructure:"addr"`
}

type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"`
	DSN             string        `mapstructure:"dsn"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type CacheConfig struct {
	LocalTTL time.Duration `mapstructure:"local_ttl"`
	RedisTTL time.Duration `mapstructure:"redis_ttl"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

type TraceConfig struct {
	Endpoint   string  `mapstructure:"endpoint"`
	SampleRate float64 `mapstructure:"sample_rate"`
}

type RateLimitConfig struct {
	RequestsPerSecond int `mapstructure:"requests_per_second"`
	Burst             int `mapstructure:"burst"`
}

type AuthConfig struct {
	APIKeys []string `mapstructure:"api_keys"`
}

// Env 环境标识
type Env string

const (
	EnvDev        Env = "dev"
	EnvTest       Env = "test"
	EnvStaging    Env = "staging"
	EnvProduction Env = "production"
)

func (e Env) IsValid() bool {
	return e == EnvDev || e == EnvTest || e == EnvStaging || e == EnvProduction
}

func (e Env) IsProd() bool {
	return e == EnvProduction
}

// Load 加载配置文件
// 优先级：环境变量 > 环境配置文件 > 基础配置文件 > 默认值
func Load(path string) (*Config, error) {
	return LoadWithEnv(path, "")
}

// LoadWithEnv 根据环境加载配置
// 加载顺序：
//  1. 基础配置（configs/config.yaml）
//  2. 环境配置覆盖（configs/config.{env}.yaml）—— 仅覆盖有差异的字段
//  3. 环境变量覆盖（CENSORHUB_ 前缀，如 CENSORHUB_DATABASE_DSN）
func LoadWithEnv(path string, env string) (*Config, error) {
	// 从环境变量获取 env（优先级：参数 > 环境变量 APP_ENV > 默认 dev）
	if env == "" {
		env = os.Getenv("APP_ENV")
	}
	if env == "" {
		env = string(EnvDev)
	}

	v := viper.New()
	v.SetConfigType("yaml")

	// Step 1: 加载基础配置
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read base config: %w", err)
	}

	// Step 2: 加载环境配置覆盖
	envPath := envConfigPath(path, env)
	if _, err := os.Stat(envPath); err == nil {
		v.SetConfigFile(envPath)
		if err := v.MergeInConfig(); err != nil {
			return nil, fmt.Errorf("merge env config (%s): %w", envPath, err)
		}
	}

	// Step 3: 环境变量覆盖（CENSORHUB_ 前缀）
	v.SetEnvPrefix("CENSORHUB")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 绑定关键环境变量（使 viper 能识别嵌套 key）
	bindEnvKeys(v)

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.Env = Env(env)
	setDefaults(cfg)
	return cfg, nil
}

// envConfigPath 根据基础配置路径生成环境配置路径
// configs/config.yaml -> configs/config.dev.yaml
func envConfigPath(basePath string, env string) string {
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	return base + "." + env + ext
}

// bindEnvKeys 绑定环境变量到嵌套 key
func bindEnvKeys(v *viper.Viper) {
	keys := []string{
		"server.http.addr",
		"server.grpc.addr",
		"database.driver",
		"database.dsn",
		"database.max_open_conns",
		"database.max_idle_conns",
		"redis.addr",
		"redis.password",
		"redis.db",
		"redis.pool_size",
		"cache.local_ttl",
		"cache.redis_ttl",
		"log.level",
		"log.format",
		"log.output",
		"trace.endpoint",
		"trace.sample_rate",
		"ratelimit.requests_per_second",
		"ratelimit.burst",
	}
	for _, key := range keys {
		v.BindEnv(key)
	}
}

func setDefaults(cfg *Config) {
	if cfg.Server.HTTP.Addr == "" {
		cfg.Server.HTTP.Addr = ":8080"
	}
	if cfg.Server.GRPC.Addr == "" {
		cfg.Server.GRPC.Addr = ":9090"
	}
	if cfg.Server.HTTP.ReadTimeout == 0 {
		cfg.Server.HTTP.ReadTimeout = 10 * time.Second
	}
	if cfg.Server.HTTP.WriteTimeout == 0 {
		cfg.Server.HTTP.WriteTimeout = 10 * time.Second
	}
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = 100
	}
	if cfg.Database.MaxIdleConns == 0 {
		cfg.Database.MaxIdleConns = 10
	}
	if cfg.Cache.LocalTTL == 0 {
		cfg.Cache.LocalTTL = 5 * time.Minute
	}
	if cfg.Cache.RedisTTL == 0 {
		cfg.Cache.RedisTTL = 30 * time.Minute
	}
	if cfg.RateLimit.RequestsPerSecond == 0 {
		cfg.RateLimit.RequestsPerSecond = 1000
	}
	if cfg.RateLimit.Burst == 0 {
		cfg.RateLimit.Burst = 2000
	}
}
