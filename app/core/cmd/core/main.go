package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/server"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/worker"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/bootstrap"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/database"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
)

var Version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("core-service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var confPath string
	flag.StringVar(&confPath, "conf", "./configs/core.yaml", "config file")
	flag.Parse()

	bc, err := bootstrap.Load(confPath)
	if err != nil {
		return err
	}

	id, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}
	logger := bootstrap.NewLogger("tiehu.core", id)
	if err := data.ValidateVisionGRPCClientConfig(bc.GetVisionGrpcClient()); err != nil {
		return err
	}

	db, err := database.OpenPostgres(bc.GetDatabase())
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Error("close database", "error", err)
		}
	}()
	schemaContext, cancelSchemaMigration := context.WithTimeout(context.Background(), time.Minute)
	err = data.AutoMigrateSchema(schemaContext, db)
	cancelSchemaMigration()
	if err != nil {
		return fmt.Errorf("initialize core database schema: %w", err)
	}
	logger.Info("core database schema synchronized")
	redisClient, err := platformredis.Open(context.Background(), bc.GetRedis())
	if err != nil {
		return err
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("close redis", "error", err)
		}
	}()

	userRepo := data.NewUserRepo(db)
	wechat := data.NewWechatProvider(bc.GetWechat())
	utools, err := data.NewUToolsProvider(bc.GetUtools())
	if err != nil {
		return err
	}
	passwordHasher := data.NewPasswordHasher()
	var accessTTL, refreshTTL time.Duration
	if auth := bc.GetAuth(); auth != nil {
		if auth.GetAccessTokenTtl() != nil {
			accessTTL = auth.GetAccessTokenTtl().AsDuration()
		}
		if auth.GetRefreshTokenTtl() != nil {
			refreshTTL = auth.GetRefreshTokenTtl().AsDuration()
		}
	}
	userUsecase := biz.NewUserUsecase(wechat, utools, passwordHasher, userRepo, accessTTL, refreshTTL)
	userService := service.NewUserService(userUsecase)
	authMiddleware := service.NewAccessTokenMiddleware(userUsecase)

	contentRepo := data.NewContentRepo(db)
	contentUsecase := biz.NewContentUsecase(contentRepo)
	contentService := service.NewContentService(contentUsecase)

	meetingQuotaRepo := data.NewMeetingQuotaRepo(db)
	quotaPolicyContext, cancelQuotaPolicyLoad := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = meetingQuotaRepo.GetDefaultPolicy(quotaPolicyContext)
	cancelQuotaPolicyLoad()
	if err != nil {
		return fmt.Errorf("load meeting quota policy from PostgreSQL: %w", err)
	}
	logger.Info("meeting quota policy loaded from PostgreSQL")
	meetingRateLimiter, err := data.NewMeetingCreateRateLimiter(redisClient)
	if err != nil {
		return err
	}
	meetingQuotaUsecase, err := biz.NewMeetingQuotaUsecase(meetingQuotaRepo, meetingQuotaRepo, meetingRateLimiter)
	if err != nil {
		return err
	}
	visionGateway, err := data.NewVisionTranscriptionGateway(context.Background(), bc.GetVisionGrpcClient())
	if err != nil {
		return err
	}
	defer func() {
		if err := visionGateway.Close(); err != nil {
			logger.Error("close vision gRPC client", "error", err)
		}
	}()
	meetingRepo := data.NewMeetingRepo(db)
	meetingUsecase, err := biz.NewMeetingUsecase(meetingRepo, meetingQuotaUsecase, visionGateway)
	if err != nil {
		return err
	}
	meetingService := service.NewMeetingService(meetingUsecase)
	meetingIngestService := service.NewMeetingIngestInternalService(meetingUsecase)
	quotaReconciler, err := worker.NewMeetingQuotaReconciler(meetingQuotaUsecase, 30*time.Second, 100, logger)
	if err != nil {
		return fmt.Errorf("create meeting quota reconciliation worker: %w", err)
	}

	httpServer, err := server.NewHTTPServer(bc.Server, bc.GetHttpCors(), authMiddleware, userService, contentService, meetingService)
	if err != nil {
		return fmt.Errorf("create core HTTP server: %w", err)
	}
	grpcServer := server.NewGRPCServer(bc.Server, authMiddleware, userService, contentService, meetingService, meetingIngestService)
	app := bootstrap.NewApp("tiehu.core", Version, id, logger, grpcServer, httpServer, quotaReconciler)
	if err := app.Run(); err != nil {
		return fmt.Errorf("run core-service: %w", err)
	}
	return nil
}
