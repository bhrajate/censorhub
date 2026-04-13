package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/bhrajate/censorhub/internal/application/service"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
	"github.com/bhrajate/censorhub/internal/infrastructure/algorithm"
	"github.com/bhrajate/censorhub/internal/infrastructure/cache"
	"github.com/bhrajate/censorhub/internal/infrastructure/config"
	"github.com/bhrajate/censorhub/internal/infrastructure/database"
	"github.com/bhrajate/censorhub/internal/infrastructure/mq"
	mysqlrepo "github.com/bhrajate/censorhub/internal/infrastructure/persistence/mysql"
	"github.com/bhrajate/censorhub/internal/infrastructure/trace"
	grpcserver "github.com/bhrajate/censorhub/internal/interfaces/grpc"
	httpserver "github.com/bhrajate/censorhub/internal/interfaces/http"
	"github.com/bhrajate/censorhub/internal/interfaces/http/handler"
	"github.com/bhrajate/censorhub/internal/interfaces/middleware"
	"github.com/bhrajate/censorhub/pkg/logger"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "config file path")
	env := flag.String("env", "", "environment: dev/test/staging/production (overrides APP_ENV)")
	flag.Parse()

	// 1. 加载配置（基础配置 + 环境配置覆盖 + 环境变量覆盖）
	cfg, err := config.LoadWithEnv(*configPath, *env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志
	log, err := logger.NewLogger(logger.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
		Output: cfg.Log.Output,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()

	log.Info("Starting CensorHub",
		zap.String("env", string(cfg.Env)),
		zap.String("http_addr", cfg.Server.HTTP.Addr),
		zap.String("grpc_addr", cfg.Server.GRPC.Addr),
	)

	// 3. 初始化 Tracer
	tracerCleanup, err := trace.InitTracer(cfg, log)
	if err != nil {
		log.Warn("failed to init tracer, continuing without tracing", zap.Error(err))
		tracerCleanup = func() {}
	}
	defer tracerCleanup()

	// 4. 初始化 MySQL
	db, dbCleanup, err := database.NewMySQL(cfg, log)
	if err != nil {
		log.Fatal("failed to connect MySQL", zap.Error(err))
	}
	defer dbCleanup()

	// 自动迁移
	if err := mysqlrepo.AutoMigrate(db, log); err != nil {
		log.Fatal("failed to migrate database", zap.Error(err))
	}

	// 5. 初始化 Redis
	rdb, redisCleanup, err := database.NewRedis(cfg, log)
	if err != nil {
		log.Fatal("failed to connect Redis", zap.Error(err))
	}
	defer redisCleanup()

	// 6. 构建依赖
	// 仓储
	wordRepo := mysqlrepo.NewWordRepository(db)

	// 过滤引擎
	engine := algorithm.NewACFilterEngine()

	// 生命周期 context，用于控制后台协程退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 缓存
	localCache := cache.NewLocalCache(ctx, cfg.Cache.LocalTTL)
	redisCache := cache.NewRedisCache(rdb, cfg.Cache.RedisTTL)
	multiCache := cache.NewMultiLevelCache(localCache, redisCache)

	// PubSub
	pubsub := mq.NewRedisPubSub(rdb, log)

	// 过滤策略
	strategies := map[valueobject.FilterStrategyType]valueobject.FilterStrategy{
		valueobject.StrategyDetect:    algorithm.NewDetectStrategy(),
		valueobject.StrategyReplace:   algorithm.NewReplaceStrategy(),
		valueobject.StrategyHighlight: algorithm.NewHighlightStrategy(),
	}

	// 应用服务
	filterAppService := service.NewFilterAppService(engine, strategies, multiCache, log)
	wordAppService := service.NewWordAppService(wordRepo, engine, multiCache, pubsub, log)

	// 7. 初始化引擎（从 DB 加载词条）
	if err := wordAppService.InitEngine(context.Background()); err != nil {
		log.Fatal("failed to init engine", zap.Error(err))
	}

	// 8. 订阅热更新通知
	pubsub.SubscribeWordUpdate(ctx, func() {
		words, err := wordRepo.FindAllActive(context.Background())
		if err != nil {
			log.Error("failed to load words for rebuild", zap.Error(err))
			return
		}
		if err := engine.Rebuild(words); err != nil {
			log.Error("failed to rebuild engine", zap.Error(err))
			return
		}
		log.Info("engine rebuilt via PubSub", zap.Int("word_count", engine.WordCount()))
	})

	// 9. HTTP handler
	filterHandler := handler.NewFilterHandler(filterAppService)
	wordHandler := handler.NewWordHandler(wordAppService)
	healthHandler := handler.NewHealthHandler(db, rdb, filterAppService)
	mw := middleware.NewMiddleware(cfg, log)

	router := httpserver.NewRouter(filterHandler, wordHandler, healthHandler, mw)

	// 10. 启动 gRPC server
	grpcSrv := grpc.NewServer()
	censorGRPC := grpcserver.NewCensorServiceServer(filterAppService)
	censorGRPC.RegisterServer(grpcSrv)

	go func() {
		lis, err := net.Listen("tcp", cfg.Server.GRPC.Addr)
		if err != nil {
			log.Fatal("failed to listen gRPC", zap.Error(err))
		}
		log.Info("gRPC server started", zap.String("addr", cfg.Server.GRPC.Addr))
		if err := grpcSrv.Serve(lis); err != nil {
			log.Fatal("gRPC server failed", zap.Error(err))
		}
	}()

	// 11. 启动 HTTP server
	httpSrv := &http.Server{
		Addr:         cfg.Server.HTTP.Addr,
		Handler:      router,
		ReadTimeout:  cfg.Server.HTTP.ReadTimeout,
		WriteTimeout: cfg.Server.HTTP.WriteTimeout,
		IdleTimeout:  cfg.Server.HTTP.IdleTimeout,
	}

	go func() {
		log.Info("HTTP server started", zap.String("addr", cfg.Server.HTTP.Addr))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("HTTP server failed", zap.Error(err))
		}
	}()

	// 12. 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("Shutting down server...", zap.String("signal", sig.String()))

	cancel() // 停止 PubSub 订阅

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	grpcSrv.GracefulStop()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP server forced shutdown", zap.Error(err))
	}

	log.Info("Server exited gracefully")
}
