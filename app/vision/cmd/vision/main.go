package main

import (
	"flag"
	"os"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/server"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/worker"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/bootstrap"
)

var Version = "dev"

func main() {
	var confPath string
	flag.StringVar(&confPath, "conf", "./configs/vision.yaml", "config file")
	flag.Parse()

	bc, err := bootstrap.Load(confPath)
	if err != nil {
		panic(err)
	}
	id, _ := os.Hostname()
	logger := bootstrap.NewLogger("tiehu.vision", id)
	repo := data.NewAnalysisRepo()
	uc := biz.NewAnalysisUsecase(repo)
	svc := service.NewVisionService(uc)
	hs := server.NewHTTPServer(bc.Server, svc)
	gs := server.NewGRPCServer(bc.Server, svc)
	workerPool := worker.NewServer(logger)
	app := bootstrap.NewApp("tiehu.vision", Version, id, logger, gs, hs, workerPool)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
