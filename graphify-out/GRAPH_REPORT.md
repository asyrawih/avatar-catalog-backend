# Graph Report - .  (2026-08-04)

## Corpus Check
- cluster-only mode — file stats not available

## Summary
- 728 nodes · 1750 edges · 38 communities (29 shown, 9 thin omitted)
- Extraction: 88% EXTRACTED · 12% INFERRED · 0% AMBIGUOUS · INFERRED: 209 edges (avg confidence: 0.81)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- writeError
- Outfit
- Error
- newOutfitService
- MemoryCatalog
- Outfits
- newTestServer
- newBackends
- Catalog
- Deps
- MemoryStore
- newTemplateService
- OutfitItem
- newPool
- Outfits
- Redis
- helpers.go
- Namespace-version cache invalidation
- Dead asset policy
- API rules enforced at schema level
- config.go
- Service api
- db/init 001_schema.sql + 002_seed.sql
- ids.go
- Open
- Cached read wrapper (internal/store/cached)
- httpapi.Authenticator auth seam
- POST /v1/outfits/resolve
- Catalog
- Catalog
- Handler
- Catalog
- Catalog
- Mutex
- Tx
- github.com/hanan/avatar-catalog-backend

## God Nodes (most connected - your core abstractions)
1. `Outfit` - 39 edges
2. `newTestServer()` - 35 edges
3. `do()` - 30 edges
4. `requireStatus()` - 28 edges
5. `writeError()` - 24 edges
6. `newOutfitService()` - 24 edges
7. `writeJSON()` - 22 edges
8. `MemoryCatalog` - 21 edges
9. `CatalogItem` - 19 edges
10. `Transaction` - 19 edges

## Surprising Connections (you probably didn't know these)
- `depends_on service_healthy gating` --semantically_similar_to--> `/readyz dependency readiness probe`  [INFERRED] [semantically similar]
  docker-compose.yml → README.md
- `Redis persistence off, allkeys-lru eviction` --semantically_similar_to--> `Cache failure treated as miss`  [INFERRED] [semantically similar]
  docker-compose.yml → README.md
- `Service api` --implements--> `Avatar Catalog Backend`  [INFERRED]
  docker-compose.yml → README.md
- `Service api` --shares_data_with--> `Postgres store adapter (pgx)`  [INFERRED]
  docker-compose.yml → README.md
- `Service api` --shares_data_with--> `Cached read wrapper (internal/store/cached)`  [INFERRED]
  docker-compose.yml → README.md

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Lapisan penyimpanan seragam: service -> cached -> postgres/in-memory** — readme_store_port_interface, readme_in_memory_store_fallback, readme_namespace_version_cache_invalidation, readme_package_layout [EXTRACTED 1.00]
- **Alur idempotensi: header, kolom unik Postgres, dan middleware Redis** — readme_idempotency_key, readme_transaction_idempotency_unique_column, readme_idempotency_response_middleware, readme_schema_level_constraints, docker_compose_redis [EXTRACTED 1.00]
- **Stack pengembangan lokal: db, redis, api, adminer, dan gating healthcheck** — docker_compose_db, docker_compose_redis, docker_compose_api, docker_compose_adminer, docker_compose_healthcheck_gating, docker_compose_initdb_mount [EXTRACTED 1.00]
- **Three storage layers behind one store port** — readme_store_port, readme_inmemory_store, readme_postgres_store, readme_cached_store, readme_service_layer [EXTRACTED 1.00]
- **Idempotency enforcement across Postgres and Redis** — readme_idempotency, readme_idempotency_middleware, readme_transactions_endpoint, readme_outfits_endpoint, readme_sync_runs_endpoint, readme_schema_level_constraints [EXTRACTED 1.00]
- **Dead asset lifecycle: sync run, catalog filtering, outfit render** — readme_sync_run_policy, readme_dead_asset_policy, readme_catalog_sections_endpoint, readme_humanoiddescription_consumer [EXTRACTED 1.00]

## Communities (38 total, 9 thin omitted)

### Community 0 - "writeError"
Cohesion: 0.07
Nodes (56): catalogHandler, catalogItemDTO, createOutfitBody, deadItemDTO, errorBody, errorEnvelope, jsonID, listEnvelope (+48 more)

