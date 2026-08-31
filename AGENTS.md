# Tiehu Fitness Engineering Rules

This file applies to the entire repository. The Chinese reference is
`AGENTS.zh-CN.md`. Keep both documents semantically aligned whenever either
one changes. In case of accidental translation differences, this file is the
machine-readable source of truth.

## 1. Project Baseline

- The repository is a Go 1.25+, Kratos v3, Protobuf, gRPC/HTTP, PostgreSQL,
  and GORM project.
- Keep one root `go.mod`. Do not add `go.work`, nested Go modules, or a
  third deployable service without an explicit architectural decision.
- The two deployable services are:
  - `core-service`: identity, session, profile, fitness content, training,
    workout records, and engagement.
  - `vision-service`: media processing, equipment recognition, posture
    analysis, AI jobs, models, and the bounded internal worker pool.
- User, content, and training packages inside core are modules, not separate
  microservices. Do not add network calls between modules in the same process.
- Core and vision own separate data. A service must never query or mutate the
  other service's database.
- Preserve unrelated user changes in a dirty worktree. Do not run destructive
  Git or filesystem commands unless the user explicitly requests them.

Existing code is not an exemption from these rules. When a touched legacy area
violates a rule, improve it in the same change when the migration is safe and
in scope. Otherwise, document the debt and do not copy the violation into new
code.

## 2. Required Architecture and Dependency Direction

Use the existing Kratos layering:

```text
api/*.proto
    |
server -> service -> biz <- data -> data/model
                     ^
                  worker
```

Responsibilities:

- `api/<domain>/v1`: versioned transport contracts and HTTP annotations.
- `app/<service>/cmd/<service>`: composition root only; load configuration,
  construct dependencies, start the app, and return startup errors.
- `internal/server`: HTTP/gRPC configuration, middleware, and route
  registration.
- `internal/service`: transport adaptation and Proto/domain mapping only.
- `internal/biz`: domain types, invariants, use cases, repository ports, and
  state transitions.
- `internal/data`: repository adapters, transactions, persistence mapping,
  caches, and third-party clients.
- `internal/data/model`: GORM persistence models only.
- `internal/worker`: vision background execution through biz use cases; it
  must not bypass biz and mutate repositories directly.
- `internal/platform`: business-neutral infrastructure shared by both
  services. Do not place core- or vision-specific business rules here.

Forbidden dependency examples:

- `service` importing GORM, SQL drivers, or `data/model`.
- `biz` importing `service`, `data`, GORM, Kratos transport, or generated
  HTTP handlers.
- `data/model` being returned directly from a service or Proto response.
- A worker updating tables directly instead of calling a biz use case.
- One service importing the other service's `internal` packages.

Dependencies must be injected through constructors. Avoid package-level
mutable state and hidden singleton clients.

## 3. API-First and Generated Code

- Define or change an API in Proto first, then regenerate code, then implement
  service, biz, and data.
- Never manually edit `*.pb.go`, `*_grpc.pb.go`, `*_http.pb.go`,
  `internal/conf/*.pb.go`, or `openapi.yaml`.
- OpenAPI descriptions belong in Proto comments. Generator-wide metadata
  belongs in `buf.gen.yaml`.
- Use `<domain>.v1` packages and stable, resource-oriented HTTP paths.
- RPC names start with verbs. Proto fields use `snake_case`.
- Mark required input with `google.api.field_behavior = REQUIRED`.
- Every service, RPC, request/response message, and non-obvious field needs a
  short useful comment.
- Never change or reuse a published Proto field number. Reserve removed field
  names and numbers.
- A breaking route, field, enum, or response change requires an explicit
  compatibility and rollout plan.
- Use `google.protobuf.Timestamp` and `Duration` instead of custom string
  time formats.
- List APIs need bounded pagination. Create/submit APIs must define retry and
  idempotency behavior.
- Do not expose database models, secrets, provider payloads, or unbounded JSON
  blobs merely because they are easy to serialize.

After a Proto change, run:

```bash
make api
buf lint
git diff -- api openapi.yaml
```

## 4. Closed Sets Must Use Domain Types or Enums

Any field with a closed value set must not be represented or compared as an
untyped string. This includes names such as `status`, `state`, `type`,
`kind`, `role`, `level`, `source`, and similar categorical fields.

