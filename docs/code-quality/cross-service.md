## Servisler arası kod tekrarı

Kapsam: `services/*` (11 servis) ve `pkg/` arasında tekrar eden kod. Servis içi tekrarlar bu raporun dışında. Satır numaraları `main` @ 7e9178d üzerinde okunarak doğrulandı.

### 1. `main.go` bootstrap iskeleti (config → logx → signal → otelx → health → gRPC → errgroup → graceful stop)

- **[Yüksek]** 11 servisin hepsinde `run()` aynı ~40 satırlık iskeletle başlıyor ve bitiyor: `config.Load[Config](defaults)`, `logx.New(logx.Options{Service: cfg.ServiceName, Version: cfg.Version, Level: cfg.LogLevel, Writer: os.Stdout})` + `slog.SetDefault`, `signal.NotifyContext(..., SIGINT, SIGTERM)`, `health.NewRegistry()`, aynı alanlarla `otelx.Setup(...)`, `providers.Shutdown` için 5 sn timeout'lu `defer`, `health.New(reg, 2*time.Second)`, `errgroup.WithContext`, `health.Serve(gctx, cfg.MetricsAddr, hh)`, sonra `g.Wait()` + `errors.Is(err, context.Canceled)` + `log.Info("stopped")`. `main()` da her yerde aynı: `fmt.Fprintln(os.Stderr, "<svc>:", err); os.Exit(1)`. gRPC sunan 7 serviste ayrıca `grpcx.NewServer` + `tenant.Unary/StreamServerInterceptor()` + `net.Listen("tcp", cfg.GRPCAddr)` + `NOT_SERVING` → `GracefulStop` goroutine'i de birebir aynı. analytics-svc ve assets-svc `main.go` dosyaları bu kısımlarda satır satır aynı.
  - `logx.New`: `services/aggregator/cmd/aggregator/main.go:84`, `services/analytics-svc/cmd/analytics-svc/main.go:64`, `services/assets-svc/cmd/assets-svc/main.go:74`, `services/billing-svc/cmd/billing-svc/main.go:116`, `services/gateway/cmd/gateway/main.go:114`, `services/ingest-svc/cmd/ingest-svc/main.go:84`, `services/invoice-worker/cmd/invoice-worker/main.go:56`, `services/limits-svc/cmd/limits-svc/main.go:89`, `services/notifier/cmd/notifier/main.go:77`, `services/payments-worker/cmd/payments-worker/main.go:122`, `services/plans-svc/cmd/plans-svc/main.go:119`
  - `otelx.Setup` + `providers.Shutdown` defer: aggregator `main.go:94/103`, analytics-svc `:73/82`, assets-svc `:83/92`, billing-svc `:126/135`, gateway `:124/133`, ingest-svc `:94/103`, invoice-worker `:65/74`, limits-svc `:104/113`, notifier `:86/95`, payments-worker `:131/140`, plans-svc `:129/138`
  - `errgroup.WithContext` + `health.Serve`: aggregator `:227/228`, analytics-svc `:134/135`, assets-svc `:143/144`, billing-svc `:346/347`, gateway `:262/263`, ingest-svc `:171/172`, invoice-worker `:97/98`, limits-svc `:181/182`, notifier `:148/149`, payments-worker `:211/212`, plans-svc `:291/292`
  - `grpcx.NewServer` + tenant server interceptor'ları: aggregator `:196/199`, analytics-svc `:122/125`, assets-svc `:131/134`, billing-svc `:306/309`, ingest-svc `:158/161`, limits-svc `:168/171`, plans-svc `:256/259`
  - `NOT_SERVING` + `GracefulStop`: aggregator `:238/239`, analytics-svc `:143/144`, assets-svc `:152/153`, billing-svc `:357/358`, ingest-svc `:182/183`, limits-svc `:196/197`, plans-svc `:306/307`
  - `fmt.Fprintln(os.Stderr, ...)` + `os.Exit(1)`: aggregator `:74`, analytics-svc `:54`, assets-svc `:64`, billing-svc `:106`, gateway `:104`, ingest-svc `:74`, invoice-worker `:46`, limits-svc `:79`, notifier `:67`, payments-worker `:112`, plans-svc `:109`
  - **Öneri:** Yeni bir `pkg/app` (ya da `pkg/servicex`) paketi: `app.Main(name string, run func(ctx context.Context, rt *app.Runtime) error)` signal, logx, otelx kurulum/kapatma, health registry ve `health.Serve`'ü yönetsin; `rt.Go(fn)` errgroup'a iş eklesin. gRPC sunan servisler için `rt.ServeGRPC(addr, register func(*grpc.Server))` tenant interceptor'larını, listen işlemini ve `NOT_SERVING`/`GracefulStop` sırasını tek yerde uygulasın. Böylece her `main.go`'da yalnızca servise özgü bağlantı kodu kalır (~60–100 satır azalır). Kapanış sırası (önce `NOT_SERVING`, sonra `GracefulStop`) da bu sayede tek yerde düzeltilir.

