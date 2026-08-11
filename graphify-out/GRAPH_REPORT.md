# Graph Report - .  (2026-08-11)

## Corpus Check
- Corpus is ~44,940 words - fits in a single context window. You may not need a graph.

## Summary
- 778 nodes · 2077 edges · 26 communities (23 shown, 3 thin omitted)
- Extraction: 85% EXTRACTED · 15% INFERRED · 0% AMBIGUOUS · INFERRED: 312 edges (avg confidence: 0.8)
- Token cost: 43,906 input · 0 output

## Community Hubs (Navigation)
- API Errors and Pagination
- Cashback HTTP Handlers and DTOs
- Outfit Domain Model and Stores
- Cashback Ledger Persistence
- Outfit and Bundle Service Tests
- Transaction Service and Handlers
- Cached Outfit Service Layer
- HTTP API Integration Tests
- Deployment and Architecture Docs
- App Bootstrap and Config
- Postgres Store Integration Tests
- Cashback Service Tests
- Redis Cache Backend
- Outfit Request Body Binding
- Router and Health Endpoints
- Authentication Middleware
- Logging and Request Middleware
- HTTP Idempotency Middleware
- No-op Cache Backend
- In-Memory Idempotency Store
- Redis Idempotency Store
- Fixed Window Rate Limiter
- HTTP Status Recorder
- Bundle Robux Model Tests
- Async Name Embedding
- Module Root

## God Nodes (most connected - your core abstractions)
1. `Outfit` - 45 edges
2. `newTestServer()` - 41 edges
3. `do()` - 35 edges
4. `newOutfitService()` - 34 edges
5. `requireStatus()` - 33 edges
6. `writeError()` - 32 edges
7. `writeJSON()` - 29 edges
8. `Transaction` - 22 edges
9. `MemoryCashback` - 21 edges
10. `BadRequest()` - 20 edges

## Surprising Connections (you probably didn't know these)
- `server override: api on external proxy network` --semantically_similar_to--> `compose service: caddy (profile edge)`  [INFERRED] [semantically similar]
  docker-compose.server.yml → docker-compose.yml
- `run()` --calls--> `NewRouter()`  [INFERRED]
  cmd/server/main.go → internal/httpapi/router.go
- `openBackend()` --calls--> `OpenRedis()`  [INFERRED]
  cmd/server/main.go → internal/cache/redis.go
- `openBackend()` --calls--> `NewMemoryStore()`  [INFERRED]
  cmd/server/main.go → internal/idempotency/idempotency.go
- `openBackend()` --calls--> `NewRedisStore()`  [INFERRED]
  cmd/server/main.go → internal/idempotency/redis.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Compose runtime stack (api + db + redis + proxy)** — docker_compose_api, docker_compose_db, docker_compose_redis, docker_compose_caddy, docker_compose_server_api_override [EXTRACTED 1.00]
- **Cashback accrual, redeem and reversal flow** — readme_cashback_system, readme_cashback_bonus_rate, readme_cashback_entry_ledger, readme_redeem_request, readme_cashback_reversal, readme_idempotency [EXTRACTED 1.00]
- **Storage port layering: memory, postgres, cached** — readme_store_port_interface, readme_in_memory_store, readme_cache_layer, readme_namespace_version_invalidation [EXTRACTED 1.00]

## Communities (26 total, 3 thin omitted)

### Community 0 - "API Errors and Pagination"
Cohesion: 0.05
Nodes (56): Error, BadRequest(), Conflict(), Forbidden(), Gone(), New(), NotFound(), RateLimited() (+48 more)

### Community 1 - "Cashback HTTP Handlers and DTOs"
Cohesion: 0.07
Nodes (66): avatarBodyDTO, bodyColorsDTO, bodyScalesDTO, cashbackBonusDTO, cashbackEntryDTO, cashbackEventDTO, cashbackHandler, cashbackSummaryDTO (+58 more)

### Community 2 - "Outfit Domain Model and Stores"
Cohesion: 0.06
Nodes (50): Time, cloneOutfit(), Context, RWMutex, ensurePlayer(), Context, Outfits, Transactions (+42 more)

### Community 3 - "Cashback Ledger Persistence"
Cohesion: 0.08
Nodes (33): Time, Time, cloneRedeem(), Context, RWMutex, Time, NewMemoryCashback(), T (+25 more)

### Community 4 - "Outfit and Bundle Service Tests"
Cohesion: 0.09
Nodes (56): T, TestAccrueMenghitungBundleSekali(), TestCreateOutfitMembawaFieldBundle(), TestCreateOutfitMenolakBundleTidakValid(), NewOutfits(), Outfits, T, newOutfitService() (+48 more)

### Community 5 - "Transaction Service and Handlers"
Cohesion: 0.07
Nodes (39): createTxBody, txItemBody, txItemResultDTO, Time, Cashback, Context, Time, indexedField() (+31 more)

### Community 6 - "Cached Outfit Service Layer"
Cohesion: 0.09
Nodes (23): Cache, countingOutfits, fakeCache, outfitPage, Outfits, HashString(), Key(), discardLogger() (+15 more)

### Community 7 - "HTTP API Integration Tests"
Cohesion: 0.24
Nodes (39): request, createOutfitBody(), do(), T, newTestServer(), requireErrorCode(), requireStatus(), TestBodyJSONTidakValid() (+31 more)