Required approach:

- Define the canonical type and constants in `biz`.
- Define a Proto enum at the transport boundary when the value is public.
- Validate values at every untrusted boundary.
- Convert using explicit parse/mapping functions with an error path.
- Keep database constraints and domain values aligned.
- Model lifecycle changes as allowed transitions, not arbitrary assignment.
- Proto enums must contain an `UNSPECIFIED = 0` value and must never reuse
  published numbers.

Bad:

```go
if plan.Status == "active" {
    plan.Status = "completed"
}
```

Good:

```go
type TrainingPlanStatus string

const (
    TrainingPlanStatusDraft     TrainingPlanStatus = "draft"
    TrainingPlanStatusActive    TrainingPlanStatus = "active"
    TrainingPlanStatusCompleted TrainingPlanStatus = "completed"
    TrainingPlanStatusCancelled TrainingPlanStatus = "cancelled"
)

func ParseTrainingPlanStatus(raw string) (TrainingPlanStatus, error) {
    switch raw {
    case string(TrainingPlanStatusDraft):
        return TrainingPlanStatusDraft, nil
    case string(TrainingPlanStatusActive):
        return TrainingPlanStatusActive, nil
    case string(TrainingPlanStatusCompleted):
        return TrainingPlanStatusCompleted, nil
    case string(TrainingPlanStatusCancelled):
        return TrainingPlanStatusCancelled, nil
    default:
        return "", fmt.Errorf("unknown training plan status %q", raw)
    }
}

func (s TrainingPlanStatus) CanTransitionTo(next TrainingPlanStatus) bool {
    switch s {
    case TrainingPlanStatusDraft:
        return next == TrainingPlanStatusActive ||
            next == TrainingPlanStatusCancelled
    case TrainingPlanStatusActive:
        return next == TrainingPlanStatusCompleted ||
            next == TrainingPlanStatusCancelled
    default:
        return false
    }
}
```

Do not scatter duplicated enum conversion switches across service and data.
Keep one transport mapper and one persistence parser per domain type.

## 5. Type Safety: No Unchecked Assertions or Narrowing

Do not use conversions that can panic, silently overflow, truncate, or accept
an invalid domain value.

Forbidden:

```go
name := value.(string)       // panics when the dynamic type differs
days := int16(req.Days)      // can silently overflow
status := Status(pb.Status)  // assumes unrelated enum values match
item := items[0]             // panics when empty
result := *maybeResult       // panics when nil
```

Required:

```go
name, ok := value.(string)
if !ok {
    return errors.BadRequest("NAME_TYPE_INVALID", "name must be a string")
}

const (
    minInt16 = -1 << 15
    maxInt16 = 1<<15 - 1
)
if req.Days < minInt16 || req.Days > maxInt16 {
    return errors.BadRequest("DAYS_OUT_OF_RANGE", "days is out of range")
}
days := int16(req.Days)

if len(items) == 0 {
    return errors.NotFound("ITEM_NOT_FOUND", "item not found")
}
item := items[0]
```

Additional rules:

- Use comma-ok for type assertions and map reads when absence is meaningful.
- Check pointers, slices, and optional nested Proto messages before access.
- Parse external strings with `strconv`, `time.Parse`, UUID parsers, or a
  domain parser and handle the error.
- Map Proto enums to domain enums with an exhaustive `switch`; do not rely on
  numeric equality.
- Check bounds before narrowing numeric types.
- Do not use `unsafe` or reflection to avoid normal type modeling.
- Generic helpers must preserve type information; do not replace typed domain
  APIs with `any`.

## 6. Panic Policy and Recovery Boundaries

- Request paths, biz logic, repositories, mappers, and workers return errors;
  they do not panic for expected failures.
- Prefer a `run() error` composition root over `panic(err)` in `main`.
- A panic is acceptable only for a proven impossible invariant or an
  unrecoverable programmer error, and the reason must be documented.
- Never use panic for validation, not-found cases, provider failures, database
  failures, or control flow.
- Do not call `Must*` helpers with runtime or user-controlled input.
- Kratos HTTP/gRPC servers must keep recovery middleware as the final safety
  boundary.