### 2. Postgres migrasyon + app/system pool açılışı ve ilgili Config alanları

- **[Orta]** `migrate()` ve `openPools()` aggregator, billing-svc ve plans-svc'de satır satır aynı (`diff` çıktısında fark yalnızca bir yorumdaki servis adı). payments-worker'da `migrate()` var; ingest-svc, assets-svc ve notifier aynı mantığı `run()` içinde yazıyor. `"migrations applied; exiting (PAYRUDDER_MIGRATE_ONLY)"` mesajı 8 serviste var. `MigrateOnStart && PGMigratorDSN == ""` kontrolü 7 yerde tekrar ediyor; assets-svc bunu `Validate` yerine `run()` içinde yapıyor, yani davranış tutarsız. `PGAppDSN`, `PGSystemDSN`, `PGMigratorDSN` ve `MigrateOnStart` env alanları da her Config'te ayrı ayrı tanımlı.
  - `migrate()`: `services/aggregator/cmd/aggregator/main.go:110`, `services/billing-svc/cmd/billing-svc/main.go:142`, `services/plans-svc/cmd/plans-svc/main.go:145`, `services/payments-worker/cmd/payments-worker/main.go:147`
  - `openPools()`: `services/aggregator/cmd/aggregator/main.go:124`, `services/billing-svc/cmd/billing-svc/main.go:156`, `services/plans-svc/cmd/plans-svc/main.go:159`
  - Satır içi kopyalar: `services/ingest-svc/cmd/ingest-svc/main.go:106-125`, `services/assets-svc/cmd/assets-svc/main.go:95-116`, `services/notifier/cmd/notifier/main.go:99-107`
  - Migrator DSN kontrolü: aggregator `main.go:64`, assets-svc `:97` (run içinde), billing-svc `:90`, ingest-svc `:61`, notifier `:60`, payments-worker `:94`, plans-svc `:82`
  - Env alanları (`PAYRUDDER_PG_*`, `PAYRUDDER_MIGRATE_ON_START`): aggregator `main.go:48-51`, assets-svc `:42-45`, billing-svc `:57-60`, ingest-svc `:46-49`, notifier `:41-43`, payments-worker `:56-58`, plans-svc `:55-58`
  - **Öneri:** `pkg/pgxx` içine gömülebilir bir `pgxx.Settings` struct'ı (`AppDSN`, `SystemDSN`, `MigratorDSN`, `MigrateOnStart`, `Validate()`) ekleyin. Yanına `pgxx.MigrateIfAsked(ctx, s, fs, migrateOnly, log) (done bool, err error)` ve `pgxx.OpenPools(ctx, s) (app, system *pgxpool.Pool, closeFn, err)` gelsin. Servis Config'leri `config.Base` gibi `pgxx.Settings`'i gömsün.

### 3. Tenant interceptor'lı gRPC istemci kurulumu (`dialPeer` / `dialBackends` / `dialUpstreams`)

- **[Orta]** `grpcx.NewClient(addr, grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()))` 7 serviste elle kuruluyor. billing-svc'deki `dialBackends` ile gateway'deki `dialUpstreams` aynı kalıbı kullanıyor (hedef tablosu, hata olursa açılmış bağlantıları kapatan `defer`, ters sırada `close()`). payments-worker aynı şeyi closure ile, limits-svc `dialPeer` ile yapıyor. Hata metni (`"%s client: %w"`) da her yerde aynı.
  - `services/limits-svc/cmd/limits-svc/main.go:208` (`dialPeer`)
  - `services/billing-svc/cmd/billing-svc/main.go:239-275` (`dialBackends` + `close`)
  - `services/gateway/cmd/gateway/main.go:164-203` (`dialUpstreams` + `close`)
  - `services/payments-worker/cmd/payments-worker/main.go:238-252` (`dial` closure + `closeConns`)
  - `services/ingest-svc/cmd/ingest-svc/main.go:132`, `services/notifier/cmd/notifier/main.go:113`, `services/aggregator/cmd/aggregator/main.go:149`
  - **Öneri:** `pkg/grpcx` içine `grpcx.NewTenantClient(name, addr string, stream bool)` ve bir `grpcx.Pool` ekleyin (`p.Dial(name, addr, opts...)`, `p.Close()` ters sırada kapatsın, ilk hatada açılanları kapatsın). Tenant forwarding varsayılan olarak açık gelsin.

### 4. plans-svc meter önbelleği (`meters.Cache`) ve `toDomain` dönüşümü

