package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/biz"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/data/asr/startupprobe"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/server"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/service"
	"github.com/tiehu-ai/tiehu-fitness/app/vision/internal/worker"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/bootstrap"
	"github.com/tiehu-ai/tiehu-fitness/internal/platform/database"
	platformredis "github.com/tiehu-ai/tiehu-fitness/internal/platform/redis"
)

var Version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("vision-service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var confPath string
	var localFakeASR bool
	var localFakeLLM bool
	flag.StringVar(&confPath, "conf", "./configs/vision.yaml", "config file")
	flag.BoolVar(&localFakeASR, "local-fake-asr", false, "use synthetic ASR output for local development only")
	flag.BoolVar(&localFakeLLM, "local-fake-llm", false, "use deterministic meeting summaries for local development only")
	flag.Parse()

	bc, err := bootstrap.Load(confPath)
	if err != nil {
		return err
	}
	id, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}
	logger := bootstrap.NewLogger("tiehu.vision", id)
	if err := data.ValidateASRRuntimeConfig(bc.GetAsr()); err != nil {
		return err
	}
	redisClient, err := platformredis.Open(context.Background(), bc.GetRedis())
	if err != nil {
		return err
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("close redis", "error", err)
		}
	}()
	db, err := database.OpenPostgres(bc.GetDatabase())
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Error("close postgres", "error", err)
		}
	}()
	schemaContext, cancelSchemaMigration := context.WithTimeout(context.Background(), time.Minute)
	err = data.AutoMigrateSchema(schemaContext, db)
	cancelSchemaMigration()
	if err != nil {
		return fmt.Errorf("initialize vision database schema: %w", err)
	}
	logger.Info("vision database schema synchronized")
	aiConfigRepo, err := data.NewAIProviderConfigRepo(db)
	if err != nil {
		return err
	}
	var credentialRepo *data.ProviderCredentialRepo
	if !localFakeASR || !localFakeLLM {
		credentialRepo, err = data.NewProviderCredentialRepo(db)
		if err != nil {
			return err
		}
	}
	repo := data.NewAnalysisRepo()
	uc := biz.NewAnalysisUsecase(repo)
	svc := service.NewVisionService(uc)
	transcriptionRepo, err := data.NewTranscriptionRepo(db)
	if err != nil {
		return err
	}
	ticketRepo, err := data.NewTranscriptionTicketRepo(redisClient)
	if err != nil {
		return err
	}
	realtime := bc.GetRealtimeTranscription()
	if realtime == nil || realtime.GetTicketTtl() == nil || realtime.GetChunkDuration() == nil {
		return fmt.Errorf("realtime transcription config is incomplete")
	}
	outboxConfig := bc.GetTranscriptionOutboxWorker()
	if outboxConfig == nil || outboxConfig.GetPollInterval() == nil || outboxConfig.GetLeaseTimeout() == nil ||
		outboxConfig.GetInitialBackoff() == nil || outboxConfig.GetMaxBackoff() == nil {
		return fmt.Errorf("transcription outbox worker config is incomplete")
	}
	summaryWorkerConfig := bc.GetMeetingSummaryWorker()
	if summaryWorkerConfig == nil || summaryWorkerConfig.GetPollInterval() == nil || summaryWorkerConfig.GetLeaseTimeout() == nil ||
		summaryWorkerConfig.GetInitialBackoff() == nil || summaryWorkerConfig.GetMaxBackoff() == nil {
		return fmt.Errorf("meeting summary worker config is incomplete")
	}
	asrProviders, err := data.NewASRProviderResolver(aiConfigRepo, credentialRepo, bc.GetAsr(), realtime.GetMaxQueueChunks(), localFakeASR, logger)
	if err != nil {
		return fmt.Errorf("create ASR provider resolver: %w", err)
	}
	configLoadCtx, cancelConfigLoad := context.WithTimeout(context.Background(), 5*time.Second)
	activeASRConfig, err := aiConfigRepo.GetActiveASRProviderConfig(configLoadCtx)
	if err != nil {
		cancelConfigLoad()
		return fmt.Errorf("load active ASR provider configuration: %w", err)
	}
	asrBinding, err := asrProviders.Resolve(configLoadCtx, activeASRConfig.ID)
	cancelConfigLoad()
	if err != nil {
		return fmt.Errorf("construct active ASR provider configuration: %w", err)
	}
	if localFakeASR {
		logger.Warn("local fake ASR provider enabled; audio is not being recognized")
	}
	logger.Info("active ASR provider configuration loaded",
		"config_id", asrBinding.ConfigID, "version", activeASRConfig.Version,
		"provider", asrBinding.Provider.Name(), "realtime_model", activeASRConfig.RealtimeModel,
		"file_model", activeASRConfig.FileModel)
	if realtime.GetAllowInsecureLoopback() && strings.HasPrefix(realtime.GetWebsocketUrl(), "ws://") && !localFakeASR {
		logger.Warn("loopback business WebSocket uses insecure ws:// for local development")
	}
	audioSpec := biz.AudioSpec{
		Format: biz.AudioFormatPCMS16LE, MIMEType: "audio/pcm", SampleRate: 16_000, Channels: 1,
		ChunkDuration: realtime.GetChunkDuration().AsDuration(), MaxChunkBytes: bc.GetAsr().GetMaxAudioFrameBytes(),
	}
	if !localFakeASR {
		probeResult, err := startupprobe.Run(context.Background(), asrBinding.Provider, audioSpec, bc.GetAsr().GetStartupProbe())
		if err != nil {
			return fmt.Errorf("verify Bailian ASR startup probe: %w", err)
		}
		if probeResult != nil {
			logger.Info("Bailian ASR startup probe passed",
				"provider", probeResult.Provider,
				"audio_duration", probeResult.AudioDuration,
				"elapsed", probeResult.Elapsed,
				"transcript", probeResult.Transcript,
			)
		}
	}
	transcriptionUC, err := biz.NewTranscriptionUsecase(
		transcriptionRepo, ticketRepo, transcriptionRepo, asrProviders, nil, nil,
		biz.TranscriptionPolicy{
			WebSocketURL:                   realtime.GetWebsocketUrl(),
			AllowInsecureLoopbackWebSocket: localFakeASR || realtime.GetAllowInsecureLoopback(),
			TicketTTL:                      realtime.GetTicketTtl().AsDuration(),
			Audio:                          audioSpec,
			MaxQueueChunks:                 realtime.GetMaxQueueChunks(),
		},
	)
	if err != nil {
		return fmt.Errorf("create transcription use case: %w", err)
	}
	coreGateway, err := data.NewCoreMeetingIngestGateway(context.Background(), bc.GetCoreGrpcClient())
	if err != nil {
		return err
	}
	defer func() {
		if err := coreGateway.Close(); err != nil {
			logger.Error("close core gRPC client", "error", err)
		}
	}()
	outboxUC, err := biz.NewTranscriptionOutboxUsecase(transcriptionRepo, coreGateway, biz.TranscriptionOutboxPolicy{
		LeaseTimeout: outboxConfig.GetLeaseTimeout().AsDuration(), BatchSize: int(outboxConfig.GetBatchSize()),
		MaxAttempts: outboxConfig.GetMaxAttempts(), InitialBackoff: outboxConfig.GetInitialBackoff().AsDuration(),
		MaxBackoff: outboxConfig.GetMaxBackoff().AsDuration(), Audio: audioSpec,
	})
	if err != nil {
		return fmt.Errorf("create transcription outbox use case: %w", err)
	}
	summaryRepo, err := data.NewMeetingSummaryRepo(db)
	if err != nil {
		return err
	}
	summarizers, err := data.NewMeetingSummarizerResolver(aiConfigRepo, credentialRepo, summaryRepo, bc.GetLlm(), localFakeLLM, logger)
	if err != nil {
		return fmt.Errorf("create meeting summary provider resolver: %w", err)
	}
	configLoadCtx, cancelConfigLoad = context.WithTimeout(context.Background(), 5*time.Second)
	activeSummaryConfig, err := aiConfigRepo.GetActiveMeetingSummaryProviderConfig(configLoadCtx)
	if err != nil {
		cancelConfigLoad()
		return fmt.Errorf("load active meeting summary provider configuration: %w", err)
	}
	summaryBinding, err := summarizers.Resolve(configLoadCtx, activeSummaryConfig.ID)
	cancelConfigLoad()
	if err != nil {
		return fmt.Errorf("construct active meeting summary provider configuration: %w", err)
	}
	if localFakeLLM {
		logger.Warn("local fake LLM provider enabled; meeting summaries are synthetic")
	}
	logger.Info("active meeting summary provider configuration loaded",
		"config_id", summaryBinding.ConfigID, "version", activeSummaryConfig.Version,
		"provider", summaryBinding.Provider,
		"model", summaryBinding.ModelName, "prompt_version", summaryBinding.PromptVersion)
	summaryUC, err := biz.NewMeetingSummaryUsecase(summaryRepo, coreGateway, summarizers, biz.MeetingSummaryPolicy{
		LeaseTimeout: summaryWorkerConfig.GetLeaseTimeout().AsDuration(), BatchSize: int(summaryWorkerConfig.GetBatchSize()),
		MaxAttempts: summaryWorkerConfig.GetMaxAttempts(), InitialBackoff: summaryWorkerConfig.GetInitialBackoff().AsDuration(),
		MaxBackoff: summaryWorkerConfig.GetMaxBackoff().AsDuration(),
	}, logger)
	if err != nil {
		return fmt.Errorf("create meeting summary use case: %w", err)
	}
	transcriptionSvc := service.NewMeetingTranscriptionInternalService(transcriptionUC, summaryUC)
	realtimeSvc, err := service.NewRealtimeTranscriptionService(transcriptionUC, realtime.GetMaxQueueChunks())
	if err != nil {
		return fmt.Errorf("create realtime transcription service: %w", err)
	}
	hs, realtimeServer, err := server.NewHTTPServer(bc.Server, realtime, svc, realtimeSvc, logger)
	if err != nil {
		return fmt.Errorf("create vision HTTP server: %w", err)
	}
	gs := server.NewGRPCServer(bc.Server, svc, transcriptionSvc)
	pollInterval := outboxConfig.GetPollInterval().AsDuration()
	if summaryPoll := summaryWorkerConfig.GetPollInterval().AsDuration(); summaryPoll < pollInterval {
		pollInterval = summaryPoll
	}
	workerPool, err := worker.NewServer(outboxUC, pollInterval, logger, summaryUC)
	if err != nil {
		return fmt.Errorf("create transcription outbox worker: %w", err)
	}
	app := bootstrap.NewApp("tiehu.vision", Version, id, logger, gs, hs, realtimeServer, workerPool)
	if err := app.Run(); err != nil {
		return fmt.Errorf("run vision-service: %w", err)
	}
	return nil
}