- Every manually started goroutine and worker execution boundary needs its own
  panic recovery because `recover` only works in the same goroutine.
- Recovery must log structured context and a stack trace, convert the failure
  into a stable internal error or failed job state, and never silently swallow
  the panic.
- Adding `recover` does not justify unsafe indexing, assertions, or nil
  dereferences. Remove the panic risk first.

Bad:

```go
func loadProfile(raw any) Profile {
    return raw.(Profile)
}
```

Acceptable worker boundary:

```go
defer func() {
    if recovered := recover(); recovered != nil {
        logger.Error("worker panic",
            "job_id", jobID,
            "panic", recovered,
            "stack", string(debug.Stack()),
        )
        if err := markJobFailed(ctx, jobID); err != nil {
            logger.Error("mark job failed", "job_id", jobID, "error", err)
        }
    }
}()
```

## 7. Error Handling

- Return stable Kratos errors at the application boundary with uppercase
  reasons such as `EXERCISE_NOT_FOUND`.
- Do not return SQL text, DSNs, provider secrets, stack traces, or raw internal
  errors to clients.
- Preserve internal causes with `%w` or `WithCause` for logs and
  `errors.Is/As`.
- Never compare errors by message text.
- Translate errors at the owning boundary:
  - data: driver/provider errors to repository/domain-meaningful errors;
  - biz: invariant and conflict errors;
  - service: transport mapping only.
- Handle every error. An intentionally ignored best-effort error needs a
  comment and, when operationally relevant, a log or metric.
- Preserve `context.Canceled` and `context.DeadlineExceeded`; do not rewrite
  them as generic internal failures.
- Use transactions for multi-write invariants and return the transaction
  callback error so rollback occurs.

Bad:

```go
profile, _ := repo.GetProfile(ctx, userID)
if err.Error() == "record not found" { /* ... */ }
return errors.InternalServer("DB_ERROR", err.Error())
```

## 8. Database and GORM Rules

- Each service must run its data-layer `AutoMigrate` with an explicit complete
  list of service-owned GORM models after opening PostgreSQL and before starting
  repositories, workers, or transport servers. Auto-migration must be bounded
  by context, serialized across concurrent instances, and return startup errors.
- Keep paired SQL migrations for destructive changes, column/table renames,
  data backfills, database functions, triggers, partial indexes, and constraints
  that GORM cannot express safely. `AutoMigrate` must never be used as an excuse
  to weaken database invariants or silently discard data.
- Do not edit a migration already applied in a shared environment. Add paired
  `up` and `down` migrations.
- Every query uses `WithContext(ctx)`.
- Handle `gorm.ErrRecordNotFound`, translated constraint errors, and
  `RowsAffected` deliberately.
- Use parameterized queries. Never concatenate untrusted SQL fragments.
- Keep transactions short; never perform slow network or AI calls inside a
  database transaction.
- Add indexes for actual lookup, join, ordering, and uniqueness patterns.
- Enforce critical invariants with database constraints as well as biz
  validation.
- Closed-set columns need a typed domain representation and a database check
  constraint. Unknown stored values must fail parsing instead of flowing into
  business logic.
- Avoid N+1 queries and unbounded `Find` calls. Batch, preload intentionally,
  or paginate.
- Store token hashes, never usable plaintext tokens. Encrypt sensitive provider
  material such as WeChat `session_key`, or do not persist it.
- Core and vision migrations and databases remain independent; no cross-service
  foreign keys.
- GORM models are persistence details. Convert them to biz entities in data.

## 9. Concurrency, Workers, and State Machines

- Every goroutine has an owner, cancellation path, and bounded lifetime.
- Propagate contexts and use explicit timeouts for network, storage, and model
  calls.
- Use bounded worker pools and backpressure; do not create one goroutine per
  unbounded input.
- Shared memory needs clear synchronization or single-owner channel semantics.
  Validate concurrent code with `go test -race`.
- Jobs and commands must be idempotent when retries are possible.
- Claiming a job and changing `pending -> processing` must be atomic through
  a queue guarantee or database transaction/lock.
- Encode allowed job transitions in a domain state machine. Do not write status
  strings from service, data, and worker independently.
- A failed worker must record a sanitized error and retry metadata without
  losing the original job.

