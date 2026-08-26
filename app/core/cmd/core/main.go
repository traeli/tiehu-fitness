package main

import (
	"flag"
	"os"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/data"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/server"
	"github.com/tiehu-ai/tiehu-fitness/app/core/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/bootstrap"
)

var Version = "dev"

func main() {
	var confPath string
	flag.StringVar(&confPath, "conf", "./configs/core.yaml", "config file")
	flag.Parse()

	bc, err := bootstrap.Load(confPath)
	if err != nil {
		panic(err)
	}

	id, _ := os.Hostname()
	logger := bootstrap.NewLogger("tiehu.core", id)

	userRepo := data.NewUserRepo()
	wechat := data.NewWechatProvider(bc.GetWechat())
	var accessTTL, refreshTTL time.Duration
	if auth := bc.GetAuth(); auth != nil {
		if auth.GetAccessTokenTtl() != nil {
			accessTTL = auth.GetAccessTokenTtl().AsDuration()
		}
		if auth.GetRefreshTokenTtl() != nil {
			refreshTTL = auth.GetRefreshTokenTtl().AsDuration()
		}
	}
	userUsecase := biz.NewUserUsecase(wechat, userRepo, accessTTL, refreshTTL)
	userService := service.NewUserService(userUsecase)

	contentRepo := data.NewContentRepo()
	contentUsecase := biz.NewContentUsecase(contentRepo)
	contentService := service.NewContentService(contentUsecase)

	httpServer := server.NewHTTPServer(bc.Server, userService, contentService)
	grpcServer := server.NewGRPCServer(bc.Server, userService, contentService)
	app := bootstrap.NewApp("tiehu.core", Version, id, logger, grpcServer, httpServer)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
