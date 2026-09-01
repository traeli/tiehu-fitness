# Tiehu Fitness production deployment

This Compose project deploys only `core-service` and `vision-service`. It joins
the existing external Docker network named `shared` and reaches PostgreSQL and
Redis through the existing container names `nutrilens-pg` and
`nutrilens-redis`.

## 1. Protect the existing infrastructure ports

The application containers communicate through `shared`; PostgreSQL and Redis
do not need public host bindings. In the NutriLens Compose file, either remove
their `ports` entries or bind them to loopback:

```yaml
postgres:
  ports:
    - "127.0.0.1:5432:5432"

redis:
  ports:
    - "127.0.0.1:6379:6379"
```

Do not expose unauthenticated Redis or PostgreSQL directly to the Internet.

## 2. Create isolated databases and roles

One PostgreSQL container is sufficient, but Core and Vision must use separate
databases and roles. Existing `POSTGRES_*` environment variables only apply
when the PostgreSQL volume is initialized for the first time, so create these
manually on an existing volume:

```bash
docker exec -it nutrilens-pg psql -U nutrilens -d postgres
```

Run the following SQL after replacing both passwords:

```sql
CREATE ROLE tiehu_core LOGIN PASSWORD 'replace_core_password';
CREATE DATABASE tiehu_core OWNER tiehu_core;

CREATE ROLE tiehu_vision LOGIN PASSWORD 'replace_vision_password';
CREATE DATABASE tiehu_vision OWNER tiehu_vision;
```

Do not reuse the `nutrilens` database for Tiehu tables.

## 3. Configure deployment values

From the Tiehu repository root:

```bash
cp deploy/.env.example deploy/.env
chmod 600 deploy/.env
```

Set the database passwords, uTools credentials, and the public Vision WSS URL.
The public API and WebSocket endpoints should use two TLS domains, for example:

```text
https://api.example.com
wss://vision.example.com/v1/realtime/transcriptions
```

Use `deploy/nginx.conf.example` with host Nginx/Certbot. The Compose ports bind
to `127.0.0.1` by default so only the reverse proxy can reach them.

Image builds default to `GOPROXY=https://goproxy.cn,direct` and
`GOSUMDB=sum.golang.google.cn` for mainland China. The corresponding
`GO_BUILD_GOPROXY` and `GO_BUILD_GOSUMDB` values are exposed in `deploy/.env`,
so another environment can override them without editing the Dockerfile.

## 4. Build images and configure provider credentials

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml build
docker compose --env-file deploy/.env -f deploy/docker-compose.yml run --rm --no-deps \
  --entrypoint /app/provider-credentials vision \
  -conf /app/configs/vision.yaml
```

Enter the Bailian and DeepSeek API keys in the interactive prompts. They are
stored in the Vision database's `provider_credentials.api_key` field.

## 5. Start and inspect services

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs -f --tail=200 core vision
```

Both services synchronize their owned schema on startup. Vision also performs
the real Bailian startup probe before opening ports, so its first health check
can take roughly 30–45 seconds.

## 6. Update the uTools plugin

Build the plugin with its API base URL pointing to the public Core HTTPS domain:

```bash
cd utools-ai-meeting-assistant
VITE_API_BASE_URL=https://api.example.com npm run build:production
```

Vision's WebSocket URL is returned by the meeting creation API and comes from
`REALTIME_WEBSOCKET_URL`; it is not hardcoded in the plugin.

## Updating

```bash
git pull
docker compose --env-file deploy/.env -f deploy/docker-compose.yml build
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

To rotate provider keys, rerun the `provider-credentials` command from step 4.

## Legacy docker-compose v1

The deployment file retains Compose `3.8` compatibility for servers that still
use the legacy standalone command. Compose v2 remains preferred. With v1, set
the project name on the command line:

```bash
docker-compose --env-file deploy/.env -p tiehu-fitness \
  -f deploy/docker-compose.yml build
docker-compose --env-file deploy/.env -p tiehu-fitness \
  -f deploy/docker-compose.yml up -d
```