- **[Yüksek]** `services/aggregator/internal/meters/meters.go` ile `services/limits-svc/internal/meters/meters.go` neredeyse birebir aynı: `expirable.LRU(10000)` + `singleflight`, `tenantID + "/" + code` anahtarı, 3 sn'lik `WithoutCancel` timeout, `tenant.OutgoingContext(tenant.NewContext(...))`, `NotFound` → `ErrNotFound` eşlemesi. Tek fark, limits-svc'nin `toDomain` içinde `Scale` alanını da doldurması. ingest-svc ise aynı problemi generic bir `cache[V]` ile (hit/miss TTL ayrı) zaten daha iyi çözmüş, ama bu kod `internal` olduğu için diğer servisler kullanamıyor.
  - `services/aggregator/internal/meters/meters.go:23-61` (Cache/New/Get), `:64-81` (`toDomain`)
  - `services/limits-svc/internal/meters/meters.go:28-67` (Cache/New/Get), `:69-86` (`toDomain`)
  - `services/ingest-svc/internal/lookup/lookup.go:53-102` (generic `cache[V]`, `get`)
  - Aggregation enum eşlemesi ayrıca şurada da var: `services/plans-svc/internal/domain/catalog.go:26,41`
  - **Öneri:** ingest'teki generic `cache[V]`'yi `pkg/lookupcache` (ya da `pkg/cachex`) olarak dışa açın: `cachex.New[V](Options{Size, HitTTL, MissTTL, LoadTimeout}, load)`, `Get(ctx, tenantID, key)`. Meter domain tipi ve `plansv1.Meter` → `Meter` dönüşümü (Aggregation, Scale, Filter) `pkg/meter` altında toplansın. aggregator, limits-svc ve ingest-svc bunu kullansın.

### 5. Tenant ayarları (saat dilimi, grace) TTL önbelleği ve `GetTenant` + `LoadLocation` kalıbı

- **[Yüksek]** `services/aggregator/internal/settings/settings.go` içindeki `Source` ile `services/limits-svc/internal/tenants/tenants.go` içindeki `Cache` aynı kod: `sync.Mutex` + sınırsız `map[uuid.UUID]` + `expires`, `ttl<=0 → time.Minute`, aynı loader (`tenant.OutgoingContext(tenant.WithOperator(ctx))`, `admin.GetTenant`, `time.LoadLocation`) ve aynı hata metinleri. İkisinde de singleflight yok (aynı anda gelen miss'ler plans-svc'ye yük bindiriyor) ve map hiç boşaltılmıyor. billing-svc'de aynı "GetTenant → LoadLocation" kodu, önbellek olmadan, en az 6 yerde tekrar ediyor. `operatorCtx` yardımcısı yalnızca billing'de tanımlı; diğer servisler aynı ifadeyi satır içinde yazıyor.
  - `services/aggregator/internal/settings/settings.go:26-85`
  - `services/limits-svc/internal/tenants/tenants.go:28-86`
  - billing-svc `GetTenant` + `LoadLocation`: `services/billing-svc/internal/runs/rate.go:320-328`, `rerate.go:210-215`, `scheduler.go:96-101`, `links.go:81-86`, `fees.go:56-61`, `einvoice.go:47`
  - gateway: `services/gateway/internal/httpapi/dashboard.go:567-581`, `overview.go:384`
  - `tenant.OutgoingContext(tenant.WithOperator(ctx))` satır içi: `services/payments-worker/internal/webhook/plans.go:79,88`, `services/payments-worker/internal/transfers/handler.go:209`, `services/notifier/internal/limits/limits.go:94`; yardımcı olarak: `services/billing-svc/internal/runs/manager.go:343`
  - **Öneri:** `pkg/tenantinfo` (ya da `pkg/tenant` altında `tenant.Settings`) paketi açılsın: `Settings{Timezone, Location, Grace, Currency, Environment}`, bir `Loader(admin plansv1.AdminServiceClient)` ve madde 4'teki `cachex` üzerine kurulmuş TTL+singleflight önbelleği. `tenant.OperatorOutgoing(ctx)` gibi tek satırlık yardımcı da `pkg/tenant`'a eklenip `operatorCtx` kopyalarının yerine geçsin.

### 6. `usage.events.v1` çözümleme (`Decode`) ve `Meter.Matches`/`Value` kuralları

- **[Yüksek]** aggregator ve limits-svc `domain` paketlerinde aynı `Event` struct'ı, aynı `periodRe`/`eventIDRe`, aynı doğrulama zinciri (tenant/customer UUID, event_id, meter_code, period_id, scale ≤ 6, units ≥ 0, ingested_at) ve aynı `Meter.Matches`/`Meter.Value` var. limits-svc'deki yorum bunu açıkça söylüyor: "It mirrors the aggregator's rules on purpose: both must count the same events for reconciliation to converge." Yani kurallar ayrıştığında mutabakat (reconciliation) bozulur, bu da en riskli kopyayı yapıyor. Şimdiden bir fark var: aggregator `event_time` alanını zorunlu tutuyor, limits-svc tutmuyor. analytics-svc üçüncü, daha gevşek bir sürüm kullanıyor (event_id regex yok, period kontrolü yok). `DecodeError{Reason, Err}` tipi de aggregator ve analytics'te ayrı ayrı tanımlanmış.
  - `services/aggregator/internal/domain/event.go:19-30` (Event), `:33-35` (regex), `:38-45` (DecodeError), `:48-87` (Decode), `:106-133` (Matches/Value)
  - `services/limits-svc/internal/domain/domain.go:18-28`, `:31-33`, `:37-72` (Decode), `:90-116` (Matches/Value)
  - `services/analytics-svc/internal/consumer/consumer.go:140-158` (`decodeError`, `decodeUsage`)
  - **Öneri:** `pkg/usage` paketi açılsın (veya `pkg/kafkax/usage.go` yanına): `usage.Event`, `usage.Decode(rec) (Event, error)` (`*usage.DecodeError{Reason}` döner; Reason değerleri `DECODE_ERROR`/`INVALID_EVENT` sabit olur) ve `usage.Meter{Code, Aggregation, Scale, Filter}` ile `Matches`/`Value`. Her iki servis aynı kodu çağırırsa "kasıtlı ayna" bir derleme garantisine dönüşür. analytics-svc de aynı doğrulamayı kullanabilir.