## 10. Reuse, Duplication, and Package Design

- Do not copy business rules, enum mappings, validation, SQL fragments, DTO
  mapping, or error translation into multiple handlers.
- Extract reuse only at the correct boundary:
  - domain behavior stays in biz;
  - transport mapping stays in service;
  - persistence mapping and query helpers stay in data;
  - truly business-neutral infrastructure may move to internal/platform.
- Similar-looking behavior from different domains must not be merged merely to
  reduce lines. Preserve domain ownership and vocabulary.
- Prefer small cohesive functions and packages. Split code when a function has
  multiple reasons to change, not at arbitrary line counts.
- Avoid god structs, catch-all `utils` packages, cyclic dependencies, hidden
  side effects, dead code, speculative abstractions, and placeholder stubs that
  return successful zero values.
- A TODO must explain what is missing and reference an issue or concrete
  follow-up. Do not leave `TODO: implement` in a completed path.
- Comments explain intent, invariants, and non-obvious tradeoffs. Do not narrate
  obvious syntax.

Bad reuse:

```go
// utils/helpers.go
func Process(data any, kind string) any { /* every domain enters here */ }
```

Good reuse:

- one `PostureAnalyzer` strategy interface in the vision biz package;
- one shared pagination value object if semantics are genuinely identical;
- separate user and analysis status types even if both use a string in SQL.

## 11. Design Pattern Decision Rule

For every structural feature, identify the variation point, lifecycle,
ownership, and failure boundary before selecting a pattern. Use a pattern only
when it makes those forces clearer.

Preferred fits in this repository:

- Repository + dependency inversion for persistence and provider ports.
- Adapter for WeChat, object storage, model runtimes, and external APIs.
- Strategy for interchangeable workout-plan algorithms or AI model providers.
- State pattern or explicit transition table for training and analysis jobs.
- Factory/constructor for validated client construction and dependency wiring.
- Command/job model for asynchronous vision work.
- Middleware/decorator for authentication, tracing, logging, metrics, recovery,
  and idempotency.
- Outbox pattern when a database change and event publication must be
  consistent.

Do not practice pattern theater:

- no interface with meaningless `Manager/Processor/Handler` methods;
- no factory that only wraps a struct literal without validation or selection;
- no global service locator;
- no abstraction introduced only because a pattern name sounds sophisticated.

A new cross-package abstraction or design pattern must include a short code or
design comment explaining the problem it solves and why a simpler local
function is insufficient.

## 12. Security and Privacy

- Never commit secrets, real tokens, DSNs with passwords, private media URLs,
  or production user data.
- Authenticate protected endpoints and authorize resource ownership; never
  trust a path `user_id` by itself.
- Validate URI scheme, host policy, MIME type, size, duration, and ownership
  before vision downloads user-provided media. Prevent SSRF and decompression
  bombs.
- Bound request sizes and collection lengths.
- Do not log access tokens, refresh tokens, WeChat codes, session keys, raw
  videos, or sensitive profile notes.
- Use least-privilege database and object-storage credentials.
- Sanitize provider and model errors before returning them.

## 13. Observability

- Use structured logs with service, operation, request/job ID, and safe domain
  identifiers.
- Log an error once at the boundary that has enough context to act on it; avoid
  duplicate logging at every layer.
- Long-running vision jobs need duration, queue delay, result, retry, and model
  version metrics.
- Propagate tracing and correlation metadata across HTTP, gRPC, and jobs.
- Never trade secret or PII safety for debugging convenience.

## 14. Testing Requirements

- Biz tests use fakes defined at the test boundary; they do not depend on the
  production data implementation.
- Data behavior involving SQL, constraints, transactions, or locking needs
  PostgreSQL integration tests. Do not assume SQLite has equivalent semantics.
- Service tests cover Proto/domain mapping and path/body precedence.
- Use table-driven tests for validation, enum parsing, transitions, and error
  mapping.
- Add regression tests for every bug fix.
- Test duplicate requests, retry behavior, not-found cases, invalid enum
  values, boundary numeric values, nil/empty input, transaction rollback, and
  context cancellation when relevant.
- Concurrent code needs race tests and deterministic cancellation tests.
- Tests must assert behavior and stable error reasons, not private
  implementation details.

