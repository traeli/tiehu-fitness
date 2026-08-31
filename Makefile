GOHOSTOS := $(shell go env GOHOSTOS)
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
BUF ?= $(shell go env GOPATH)/bin/buf
COMPOSE ?= docker compose
LOCAL_ENV_FILE ?= ./configs/docker-compose.env

.PHONY: init
init:
	go install github.com/bufbuild/buf/cmd/buf@v1.61.0

.PHONY: api
api:
	$(BUF) dep update
	$(BUF) generate --template buf.gen.yaml

.PHONY: config
config:
	$(BUF) generate --template buf.gen.config.yaml

.PHONY: generate
generate:
	go generate ./...
	go mod tidy

.PHONY: all
all: api config generate

.PHONY: lint
lint:
	$(BUF) lint
	go vet ./...

.PHONY: test
test:
	go test ./...

.PHONY: build
build:
	mkdir -p bin
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/core ./app/core/cmd/core
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/vision ./app/vision/cmd/vision

.PHONY: run-core run-vision configure-vision-credentials
run-core:
	@if [ -f "$(LOCAL_ENV_FILE)" ]; then set -a; . "$(LOCAL_ENV_FILE)"; set +a; fi; go run ./app/core/cmd/core -conf ./configs/core.yaml
run-vision:
	go run ./app/vision/cmd/vision -conf ./configs/vision.yaml

configure-vision-credentials:
	go run ./app/vision/cmd/provider-credentials -conf ./configs/vision.yaml

.PHONY: infra-up infra-down infra-reset infra-status
infra-up:
	@test -f "$(LOCAL_ENV_FILE)" || (echo "$(LOCAL_ENV_FILE) is required; copy configs/docker-compose.env.example first" && exit 1)
	$(COMPOSE) --env-file "$(LOCAL_ENV_FILE)" up -d --wait

infra-down:
	@test -f "$(LOCAL_ENV_FILE)" || (echo "$(LOCAL_ENV_FILE) is required" && exit 1)
	$(COMPOSE) --env-file "$(LOCAL_ENV_FILE)" down

infra-reset:
	@test -f "$(LOCAL_ENV_FILE)" || (echo "$(LOCAL_ENV_FILE) is required" && exit 1)
	$(COMPOSE) --env-file "$(LOCAL_ENV_FILE)" down --volumes

infra-status:
	@test -f "$(LOCAL_ENV_FILE)" || (echo "$(LOCAL_ENV_FILE) is required" && exit 1)
	$(COMPOSE) --env-file "$(LOCAL_ENV_FILE)" ps

.PHONY: migrate-core-up migrate-core-down migrate-vision-up migrate-vision-down
migrate-core-up:
	@test -n "$(CORE_DATABASE_DSN)" || (echo "CORE_DATABASE_DSN is required" && exit 1)
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000001_init.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000002_add_password_credentials.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000003_use_email_for_password_credentials.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000004_add_utools_identities.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000005_add_meeting_usage_quota.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000006_add_meetings.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000007_allow_auto_transcript_language.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000008_add_meeting_summaries.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000009_add_meeting_quota_policy.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000010_compact_meeting_summaries.up.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000011_add_monthly_quota_snapshot_and_orders.up.sql

migrate-core-down:
	@test -n "$(CORE_DATABASE_DSN)" || (echo "CORE_DATABASE_DSN is required" && exit 1)
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000011_add_monthly_quota_snapshot_and_orders.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000010_compact_meeting_summaries.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000009_add_meeting_quota_policy.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000008_add_meeting_summaries.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000007_allow_auto_transcript_language.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000006_add_meetings.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000005_add_meeting_usage_quota.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000004_add_utools_identities.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000003_use_email_for_password_credentials.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000002_add_password_credentials.down.sql
	@psql "$(CORE_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/core/migrations/000001_init.down.sql

migrate-vision-up:
	@test -n "$(VISION_DATABASE_DSN)" || (echo "VISION_DATABASE_DSN is required" && exit 1)
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000001_init.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000002_add_transcription_sessions.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000003_add_local_fake_asr_provider.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000004_add_meeting_summary_jobs.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000005_add_ai_provider_configs.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000006_enforce_ai_provider_config_immutability.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000007_add_encrypted_provider_credentials.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000008_add_llm_exchange_payloads.up.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000009_store_provider_credentials_plaintext.up.sql

migrate-vision-down:
	@test -n "$(VISION_DATABASE_DSN)" || (echo "VISION_DATABASE_DSN is required" && exit 1)
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000009_store_provider_credentials_plaintext.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000008_add_llm_exchange_payloads.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000007_add_encrypted_provider_credentials.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000006_enforce_ai_provider_config_immutability.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000005_add_ai_provider_configs.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000004_add_meeting_summary_jobs.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000003_add_local_fake_asr_provider.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000002_add_transcription_sessions.down.sql
	@psql "$(VISION_DATABASE_DSN)" -v ON_ERROR_STOP=1 -f ./app/vision/migrations/000001_init.down.sql