### Community 1 - "Outfit"
Cohesion: 0.08
Nodes (32): cloneOutfit(), cloneTransaction(), Context, KeysetCursor, T, Time, NewMemoryTransactions(), sortByRecency() (+24 more)

### Community 2 - "Error"
Cohesion: 0.07
Nodes (41): Error, createTxBody, txItemBody, BadRequest(), Conflict(), Forbidden(), Gone(), New() (+33 more)

### Community 3 - "newOutfitService"
Cohesion: 0.09
Nodes (51): Authenticator, Context, Logger, Outfits, Store, Templates, Transactions, main() (+43 more)

### Community 4 - "MemoryCatalog"
Cohesion: 0.08
Nodes (34): batchGetBody, syncRunBody, Time, newCatalogItem(), newCatalogItems(), Time, Context, Pool (+26 more)

### Community 5 - "Outfits"
Cohesion: 0.10
Nodes (28): actorCtxKey, Authenticator, StaticTokenAuth, UnverifiedActorAuth, actorFrom(), authenticate(), Context, Handler (+20 more)

### Community 6 - "newTestServer"
Cohesion: 0.27
Nodes (34): request, createOutfitBody(), do(), T, newTestServer(), requireErrorCode(), requireStatus(), TestBatchGetItems() (+26 more)

### Community 7 - "newBackends"
Cohesion: 0.14
Nodes (21): countingCatalog, countingOutfits, fakeCache, discardLogger(), Context, Duration, KeysetCursor, Logger (+13 more)

### Community 8 - "Catalog"
Cohesion: 0.12
Nodes (13): Cache, Noop, Catalog, sectionPage, Context, Duration, HashInt64s(), Key() (+5 more)

### Community 9 - "Deps"
Cohesion: 0.10
Nodes (26): Handler, HandlerFunc, ctxKey, Deps, statusRecorder, accessLog(), chain(), Context (+18 more)

### Community 10 - "MemoryStore"
Cohesion: 0.12
Nodes (18): Buffer, captureWriter, MemoryStore, Record, RedisStore, Store, Handler, ResponseWriter (+10 more)

### Community 11 - "newTemplateService"
Cohesion: 0.16
Nodes (22): Context, Time, NewTemplates(), ParseTemplateID(), T, Templates, newTemplateService(), TestGetTemplateBelumTerdaftar() (+14 more)

### Community 12 - "OutfitItem"
Cohesion: 0.23
Nodes (13): collectOutfitItems(), collectOutfits(), Context, KeysetCursor, Pool, Rows, insertOutfitItems(), itemsForTx() (+5 more)

### Community 13 - "newPool"
Cohesion: 0.28
Nodes (19): NewOutfits(), Pool, T, Time, newPool(), resetSchema(), sampleOutfit(), TestCatalogEtalaseMenyaringItemMati() (+11 more)

### Community 14 - "Outfits"
Cohesion: 0.22
Nodes (10): Cache, outfitPage, Outfits, Context, Duration, KeysetCursor, Logger, listNamespace() (+2 more)

### Community 15 - "Redis"
Cohesion: 0.24
Nodes (5): Redis, Client, Context, Duration, OpenRedis()

### Community 16 - "helpers.go"
Cohesion: 0.18
Nodes (10): Time, ensurePlayer(), Context, Outfits, Transactions, Time, Tx, nullableTime() (+2 more)

### Community 17 - "Namespace-version cache invalidation"
Cohesion: 0.18
Nodes (15): Service redis (redis:8-alpine), Redis persistence off, allkeys-lru eviction, Redis tanpa persistensi + allkeys-lru, Cache fail-open (error Redis = miss), Cache failure treated as miss, Idempotency-Key, Response-storing idempotency middleware, Middleware penyimpan respons idempoten (Redis) (+7 more)

### Community 18 - "Dead asset policy"
Cohesion: 0.23
Nodes (14): Pinned initdb encoding/collation, GET /v1/catalog/sections/{sectionId}/items, Cursor pagination, not offset, Dead asset policy, Amplop error tunggal, Roblox game server HumanoidDescription consumer, Guard 422 invalid_template_id, Soft delete with recoItemId reminder (+6 more)