### 7. Doğrulama regex'leri: period `YYYY-MM`, event id, ülke/para birimi kodu

- **[Orta]** `^[0-9]{4}-(0[1-9]|1[0-2])$` 9 ayrı yerde derleniyor. `pkg/period` ise `Bounds()` içinde aynı kuralı `Sscanf` ile ayrıca uyguluyor ama dışa açık bir `Valid()` sunmuyor, billing-svc de doğrulama için `period.Bounds(pid, time.UTC)` kullanıyor. `"period_id must look like 2026-11"` hata metni de birkaç serviste kopyalanmış. `^[A-Z]{2}$` (ülke) 4 yerde, `^[A-Z]{3}$` (para birimi) 2 yerde tekrar ediyor.
  - period: `services/aggregator/internal/domain/event.go:34`, `services/aggregator/internal/query/query.go:28`, `services/aggregator/internal/replay/replay.go:61`, `services/limits-svc/internal/domain/domain.go:32`, `services/limits-svc/internal/query/query.go:27`, `services/billing-svc/internal/reports/query/query.go:20`, `services/gateway/internal/httpapi/usage.go:14`, `services/payments-worker/internal/webhook/reconcile.go:84`, `services/plans-svc/internal/reports/query/query.go:24` (capture group'lu)
  - period doğrulaması `Bounds` ile: `services/billing-svc/internal/billingruns/command/command.go:31`
  - event id: `services/aggregator/internal/domain/event.go:35`, `services/limits-svc/internal/domain/domain.go:33`
  - ülke: `services/plans-svc/internal/domain/tenant.go:215`, `services/plans-svc/internal/domain/routing.go:19`, `services/plans-svc/internal/tax/command/command.go:18`, `pkg/einvoice/ubl/build.go:45`
  - para birimi: `pkg/einvoice/ubl/build.go:44`, `pkg/payments/banktransfer/banktransfer.go:48`
  - **Öneri:** `pkg/period` içine `period.Valid(id string) bool` ve `period.Parse(id) (year, month int, err error)` eklenip tüm regex'ler kaldırılsın. Event id kuralı madde 6'daki `pkg/usage` paketine taşınsın. Ülke kodu `pkg/address` içinde `address.ValidCountry`, para birimi `pkg/money` içinde `money.ValidCode` olsun (ya da doğrudan `money.LookupCurrency` kullanılsın).

### 8. "Varsayılan dönem = tenant saat diliminde bugün, yoksa UTC" kalıbı

- **[Orta]** `loc := time.UTC; if tz != "" { if l, err := time.LoadLocation(tz); err == nil { loc = l } }; period.PeriodOf(now, loc)` bloğu üç serviste aynen tekrarlanıyor. Bu kod geçersiz bir saat dilimini sessizce UTC'ye düşürüyor; madde 5'teki tenant önbelleği ise aynı durumda hata döndürüyor. İki yaklaşım birbiriyle tutarsız.
  - `services/limits-svc/internal/query/query.go:52-58`
  - `services/billing-svc/internal/billingruns/command/command.go:23-30`
  - `services/gateway/internal/httpapi/usage.go:31-38`
  - **Öneri:** `pkg/period` içine `period.Current(now time.Time, tz string) string` (ya da `tenant.Info`'dan `ti.Location()`) eklensin. Bozuk saat dilimi politikası (UTC'ye düşmek mi, hata vermek mi) tek yerde belirlensin.

### 9. gRPC hata yardımcıları: UUID ayrıştırma + `InvalidArgument`, `invalid()`, `internal()`, `notFound()`

- **[Orta]** `uuid.Parse` + `status.Error(codes.InvalidArgument, field+" must be a UUID")` kalıbı 53 yerde geçiyor (billing-svc 23, plans-svc 12, aggregator 6, limits-svc 4, payments-worker 4, analytics-svc 2, assets-svc 1, gateway 1). 5 ayrı `parseID`/`parseUUID` yardımcısı var. `invalid(msg)` 6 pakette tek satırlık aynı fonksiyon. `internal()` 6 kez, birbirinden farklı imzalarla tanımlı: `internal()`, `internal(err)`, `internal(error)`. Bazıları `status` hatalarını olduğu gibi geçiriyor, bazıları geçirmiyor. Proje zaten `pkg/errkey` ile `errkey.New(codes.X, key, msg)` sunuyor, ama bu yardımcılar onu kullanmıyor.
  - `parseID`/`parseUUID`: `services/plans-svc/internal/admin/admin.go:199`, `services/plans-svc/internal/webhookendpoints/query/query.go:26`, `services/assets-svc/internal/service/service.go:188`, `services/billing-svc/internal/einvoices/handler.go:66`, `services/billing-svc/internal/hosted/handler.go:63`
  - Satır içi örnekler: `services/aggregator/internal/replay/service.go:28,35`, `services/payments-worker/internal/transfers/handler.go:48,51`, `services/billing-svc/internal/sagas/handler.go:32,35`, `services/billing-svc/internal/payments/handler.go:38,42`, `services/gateway/internal/httpapi/admin.go:23`
  - `invalid(msg)`: `services/aggregator/internal/query/query.go:51`, `services/limits-svc/internal/query/query.go:44`, `services/analytics-svc/internal/api/api.go:38`, `services/billing-svc/internal/einvoices/handler.go:40`, `services/billing-svc/internal/rerating/handler.go:27`, `services/billing-svc/internal/creditnotes/handler.go:33`
  - `internal(...)`: `services/assets-svc/internal/service/service.go:131`, `services/billing-svc/internal/hosted/command/command.go:99`, `services/billing-svc/internal/accounting/command/command.go:212`, `services/billing-svc/internal/accounting/query/query.go:231`, `services/billing-svc/internal/credits/command/command.go:401`, `services/billing-svc/internal/credits/query/query.go:179`
  - **Öneri:** `pkg/grpcx` (ya da `pkg/errkey`) içine `grpcx.ParseUUID(field, v string) (uuid.UUID, error)`, `grpcx.Invalid(msg)`, `grpcx.Internal(log, err)` (status hatalarını geçirsin, gerisini loglayıp `Internal` döndürsün) ve `grpcx.NotFoundIfNoRows(err, msg)` eklenmeli (`pgx.ErrNoRows` eşlemesi billing 53, plans 42 kez geçiyor). Hata anahtarları `errkey` ile uyumlu olsun.

### 10. Postgres tabanlı Kafka offset deposu

- **[Düşük]** aggregator ve notifier'da `kafkax.OffsetLoader` aynı biçimde yazılmış: sqlc `LoadOffsets` çağrısı, sonra `map[int32]int64` doldurma. Arkasındaki tablo (`consumer_offsets` / `kafka_offsets`) ve SQL sorgusu da aynı şemaya sahip, yalnızca adları farklı. limits-svc offset'leri Redis'te tutuyor, bu bilinçli bir fark.
  - `services/aggregator/internal/apply/apply.go:136-148`, `services/aggregator/migrations/00001_init.sql:60-67`, `services/aggregator/internal/store/queries/aggregator.sql:1-4`
  - `services/notifier/internal/store/store.go:112-135`, `services/notifier/migrations/00001_init.sql:16-23`, `services/notifier/internal/store/queries/offsets.sql:3-11`
  - **Öneri:** `pkg/kafkax` içine `kafkax.PGOffsets{DB, Group, Table}` (Load/Save, `GREATEST` ile geri gitmeyen upsert) ve paylaşılan bir DDL sabiti eklensin. Tablo adı da tek bir isimde birleştirilsin.

### 11. Outbox tablo DDL'i

- **[Düşük]** `pkg/outbox` sabit bir şema bekliyor (`outbox.go:26-33`: `id, tenant_id, topic, partition_key, payload, headers, ingested_at`). Buna rağmen her servis tabloyu kendi migrasyonunda farklı biçimde tanımlıyor: billing'de `message_id` sütunu var; ingest'te `payload NOT NULL`, `headers` default'suz; plans-svc ise `created_at` ile başlayıp sonraki bir migrasyonda sütunu `ingested_at` olarak yeniden adlandırmak zorunda kalmış.
  - `services/billing-svc/migrations/00001_init.sql:122-131`, `services/ingest-svc/migrations/00001_init.sql:30-39`, `services/plans-svc/migrations/00001_init.sql:141-149`, `services/plans-svc/migrations/00002_outbox_relay.sql:4`
  - **Öneri:** `pkg/outbox` içinde `outbox.SchemaSQL` (ya da `embed` edilmiş bir goose migrasyonu) yayınlansın ve servis migrasyonları buna referans versin. Relay'in beklediği sütunlar da bir test ile şemaya bağlansın.

### 12. `_ "time/tzdata"` importları (`pkg/tzdata` varken)

- **[Düşük]** `pkg/tzdata` tam olarak bu iş için var ve 6 servisin `main.go` dosyası onu import ediyor. Yine de 9 yerde doğrudan `_ "time/tzdata"` duruyor. ingest-svc ve plans-svc `main.go` dosyalarında ikisi birden import edilmiş. `services/analytics-svc/internal/api/api.go:8` ise import ettiği halde hiç `time.LoadLocation` çağırmıyor, yani orada gereksiz.
  - `services/ingest-svc/cmd/ingest-svc/main.go:16` (+`:33` pkg/tzdata)
  - `services/plans-svc/cmd/plans-svc/main.go:16` (+`:38` pkg/tzdata)
  - `services/limits-svc/internal/tenants/tenants.go:9`, `services/limits-svc/internal/query/query.go:10`
  - `services/aggregator/internal/settings/settings.go:9`
  - `services/analytics-svc/internal/api/api.go:8`
  - `services/billing-svc/internal/api/api.go:7`, `services/billing-svc/internal/runs/manager.go:13`
  - `services/gateway/internal/httpapi/usage.go:7`
  - **Öneri:** Hepsi `pkg/tzdata`'ya çevrilsin; tercihen yalnızca `main.go`'da kalsın (madde 1'deki `pkg/app` bunu kendisi import edebilir). analytics-svc'deki gereksiz import silinsin. `depguard`/`forbidigo` ile doğrudan `time/tzdata` importu yasaklanabilir.

### 13. Chaos fault hook'ları (`chaos.go` / `nochaos.go`)

- **[Düşük]** 4 serviste aynı 30 satırlık fonksiyon kopyalanmış: env değişkeni oku, `[0,1]` aralığında olasılık olarak ayrıştır, `slog.Warn` yaz, `rand.Float64() < p` ise ya `os.Exit(137)` ya da hata döndür. `nochaos.go` stub'ları da aynı.
  - `services/aggregator/cmd/aggregator/chaos.go:15-33`, `nochaos.go:5`
  - `services/billing-svc/cmd/billing-svc/chaos.go:16-33`, `nochaos.go:5`
  - `services/ingest-svc/cmd/ingest-svc/chaos.go:16-33`, `nochaos.go:5`
  - `services/limits-svc/cmd/limits-svc/chaos.go:16-34`, `nochaos.go:5`
  - **Öneri:** `//go:build chaos` etiketli bir `pkg/chaos` paketi açılsın: `chaos.Crash(env, msg string) func() error` ve `chaos.Fail(env, msg string) func() error`; `!chaos` sürümü `nil` döndürsün. Servislerde yalnızca çağrı satırı kalır.

### 14. Servis Dockerfile'ları

- **[Orta]** 11 `services/*/Dockerfile` servis adı normalize edildiğinde yalnızca iki varyanta iniyor (9 tanesi md5 olarak aynı). gateway ve plans-svc'nin tek farkı `ARG GO_TAGS` / `-tags "$GO_TAGS"` satırlarının olmaması. Dosyanın kendi yorumu da "copy for new services" diyor. `deploy/railway/go.Dockerfile` ise `SERVICE_DIR` ARG'ı ile aynı işi tek dosyada zaten yapıyor; aradaki tek fark BuildKit cache mount'ları. Golang imaj sürümü, distroless tabanı ve ldflags şu anda 12 dosyada ayrı ayrı güncellenmek zorunda.
  - `services/aggregator/Dockerfile`, `services/analytics-svc/Dockerfile`, `services/assets-svc/Dockerfile`, `services/billing-svc/Dockerfile`, `services/ingest-svc/Dockerfile`, `services/invoice-worker/Dockerfile`, `services/limits-svc/Dockerfile`, `services/notifier/Dockerfile`, `services/payments-worker/Dockerfile` (aynı)
  - `services/gateway/Dockerfile:6,18`, `services/plans-svc/Dockerfile:6,18` (`GO_TAGS` yok)
  - `deploy/railway/go.Dockerfile:1-24`
  - **Öneri:** Tek bir `build/go.Dockerfile` kullanılsın (`ARG SERVICE`, `ARG GO_TAGS=`, cache mount'lu). compose/helm/Makefile bunu `--build-arg SERVICE=...` ile çağırsın. Railway varyantı aynı dosyadan bir `--target` ya da build-arg ile cache mount'suz üretilsin.

### 15. Abonelik aralığı (`SubscriptionInterval`) enum eşlemeleri

- **[Orta]** `plansv1.SubscriptionInterval` ↔ `"month"`/`"year"`/`subscription.Interval` eşlemesi 5 serviste ayrı ayrı elle yazılmış. Bu sırada iki ayrışma oluşmuş:
  1. `services/gateway/internal/httpapi/dashboard.go:407`, `map[...]string{YEAR: "year"}[s.GetInterval()]` kullanıyor; aylık abonelikler için sonuç `"month"` yerine boş string oluyor.
  2. `dashboard.go:378-382` yenileme tarihini `base.AddDate(0, n, 0)` ile hesaplıyor. `pkg/subscription/interval.go:64` ise `addMonthsClamped` kullanıyor. 31 Ocak'ta başlayan bir abonelikte gateway 3 Mart gösterirken faturalama 28/29 Şubat'ı kullanır.
  - `services/payments-worker/internal/webhook/api.go:92-104,368-374`
  - `services/plans-svc/internal/convert/helpers.go:17`, `services/plans-svc/internal/subscriptions/command/create.go:61`
  - `services/billing-svc/internal/runs/fees.go:259` (`feeWord`)
  - `services/gateway/internal/httpapi/dashboard.go:378-382,407`
  - **Öneri:** `pkg/subscription` içine `IntervalFromProto`/`IntervalToProto`/`Interval.Word()` eklensin. gateway'deki yenileme tarihi hesabı `subscription.PeriodEnd(anchor, i, n, loc)` ile değiştirilsin.

### 16. Para / ondalık / baz puan biçimlendirme

- **[Düşük]** "Tamsayı minor/units değerini ondalığa çevir" işi üç ayrı yerde yazılmış: `pkg/quantity.FormatUnits`, `pkg/einvoice/ubl` içindeki `formatMinor` ve gateway'deki `formatMinor` (binlik ayırıcılı). Baz puanı yüzdeye çevirme de iki ayrı yerde var. `pkg/money` biçimlendirme fonksiyonu sunmuyor.
  - `pkg/quantity/quantity.go:77`
  - `pkg/einvoice/ubl/xmlw.go:86` (`formatMinor`), `:106` (`formatBps`)
  - `services/gateway/internal/httpapi/hosted.go:143` (`formatMinor`, binlik ayırıcılı)
  - `services/billing-svc/internal/rating/fee.go:64` (`percent`)
  - **Öneri:** `pkg/money` içine `money.FormatMinor(v int64, exp int) string`, `money.FormatMinorGrouped(...)` ve `money.FormatBps(bps, trimZeros bool)` eklensin. `quantity.FormatUnits` da aynı çekirdeği kullansın.

### 17. Küçük yardımcı fonksiyonlar ve el yazması sıralamalar (std lib yerine)

- **[Düşük]** Go 1.27.2 kullanılıyor, yani `cmp.Or`, `slices.SortFunc`, `slices.Sorted(maps.Keys(m))` ve `slices.Contains` hazır. Buna rağmen servisler arasında aynı mini fonksiyonlar tekrar tekrar yazılmış. Byte bazlı `truncate` sürümleri çok baytlı UTF-8 karakterleri ortadan bölebiliyor; doğru sürüm (`strings.ToValidUTF8` kullanan) yalnızca `pkg/einvoice/webhook` içinde.
  - `orDefault` (→ `cmp.Or`): `services/payments-worker/internal/einvoice/handler.go:239`, `services/billing-svc/internal/runs/rate.go:136`, `services/gateway/internal/httpapi/hosted_page.go:330`, `pkg/einvoice/webhook/webhook.go:151`, `pkg/blob/blob.go:66`
  - `deref(*string)`: `services/notifier/internal/store/store.go:79`, `services/billing-svc/internal/hosted/query/query.go:192`, `services/billing-svc/internal/saga/links.go:60`, `services/billing-svc/internal/runs/util.go:13`, `pkg/einvoice/nilvera/nilvera.go:263`
  - `abs(int64)`: `services/payments-worker/internal/einvoice/document.go:40`, `services/limits-svc/internal/engine/checkpoint.go:183`
  - `truncate`: `services/payments-worker/internal/einvoice/handler.go:78`, `services/billing-svc/internal/einvoices/command/command.go:55`, `services/plans-svc/internal/webhooks/dispatcher.go:220`, `pkg/einvoice/webhook/webhook.go:158`
  - `itoa` (→ `strconv.FormatInt`): `services/assets-svc/internal/policy/policy.go:135`, `services/gateway/internal/httpapi/paymentsclient.go:182`, `pkg/einvoice/ubl/build.go:536`
  - Elle yazılmış insertion sort (→ `slices.SortFunc`): `services/limits-svc/internal/entitlements/entitlements.go:190-194`, `services/billing-svc/internal/rating/rating.go:243-249`
  - `sortedKeys` (→ `slices.Sorted(maps.Keys(m))`): `services/payments-worker/internal/webhook/reconcile.go:237`
  - Elle yazılmış "contains" döngüsü (→ `slices.Contains`): `services/aggregator/internal/domain/event.go:106-124`, `services/limits-svc/internal/domain/domain.go:90-108`
  - **Öneri:** Mümkün olan yerlerde std lib'e geçilsin (`cmp.Or`, `slices.*`, `maps.Keys`, `strconv`). Geriye kalan `deref`, `abs` ve UTF-8 güvenli `truncate` için küçük bir `pkg/strx` (ya da `pkg/ptr`) yeterli. `truncate` tek bir UTF-8 güvenli sürümde birleştirilsin.

### 18. OTel metrik kayıt boilerplate'i

- **[Düşük]** 18 dosyada aynı kalıp tekrar ediyor: `otel.Meter("github.com/0furkancolak/payrudder/services/<svc>[/sub]")` ardından her sayaç için `if x, err = m.Int64Counter(name, metric.WithDescription(...)); err != nil { return err }`. aggregator bunu yerel bir `ctr := func(name, desc string)` closure'ı ile kısaltmış, diğer servisler kısaltmamış. Meter adı stringi de her yerde elle yazılıyor.
  - `services/aggregator/internal/apply/apply.go:109-115`, `services/analytics-svc/internal/consumer/consumer.go:80-83`, `services/limits-svc/internal/engine/apply.go:92-95`, `services/billing-svc/internal/saga/engine.go:156-159`, `services/assets-svc/internal/background/background.go:65-68`, `services/ingest-svc/internal/ingest/service.go:109-116`; ayrıca `billing-svc/internal/runs/manager.go`, `billing-svc/internal/replies/replies.go`, `gateway/internal/ratelimit/limiter.go`, `gateway/internal/auth/auth.go`, `limits-svc/internal/engine/checkpoint.go`, `payments-worker/internal/webhook/webhook.go`
  - **Öneri:** `pkg/otelx` içine `otelx.Meter(scope)` ve hata biriktiren bir builder eklensin: `b := otelx.NewInstruments(scope); c := b.Counter(name, desc); h := b.Histogram(...); if err := b.Err(); ...`.

### 19. Idempotency key doğrulaması

- **[Düşük]** Aynı kavram için her servis farklı kural uyguluyor: ingest-svc'de regex ve en fazla 128 karakter, billing-svc credits'te en fazla 128 (`maxKeyLen`), billing-svc hosted'da en fazla 200 (`maxKey`), assets-svc'de hiçbir sınır yok. Aynı müşteri farklı uç noktalara farklı anahtar kuralları ile karşılaşıyor.
  - `services/ingest-svc/internal/domain/domain.go:44,66-72`
  - `services/billing-svc/internal/credits/command/command.go:31`
  - `services/billing-svc/internal/hosted/command/command.go:45,135-136`
  - `services/assets-svc/internal/service/session.go:186`
  - **Öneri:** `pkg/idgen` (ya da yeni `pkg/idempotency`) içine `ValidateKey(key string) error` ve tek bir `MaxKeyLen` sabiti eklensin. Gateway'in `Idempotency-Key` header'ını okuması ve billing/assets/ingest tarafındaki doğrulama bunu kullansın.

### Özet

| # | Tema | Önem | Hedef paket |
|---|------|------|-------------|
| 1 | `main.go` bootstrap iskeleti (11 servis) | Yüksek | yeni `pkg/app` |
| 4 | Meter önbelleği + `toDomain` (aggregator/limits/ingest) | Yüksek | yeni `pkg/cachex` + `pkg/meter` |
| 5 | Tenant ayarları TTL önbelleği, `GetTenant`+`LoadLocation` | Yüksek | `pkg/tenant` / yeni `pkg/tenantinfo` |
| 6 | Usage event `Decode` + `Meter.Matches` (mutabakat riski) | Yüksek | yeni `pkg/usage` |
| 2 | Migrasyon + app/system pool + Config alanları | Orta | `pkg/pgxx` |
| 3 | Tenant interceptor'lı gRPC istemci kurulumu | Orta | `pkg/grpcx` |
| 7 | Period/event-id/ülke/para birimi regex'leri | Orta | `pkg/period`, `pkg/address`, `pkg/money` |
| 8 | "Tenant TZ'de güncel dönem, yoksa UTC" | Orta | `pkg/period` |
| 9 | UUID → InvalidArgument, `invalid`/`internal`/`notFound` | Orta | `pkg/grpcx` / `pkg/errkey` |
| 14 | Servis Dockerfile'ları (2 varyant, 12 dosya) | Orta | tek `build/go.Dockerfile` |
| 15 | `SubscriptionInterval` eşlemeleri (gateway'de iki ayrışma) | Orta | `pkg/subscription` |
| 10 | Postgres Kafka offset deposu | Düşük | `pkg/kafkax` |
| 11 | Outbox DDL | Düşük | `pkg/outbox` |
| 12 | `_ "time/tzdata"` importları | Düşük | `pkg/tzdata` |
| 13 | Chaos fault hook'ları | Düşük | yeni `pkg/chaos` |
| 16 | Para/ondalık/bps biçimlendirme | Düşük | `pkg/money` |
| 17 | `orDefault`/`deref`/`abs`/`truncate`/`itoa`, el yazması sıralamalar | Düşük | std lib + `pkg/strx` |
| 18 | OTel metrik kayıt boilerplate'i | Düşük | `pkg/otelx` |
| 19 | Idempotency key kuralları | Düşük | `pkg/idempotency` |

Öncelik sırası şöyle önerilir:

1. **Madde 6 (`pkg/usage`):** aggregator ile limits-svc'nin aynı olayları sayması mutabakatın doğruluğuna bağlı. Bu kopya şu an yalnızca bir yorumla korunuyor ve `event_time` kontrolünde ayrışmaya başlamış bile.
2. **Madde 4 ve 5 (önbellekler):** tenant önbelleklerinde singleflight yok ve map'ler sınırsız büyüyor.
3. **Madde 1 ve 2 (bootstrap ve DB kurulumu):** tekrar eden satır sayısının en büyük kısmı burada.
4. **Madde 15:** gateway'de aylık abonelikler için boş interval ve ay sonu kıstırması yapılmayan yenileme tarihi, gerçek kullanıcıya görünen hatalar.