Minimum validation for ordinary changes:

```bash
gofmt -w <changed-go-files>
buf lint
go test ./...
go vet ./...
go build ./app/core/cmd/core
go build ./app/vision/cmd/vision
git diff --check
```

Run `go test -race ./...` for concurrency changes and execute migrations
against a disposable PostgreSQL database for persistence changes.

## 15. Dependency and Repository Hygiene

- Prefer the standard library and existing dependencies. A new dependency
  needs a concrete benefit, maintenance/license review, and minimal scope.
- Pin code generators in Buf templates and keep generated output reproducible.
- Do not introduce vendored generated code, a second framework, or duplicate
  infrastructure wrappers.
- Format only touched files; avoid unrelated repository-wide rewrites.
- Do not leave build binaries, temporary media, local databases, coverage
  output, or secrets in the repository.
- Do not weaken lint, tests, constraints, or error handling merely to make a
  check pass.

## 16. Required Change Workflow

Before coding:

1. Decide whether the feature belongs to core or vision.
2. Identify domain ownership, invariants, states, trust boundaries, and failure
   modes.
3. Check whether the API or database contract changes and assess
   compatibility.
4. Select the simplest suitable pattern and record non-obvious reasoning.

Implementation order:

1. Proto contract and comments.
2. Generated code and OpenAPI review.
3. Biz domain types, enums/state transitions, ports, and use case.
4. Data adapter, model mapping, transaction, and migration.
5. Service mapping and server/worker wiring.
6. Tests, documentation, formatting, and full validation.

Definition of done:

- The implementation follows the existing service and layer boundaries.
- Closed sets are typed and validated; no raw status/type string comparisons
  were introduced.
- No unchecked assertion, narrowing conversion, nil dereference, indexing, or
  avoidable panic risk remains.
- Errors are stable for clients and preserve safe internal causes.
- Duplicate logic was removed without crossing business boundaries.
- Security, concurrency, idempotency, observability, and rollback were
  considered where relevant.
- Generated files are reproducible and were not edited manually.
- Tests and validation pass, and remaining limitations are stated explicitly.

## 17. Rejected Garbage-Code Patterns

The following patterns are review blockers even when the code compiles:

| Rejected pattern | Why it is rejected | Required replacement |
|---|---|---|
| Service method queries GORM directly | Breaks dependency direction and makes business behavior transport-dependent | Add a biz use case and repository port |
| Same validation or mapping copied into several handlers | Fixes drift and one path will eventually be missed | Extract a boundary-owned typed helper |
| Stub returns `nil, nil`, an empty object, or success without doing the work | Produces false success and hides incomplete behavior | Return a stable NotImplemented/internal error or keep the path unregistered |
| `go func()` without ownership, cancellation, bounds, or recovery | Leaks resources and can crash or overload the process | Use an owned errgroup or bounded worker pool |
| `context.Background()` replaces the request context | Discards cancellation, deadline, and trace propagation | Pass the caller context; derive only a bounded child context |
| Error is ignored or only printed | Loses rollback and operational failure signals | Return, translate, or deliberately record the error |
| One generic Manager switches on string kinds for unrelated domains | Creates a god abstraction and destroys business boundaries | Separate domain strategies/adapters with typed inputs |
| Mutable global map/client/config | Introduces races, hidden coupling, and test pollution | Constructor injection and explicit ownership |
| Recover logs nothing and returns success | Corrupts state while pretending the operation succeeded | Log stack/context and return failure or mark the job failed |
| Repeated per-row database query | Creates N+1 latency and load | Batch query, join, or intentional preload |

Bad layer leakage and false success:

```go
func (s *UserService) GetProfile(ctx context.Context, req *v1.Request) (*v1.Reply, error) {
    var row model.FitnessProfile
    _ = s.db.Where("user_id = ?", req.UserId).First(&row).Error
    return &v1.Reply{}, nil
}
```

Bad unbounded work and swallowed panic:

```go
for _, video := range videos {
    go func() {
        defer func() { _ = recover() }()
        analyze(context.Background(), video)
    }()
}
```

Code review must reject a change that solves the happy path by weakening type
safety, boundaries, failure handling, cancellation, or observability. Fewer
lines are not automatically better; clear ownership and correct behavior are.