### Community 19 - "API rules enforced at schema level"
Cohesion: 0.20
Nodes (12): Mount ./db/init ke docker-entrypoint-initdb.d, Volume pgdata, db/init/001_schema.sql, db/init/002_seed.sql, Idempotency via unique transaction column, Minimal player row upsert on first write, API rules enforced at schema level, Rig terdaftar sendiri pada pemakaian pertama (+4 more)

### Community 20 - "config.go"
Cohesion: 0.36
Nodes (8): Config, envBool(), envDuration(), envInt(), envList(), envString(), Duration, Load()

### Community 21 - "Service api"
Cohesion: 0.29
Nodes (10): Service adminer (profile tools), Service api, Service db (postgres:17-alpine), depends_on service_healthy gating, Wiring internal db:5432 / redis:6379, Service-name DSN wiring (db:5432, redis:6379), avatar-catalog compose stack, Tabel konfigurasi environment (+2 more)

### Community 22 - "db/init 001_schema.sql + 002_seed.sql"
Cohesion: 0.28
Nodes (9): Avatar Catalog Backend, db/init 001_schema.sql + 002_seed.sql, ERD v3 (13 tabel, FigJam), In-memory store fallback (tanpa DATABASE_URL), In-memory store fallback, Struktur paket (cmd/server, internal/*), Test integrasi Postgres (TEST_DATABASE_URL), Postman collection avatar-catalog-api (+1 more)

### Community 23 - "ids.go"
Cohesion: 0.38
Nodes (5): Time, newOutfitID(), newRunID(), newTxID(), randomHex()

### Community 24 - "Open"
Cohesion: 0.40
Nodes (5): Context, Duration, Pool, Open(), PoolConfig

### Community 25 - "Cached read wrapper (internal/store/cached)"
Cohesion: 0.47
Nodes (6): Cached read wrapper (internal/store/cached), /healthz liveness probe, Postgres store adapter (pgx), /readyz dependency readiness probe, Service layer (outfit, katalog, transaksi), Store port interface (internal/store)

### Community 26 - "httpapi.Authenticator auth seam"
Cohesion: 0.40
Nodes (5): AUTH_TOKENS service bearer tokens, AUTH_TOKENS (Bearer token antar-layanan), httpapi.Authenticator auth seam, GET /v1/outfits tanpa userId (bocor outfit privat), Header X-User-Id (belum diverifikasi)

## Ambiguous Edges - Review These
- `API rules enforced at schema level` → `Volume pgdata`  [AMBIGUOUS]
  docker-compose.yml · relation: conceptually_related_to
- `Volume pgdata` → `Seed hanya satu rig nyata (88484288792766)`  [AMBIGUOUS]
  docker-compose.yml · relation: conceptually_related_to

## Knowledge Gaps
- **19 isolated node(s):** `github.com/hanan/avatar-catalog-backend`, `actorCtxKey`, `batchGetBody`, `ctxKey`, `PositionCursor` (+14 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **9 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **What is the exact relationship between `API rules enforced at schema level` and `Volume pgdata`?**
  _Edge tagged AMBIGUOUS (relation: conceptually_related_to) - confidence is low._
- **What is the exact relationship between `Volume pgdata` and `Seed hanya satu rig nyata (88484288792766)`?**
  _Edge tagged AMBIGUOUS (relation: conceptually_related_to) - confidence is low._
- **Why does `Outfit` connect `Outfit` to `writeError`, `MemoryCatalog`, `Outfits`, `newBackends`, `OutfitItem`, `newPool`, `Outfits`?**
  _High betweenness centrality (0.150) - this node is a cross-community bridge._
- **Why does `newTestServer()` connect `newTestServer` to `Deps`, `newOutfitService`, `MemoryCatalog`, `Outfit`?**
  _High betweenness centrality (0.112) - this node is a cross-community bridge._
- **Why does `CatalogItem` connect `MemoryCatalog` to `writeError`, `Outfit`, `Error`, `newBackends`, `Catalog`?**
  _High betweenness centrality (0.102) - this node is a cross-community bridge._
- **Are the 7 inferred relationships involving `newTestServer()` (e.g. with `NewRouter()` and `NewMemoryCatalog()`) actually correct?**
  _`newTestServer()` has 7 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/hanan/avatar-catalog-backend`, `actorCtxKey`, `batchGetBody` to the rest of the system?**
  _19 weakly-connected nodes found - possible documentation gaps or missing edges._