### Community 8 - "Deployment and Architecture Docs"
Cohesion: 0.06
Nodes (39): compose service: adminer (profile tools), compose service: api, compose service: caddy (profile edge), compose service: db (pgvector/pgvector:pg17), db/init mounted as docker-entrypoint-initdb.d, initdb UTF8 / locale=C determinism, pgvector image for OUTFIT.name_embedding, compose service: redis (redis:8-alpine) (+31 more)

### Community 9 - "App Bootstrap and Config"
Cohesion: 0.11
Nodes (25): Cashback, Context, Logger, Outfits, Templates, Transactions, main(), newAuthenticator() (+17 more)

### Community 10 - "Postgres Store Integration Tests"
Cohesion: 0.28
Nodes (21): Pool, NewOutfits(), Pool, T, Time, newPool(), resetSchema(), sampleOutfit() (+13 more)

### Community 11 - "Cashback Service Tests"
Cohesion: 0.37
Nodes (17): assertAPIError(), day(), Cashback, T, Time, Transactions, newCashbackFixture(), TestAccrualDasarDuaPuluhPersenDenganFloor() (+9 more)

### Community 12 - "Redis Cache Backend"
Cohesion: 0.24
Nodes (5): Redis, Client, Context, Duration, OpenRedis()

### Community 13 - "Outfit Request Body Binding"
Cohesion: 0.18
Nodes (13): avatarBodyBody, bodyColorsBody, bodyScalesBody, createOutfitBody, jsonID, outfitItemBody, registerTemplateBody, replaceItemsBody (+5 more)

### Community 14 - "Router and Health Endpoints"
Cohesion: 0.18
Nodes (13): Deps, Cashback, Context, Handler, Logger, Outfits, Request, ResponseWriter (+5 more)

### Community 15 - "Authentication Middleware"
Cohesion: 0.23
Nodes (9): actorCtxKey, Authenticator, StaticTokenAuth, UnverifiedActorAuth, authenticate(), Context, Handler, Request (+1 more)

### Community 16 - "Logging and Request Middleware"
Cohesion: 0.38
Nodes (10): HandlerFunc, ctxKey, accessLog(), chain(), Context, Handler, Logger, recoverPanic() (+2 more)

### Community 17 - "HTTP Idempotency Middleware"
Cohesion: 0.29
Nodes (7): Buffer, captureWriter, Store, Handler, ResponseWriter, idempotent(), writeReplay()

### Community 18 - "No-op Cache Backend"
Cohesion: 0.31
Nodes (3): Noop, Context, Duration

### Community 19 - "In-Memory Idempotency Store"
Cohesion: 0.39
Nodes (6): MemoryStore, Record, Duration, Mutex, Time, NewMemoryStore()

### Community 20 - "Redis Idempotency Store"
Cohesion: 0.39
Nodes (5): RedisStore, Client, Duration, Logger, NewRedisStore()

### Community 21 - "Fixed Window Rate Limiter"
Cohesion: 0.39
Nodes (6): Duration, Mutex, Time, NewFixedWindow(), FixedWindow, window

### Community 23 - "Bundle Robux Model Tests"
Cohesion: 0.67
Nodes (3): T, TestRobuxTotalBundleGagalSebagian(), TestRobuxTotalMenghitungBundleSekali()

## Knowledge Gaps
- **21 isolated node(s):** `github.com/hanan/avatar-catalog-backend`, `actorCtxKey`, `createRedeemBody`, `resolveRedeemBody`, `createReversalBody` (+16 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **3 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Outfit` connect `Outfit Domain Model and Stores` to `API Errors and Pagination`, `Cashback HTTP Handlers and DTOs`, `Postgres Store Integration Tests`, `Cached Outfit Service Layer`?**
  _High betweenness centrality (0.165) - this node is a cross-community bridge._
- **Why does `newTestServer()` connect `HTTP API Integration Tests` to `API Errors and Pagination`, `Cashback Ledger Persistence`, `Outfit and Bundle Service Tests`, `Transaction Service and Handlers`, `Router and Health Endpoints`, `In-Memory Idempotency Store`?**
  _High betweenness centrality (0.129) - this node is a cross-community bridge._
- **Why does `openBackend()` connect `App Bootstrap and Config` to `Cashback Ledger Persistence`, `Outfit and Bundle Service Tests`, `Transaction Service and Handlers`, `Redis Cache Backend`, `In-Memory Idempotency Store`, `Redis Idempotency Store`?**
  _High betweenness centrality (0.120) - this node is a cross-community bridge._
- **Are the 8 inferred relationships involving `newTestServer()` (e.g. with `New()` and `NewRouter()`) actually correct?**
  _`newTestServer()` has 8 INFERRED edges - model-reasoned connections that need verification._
- **Are the 9 inferred relationships involving `newOutfitService()` (e.g. with `TestCreateOutfitMembawaFieldBundle()` and `TestCreateOutfitMenolakBundleTidakValid()`) actually correct?**
  _`newOutfitService()` has 9 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/hanan/avatar-catalog-backend`, `actorCtxKey`, `createRedeemBody` to the rest of the system?**
  _21 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `API Errors and Pagination` be split into smaller, more focused modules?**
  _Cohesion score 0.05352743561030235 - nodes in this community are weakly interconnected._