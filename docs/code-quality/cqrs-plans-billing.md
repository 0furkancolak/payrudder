## Modül tabanlı CQRS / ADR 0020 uyumu — plans-svc ve billing-svc

Kapsam: `services/plans-svc` ve `services/billing-svc` (testler ve `sqlc/` üretilmiş kod hariç). Ölçüt: [ADR 0020](../adr/0020-modules-cqrs-errors-config.md). Bütün bulgular kod okunarak doğrulandı. Sayımlar grep ile yapıldı.

**Genel olumlu tespitler (ihlal değil):**
- İki serviste de hiçbir `query/` paketi sqlc yazma sorgusu (INSERT/UPDATE/DELETE, outbox, `webhooks.Emit`, `tenantevents`) çağırmıyor.
- SQL'in tamamı sqlc ile üretiliyor. Toplamı ikinci bir sorguyla hesaplayan liste yok (`ListEInvoiceDocuments` `count(*) OVER()` kullanıyor).
- YAML'da secret yok.
- Kodda geçen 109 error key'in hepsi `docs/error-keys.md` içinde var. Belgede olup kodda olmayan plans/billing key'i de yok.

---

### plans-svc

**Query yazıyor / command aslında okuma**
- **[Orta]** `services/plans-svc/internal/identity/grpc.go:82` — `GetSession` bir okuma RPC'si. Ama handler, query'nin döndürdüğü `s.Slide` bayrağına bakıp `h.cmd.Slide(...)` ile oturumun süresini yazıyor. Yani bir read yolu yazma yapıyor ve "yazalım mı" kararı handler'da veriliyor. **Öneri:** Kararı ve yazmayı bir command'a taşıyın (`command.TouchSession`). Ya da süre uzatmayı ayrı bir RPC/interceptor yapın ki `GetSession` saf okuma kalsın.
- **[Orta]** `services/plans-svc/internal/identity/command/members.go:414` — `LookupInvitation` command paketinde ama sadece okuyor (`GetInviteByToken`, `GetUser`, `GetUserByEmail`). **Öneri:** `identity/query/invitations.go` içine taşıyın.

**Modül sınırı ihlali**
- **[Yüksek]** `services/plans-svc/sqlc.yaml:5` / `internal/store/queries/plans.sql` — 12 modülün 75 sorgusu tek bir `plans.sql` dosyasında ve tek bir `store/sqlc` paketinde duruyor. ADR'ye göre her modülün kendi store sorguları olmalı. Bu düzen yüzünden her modül, başka bir modülün tablosuna doğrudan yazabiliyor (alttaki maddeler). **Öneri:** Sorguları modül başına `.sql` dosyalarına bölün (`internal/<modül>/store/*.sql`, sqlc.yaml'da modül başına bir `sql:` girdisi). Ya da en azından modül başına dosya açıp depguard ile "X modülü yalnız kendi sorgularını çağırır" kuralı koyun.
- **[Yüksek]** `services/plans-svc/internal/providermigrations/command/command.go:40-66` ve `internal/subscriptions/command/planchange.go:146-156` — İkisi de "abonelik halefi" kuralını (`EndSubscription` → `SetSubscriptionState` → `InsertSubscription` → entitlements) yeniden yazıyor. Oysa bu kural `changes/command.SwitchTo` (`changes/command/command.go:52`) olarak dışa açık. `subscriptions` tablosuna üç modül ayrı ayrı yazıyor ve kural kopyaları birbirinden ayrışmış durumda (örneğin `SwitchTo` kuponu taşıyor, `replacePlan` taşımıyor). **Öneri:** Halef oluşturmayı tablonun sahibi olan modülde, `subscriptions/command`'da tek bir exported command olarak toplayın. `changes`, `providermigrations` ve `ChangePlan` onu çağırsın.
- **[Orta]** `services/plans-svc/internal/fees/command/command.go:73` — `CountCouponPeriod`, `subscription_coupons` tablosuna fees modülünden doğrudan yazıyor. **Öneri:** `coupons/command` içine `CountPeriod(ctx, q, subID)` ekleyip onu çağırın.
- **[Orta]** `services/plans-svc/internal/changes/command/command.go:81` — `MoveSubscriptionCoupon` kupon tablosuna changes modülünden yazıyor. **Öneri:** `coupons/command.Move(...)` olsun.
- **[Orta]** `services/plans-svc/internal/subscriptions/query/query.go:29,38,48` (ve `coupons/query/query.go:32`, `webhookendpoints/query/query.go:26-46`, `providermigrations/query/query.go:24`) — Query paketleri `ParseID`, `Message` (proto mapper) ve `AttachExtras` için ortak yardımcı paket gibi kullanılıyor. Command'lar `subsqry.Message`'ı 15 kez, `subsqry.ParseID`'yi 12 kez çağırıyor. Bu, query→command bağımlılığını ters yönde kuruyor ve ADR'nin "query okur" tanımını aşıyor. **Öneri:** Mapper'ları `internal/convert`'e (ADR'nin izin verdiği mapping paketi) ya da modül içi bir `model`/`mapping` alt paketine taşıyın. Query paketinde yalnız okuma fonksiyonları kalsın.
- **[Orta]** `services/plans-svc/internal/identity/command/command.go:45-55,325` — identity modülü, modül dışındaki `admin` paketini import ediyor ve `admin.Server`'ı (bir gRPC server tipi) `AppFactory`/`KeyAdmin` olarak, `admin.ResolveSlug`'ı da kural olarak kullanıyor. Yani bir modül başka bir paketin transport tipine bağımlı. **Öneri:** `admin`'i `tenants`/`apps` modülüne dönüştürün (`command.CreateApp`, `command.IssueAPIKey`). identity bu exported command'ları çağırsın.
- **[Düşük]** `services/plans-svc/internal/plans/query/query.go:83,143,161`, `changes/query/query.go:73,292`, `coupons/query/query.go:77`, `changes/command/command.go:52,276`, `coupons/command/command.go:145`, `entitlements/command/command.go:26` — Modüller arası API'ler `*sqlc.Queries` alıyor. Paylaşılan transaction için bu kabul edilebilir, ama sqlc tipleri (`sqlc.GetSubscriptionRow`) modül sınırını geçiyor. **Öneri:** Modül API'lerinde sqlc satırı yerine modülün kendi tipini kullanın. Tx'i `store.Tx` gibi soyut bir tiple geçirin.

**Kalın handler**
- **[Yüksek]** `services/plans-svc/internal/admin/admin.go:255-324` — `UpdateTenantSettings` handler'ı doğrulamayı (`ValidateGrace`, `TierToDB`, logo/bank-transfer çakışma kuralları), parametre kurmayı ve DB yazmayı (`s.q().UpdateTenantSettings`) tek fonksiyonda yapıyor. `CreateAccount` (54), `CreateApp` (119), `ListTenants` (222), `IssueAPIKey` (343) ve `ResolveApiKey` (404) da aynı kalıpta. Bu, ADR'nin Context bölümünde tarif edilen eski düzenin kendisi. **Öneri:** `tenants`, `apps` ve `apikeys` için `command/` ve `query/` içeren modüller açın. `AdminService` handler'ları yalnız eşleme yapsın.
- **[Düşük]** `services/plans-svc/internal/customers/grpc.go:58` — `GetCustomer` id ile external_id arasındaki seçimi handler'da yapıyor. Küçük bir karar. **Öneri:** `qry.Get(ctx, req)` içine alın.

**Modül dışı kod**
- **[Orta]** `services/plans-svc/internal/admin/` — AdminService modül değil (command/query yok). Ayrıca `util.go:9` tek fonksiyonlu bir `convertTS` util'i taşıyor. **Öneri:** Yukarıdaki gibi modüllere bölün, `convertTS` yerine doğrudan `timestamppb.New` kullanın.
- **[Orta]** `services/plans-svc/internal/webhooks/` (`webhooks.go:64` `Emit`, `subscription_event.go:19` `EmitSubscription`, `dispatcher.go`) — Olay yazma (outbox benzeri) ve teslimat işçisi modül dışında, ortak bir paket olarak duruyor. 4 modülün command'ı bunu import ediyor. **Öneri:** `webhookendpoints` modülünün altına alın, ya da `webhooks` modülü olarak `command.Emit` / `worker.Dispatcher` şeklinde yapılandırın.
- **[Düşük]** `services/plans-svc/internal/convert/helpers.go:10-14` — `Str` ve `UUIDStr` genel helper'lar. ADR `convert`'i mapping paketi olarak tutuyor ama `convert.Err` (`convert.go:28-58`) gRPC hata üretiyor (aşağıya bakın). **Öneri:** Hata eşlemeyi ayrı bir `errs` katmanına alın, helper'ları kullanıldıkları yere taşıyın.
- **[Düşük]** `services/plans-svc/internal/grpcapi/grpcapi_test.go:12` — "Her metodun bir modülü var" testi yalnız `PlansService` için var. `WebhookService`, `IdentityService` ve `AdminService` kapsanmıyor. **Öneri:** Testi bu üç servise de genişletin.

**Transport sızıntısı (command/domain gRPC status döndürüyor)**
- **[Orta]** `services/plans-svc/internal/domain/violations.go:25-41`, `domain/subscription.go:22`, `domain/einvoice.go:26-53` — Domain paketi `status`/`errdetails`/`errkey` ile gRPC hatası üretiyor. **Öneri:** Domain tipli hata döndürsün (`ErrInvalid{Field, Key}`). gRPC'ye çevirmeyi handler katmanındaki tek bir mapper yapsın.
- **[Orta]** Command imzaları: örneğin `customers/command/command.go:26`, `changes/command/command.go:92` (`*plansv1.ChangeSubscriptionResponse` döndürüyor), `providermigrations/command/command.go:164`, `identity/command/members.go:527`. Neredeyse bütün command'lar `*plansv1.*Request` alıp proto döndürüyor. ADR'ye göre command "id veya hata" döndürmeli. Handler'lar ince görünüyor, çünkü eşleme command'ın içine kaymış. **Öneri:** Command'lar domain girdisi alsın ve id döndürsün. Okuma sonucunu handler query ile alsın, ya da command domain tipi dönsün. Proto eşlemesi `grpc.go`'da yapılsın.

**Error key eksikliği**
- **[Yüksek]** Key'siz istemci hataları:
  - 79 adet düz `status.Error/Errorf`, bunların 60'ı `codes.Internal` dışında. Temsili yerler: `subscriptions/command/lifecycle.go:64,99,102,139,263,292` (12 adet), `providermigrations/command/command.go:50,120-126,201-253` (9 adet), `coupons/command/command.go:128-212` (7 adet), `subscriptions/command/planchange.go:120-151` (6 adet), `changes/query/query.go:133-158` (5 adet), `fees/query/query.go:40,47`, `plans/query/query.go:156,181`, `customers/command/command.go:42`, `meters/command/command.go:53,61`, `tenants/query/query.go:20`, `admin/admin.go:370,410`, `domain/subscription.go:22`.
  - Bunlara ek olarak `convert.Err` (`convert/convert.go:36-52`, 123 çağrı noktası) NotFound/AlreadyExists/FailedPrecondition hatalarını key'siz üretiyor.
  - `domain.Invalid` / `Violations.Err` (`domain/violations.go:25-41`, yaklaşık 77 `Invalid(` ve 52 `v.Add` çağrısı) InvalidArgument'ı key'siz döndürüyor.

  Gateway bunlara yalnız `common.*` key'i veriyor (`gateway/internal/httpapi/errors.go:77`). **Öneri:** `convert.Err`'e `what`'tan türetilen key'i (`plans.<what>.not_found`) ekleyin. `Violations.Err`'e `<service>.<entity>.invalid` key'ini ekleyin. Tekrarlanan mesajları sabit key'li constructor'lara çevirin (`errSubscriptionEnded()` → `plans.subscription.ended`, `errConcurrent()` → `plans.subscription.conflict`).
- **[Düşük]** `domain/einvoice.go:26` (`einvoice.setting.invalid`) ve 25 adet `identity.*` key'i (`identity/command/*.go`) — ADR "service first" diyor (`plans.…`). `einvoice.*` iki serviste ortak kullanılıyor. **Öneri:** Yeniden adlandırma breaking değişiklik olduğu için ya ADR'ye istisna olarak yazın ya da deprecation kuralıyla `plans.identity.*` / `plans.einvoice.*` adlarına geçin.

**Config**
- **[Orta]** `services/plans-svc/cmd/plans-svc/main.go:66,76` — `PAYRUDDER_SECRETS_KEY` ve `PAYRUDDER_OTP_PEPPER` secret ama `required` değil. Pepper boşsa OTP hash'i anahtarsız kalıyor. ADR: "secrets … are required from the environment". **Öneri:** En azından `live` ortamında ikisini de zorunlu kılın (ya `,required` ya da `validate()` içinde ortam kontrolü).
- **[Düşük]** `services/plans-svc/internal/webhooks/dispatcher.go:55-63` ve `cmd/plans-svc/main.go:297` — Dispatcher için `Every=2s`, `Batch=50`, `Workers=8` ve HTTP timeout `10s` Go'ya gömülü, YAML'da değil. **Öneri:** `PAYRUDDER_WEBHOOK_*` ayarları olarak `config.default.yaml`'a taşıyın.

**SQL**
- **[Düşük]** `services/plans-svc/internal/store/queries/plans.sql:231` ve `webhooks.sql:46-47` — Opsiyonel filtreler `sqlc.narg` yerine sentinel değerle yazılmış (`'00000000-…'::uuid`, `@state::text = ''`). ADR örneği `sqlc.narg` kullanıyor. **Öneri:** `sqlc.narg('customer_id')::uuid IS NULL OR …` biçimine çevirin.
- **[Düşük]** `services/plans-svc/internal/plans/query/query.go:143-181` — `FeeIn` ve `RequireUsagePrices` bütün fiyat listesini çekip para birimi/interval filtresini Go'da yapıyor. **Öneri:** Filtreyi statement'a koyun (`WHERE currency = @currency AND interval = @interval`).

**Seremoni**
- Ciddi bir boş command/query paketi yok. `tenants` (yalnız query) ve `entitlements` (yalnız command) ADR'de açıkça izinli.

---

### billing-svc

**Query yazıyor / command aslında okuma**
- **[Orta]** `services/billing-svc/internal/dlq/dlq.go:97,120` (`deadletters/query/query.go:20` üzerinden) — `ListDeadLetters` "peek" ediyor ama bunu `ch.Get(..., autoAck=false)` sonra `Nack(requeue=true)` ile yapıyor. Bu, mesajları broker'da yeniden kuyruğa alıyor (redelivered bayrağı ve sıra değişiyor). Yani okuma yolu broker durumunu değiştiriyor. **Öneri:** Mesaj içeriğini yan etkisiz okuyan bir yol kullanın (RabbitMQ management API, ya da stream/quorum kuyruğunda consumer offset). Bu mümkün değilse `List`'i command olarak belgeleyin.
- **[Düşük]** `services/billing-svc/internal/hosted/command/command.go:467` — `UnpaidInvoices` yalnız okuyor, command paketinde duruyor ve hiçbir yerden çağrılmıyor. **Öneri:** Silin, ya da `hosted/query`'ye taşıyın.

**Modül sınırı ihlali**
- **[Orta]** `services/billing-svc/internal/credits/query/query.go:24,40` — Query paketi `credits/command`'ı import edip `command.Pack` tipini döndürüyor. Bu, query'yi command'a bağlıyor. **Öneri:** `Pack` gibi ortak tipleri `accounting/model` örneğindeki gibi `credits/model` alt paketine taşıyın.
- **[Orta]** `services/billing-svc/internal/saga/links.go:11,49` — Modül dışı saga motoru `credits/command.PurchasePaid`'i import ediyor. Bağımlılık ters yönde: alt katman bir modüle bağlı. **Öneri:** Saga'ya `PostPaidHook` arayüzü verin. Credits modülü bu hook'u wiring sırasında kaydetsin.
- **[Orta]** `services/billing-svc/internal/runs/linkexpiry.go:12,52` ve `hosted/command/command.go:30,475` — `sagas/command.Void` best-effort çağrılıyor ve dönen gRPC status hatası `_ , _ =` ile yutuluyor. `sagas/command` transport hatası döndürdüğü için modül içi çağıranlar da status kodlarıyla uğraşmak zorunda kalıyor. **Öneri:** `sagas/command`'ı domain hatası döndürecek şekilde ayırın, status eşlemesini handler yapsın.
- **[Orta]** `services/billing-svc/internal/invoices/handler.go:97` (`payments.Proto`) ve `hosted/handler.go:357` (`invoices.Proto`) — Bir modülün handler'ı başka bir modülün handler (transport) paketinden mapper import ediyor. **Öneri:** Proto mapper'larını paylaşılan bir `convert` paketine taşıyın, ya da her modül kendi mapper'ını yazsın.
- **[Orta]** `services/billing-svc/internal/hosted/handler.go:35-47` — Handler'ın `New` fonksiyonu `runs.Manager` üzerinden seller bilgisini çeken bir closure kuruyor. Ayrıca `codes.Unavailable` fallback kararı handler'da veriliyor. **Öneri:** Bu wiring `api.New`'a gitsin. Seller okuma, `query.Deps`'e arayüz olarak verilsin.
- **[Düşük]** `services/billing-svc/internal/hosted/command/command.go` (`SetInvoicePaymentMethod`) — invoices tablosuna hosted modülünden doğrudan yazılıyor. invoices modülünün command paketi yok. **Öneri:** `invoices/command.SetPaymentMethod` ekleyin.
- **[Düşük]** `services/billing-svc/sqlc.yaml:5` — Sorgular capability başına dosyalara ayrılmış, ama hepsi tek `store/sqlc` paketinde. Her modül her sorguyu çağırabiliyor. **Öneri:** plans-svc'deki öneriyle aynı.

**Kalın handler**
- **[Düşük]** UUID ayrıştırma ve hata kararı handler'larda yapılıyor, ayrıca tutarsız:
  - `credits/handler.go:64-67`: geçersiz UUID → `InvalidArgument` + `billing.credits.pack_not_found`.
  - `hosted/handler.go:138-141,173-176`: geçersiz UUID → `NotFound`.
  - `accounting/handler.go:150`: aynı şekilde `NotFound`.
  - Diğerleri `InvalidArgument` dönüyor: `payments/handler.go:36-43`, `sagas/handler.go:30-38`, `billingruns/handler.go:45-48,61-64`, `rerating/handler.go:27-60`, `creditnotes/handler.go:33-83`, `einvoices/handler.go:40,69`, `invoices/handler.go:43-46,60-63`, `hosted/handler.go:63-69`.

  **Öneri:** ID'yi ham string olarak command/query'ye verin. Ayrıştırmayı ve key'li hatayı (`billing.<entity>.bad_id` ya da `not_found`) orada tek yerde yapın.

**Modül dışı kod** (hangi capability'ye dönüşmeli)
- **[Yüksek]** `services/billing-svc/internal/runs/` (yaklaşık 2.700 satır) — Asıl iş kuralları burada. `billingruns`, `rerating` ve `creditnotes` modülleri bunun ince sarmalayıcıları. Dağılım şöyle olmalı:
  - `manager.go`, `scheduler.go`, `watermark.go`, `invoice.go`, `gauges.go`, `rate.go` → **billingruns** (`command/`, rating ve finalizasyon dahil).
  - `rerate.go` → **rerating/command**.
  - `creditnotes.go` → **creditnotes/command**.
  - `fees.go` → yeni bir **fees** (peşin ücret faturalama) modülü, ya da billingruns.
  - `links.go`, `linkexpiry.go` → **hosted** (paymentlinks).
  - `einvoice.go` → **einvoices**.
  - `tenantevents.go` → **tenantevents** relay worker'ı.
  - `providermail.go` → **sagas**/hosted.
  - `util.go:9-18` (`timestampOf`, `i32`, `deref`) bir util dosyası.

  **Öneri:** Dosyaları kademeli olarak ilgili modüllerin `command/` paketlerine taşıyın. `runs.Manager`'ı bağımlılık kabı olarak küçültün.
- **[Yüksek]** `services/billing-svc/internal/saga/` (`engine.go`, `engine_commands.go`, `einvoice.go`, `links.go`, `events.go`, `transfer.go`, `table.go`) — Fatura saga motoru modül dışında. `sagas` modülü yalnız operatör uçlarını sarıyor. **Öneri:** `sagas/` altına alın: `table.go` → `sagas/domain`, motor → `sagas/command`. `links.go` (payment-link ödendi) ve `einvoice.go` ilgili modüllerin hook'larına gitsin.
- **[Orta]** `services/billing-svc/internal/replies/replies.go` — Worker cevaplarını tüketen inbound adapter, modül dışında, `runs` ve `einvoices/command`'ı import ediyor. **Öneri:** `sagas/consumer` (ya da `sagas/command.ApplyReply`) olsun.
- **[Orta]** `services/billing-svc/internal/tenantevents/tenantevents.go` — Kendi yorumuna göre "saga ve credits birbirini import etmesin diye" var olan ortak bir yazma paketi. ADR "No shared service or utils package" diyor. **Öneri:** Bir `tenantevents` modülü olarak `command.Emit` exported fonksiyonu verin. Relay worker'ı (`runs/tenantevents.go`) da buraya taşıyın.
- **[Düşük]** `services/billing-svc/internal/rating/` — Saf fiyatlama kütüphanesi (I/O yok). Domain paketi olarak kabul edilebilir. **Öneri:** `billingruns/rating` altına alın, ya da ADR'ye "saf domain paketleri" istisnası yazın.
- **[Düşük]** `services/billing-svc/internal/dlq/` — Broker adapter'ı. **Öneri:** `deadletters/store` (adapter) olarak modülün altına alın.
- **[Düşük]** `services/billing-svc/internal/api/api.go` — Wiring için uygun, ama plans-svc'deki `TestEveryPlansMethodHasAModule` testinin karşılığı yok (dört servis için). **Öneri:** Aynı reflection testini `BillingService`, `SandboxService`, `BillingAdminService` ve `HostedInvoiceService` için ekleyin.

**Transport sızıntısı**
- **[Orta]** `services/billing-svc/internal/runs/scheduler.go:107,113` — Modül dışı iş paketi gRPC status döndürüyor. Ayrıca `scheduler.go:96-103`, plans-svc'nin `GetTenant` hatasını ve `time.LoadLocation` hatasını eşlemeden yukarı gönderiyor. `billingruns/command/command.go:40-43` bunu da olduğu gibi döndürüyor, yani upstream servisin gRPC kodu ve mesajı istemciye sızıyor. **Öneri:** `runs` sentinel hata döndürsün (`ErrBadPeriod`, `ErrPeriodOpen`). Eşlemeyi `billingruns/command` key'li olarak yapsın, upstream hatalarını `Unavailable`'a sarsın.
- **[Orta]** `services/billing-svc/internal/billingruns/command/command.go:46,50` — `err.Error()` istemci mesajına ekleniyor (`"run could not advance now…: "+err.Error()`), bu da iç hata metnini sızdırıyor. **Öneri:** Hatayı loglayın, istemciye sabit mesaj ve key döndürün.

**Error key eksikliği**
- **[Yüksek]** 118 düz `status.Error/Errorf` var, 74'ü `codes.Internal` dışında. Temsili yerler:
  - `hosted/command/command.go:232-518` (15 adet), `invoices/query/query.go:64-164` (11 adet), `hosted/query/query.go:249-441` (11 adet), `einvoices/query/query.go:62-128` (10 adet), `creditnotes/query/query.go:30-78` (7 adet), `reports/query/query.go:36-79` (6 adet).
  - `rerating/command/command.go:22-31`, `billingruns/command/command.go:20-50`, `billingruns/query/query.go:20`, `rerating/query/query.go:19`, `sagas/command/command.go:33,58`, `sagas/query/query.go:33`, `deadletters/command/command.go:18-34`, `deadletters/query/query.go:18-34`, `creditnotes/command/command.go:22,47` (kardeş hatalar key'li olduğu halde).
  - Handler'lardaki `invalid()` helper'ları (`einvoices/handler.go:40`, `rerating/handler.go:27`, `creditnotes/handler.go:33`).

  Aynı modülde key'li ve key'siz hatalar karışık. Örneğin `creditnotes/command/command.go:27-35` key'li ama `:47` key'siz. Hatta `:47` "credit note not found" için `runs.ErrInvoiceNotFound`'u eşliyor. **Öneri:** Her modüle küçük bir `errors.go` ekleyin (`errInvoiceNotFound = errkey.New(codes.NotFound, "billing.invoice.not_found", …)`). `page_size`, `page_token` ve UUID hataları için `billing.common.bad_page_token`, `billing.<entity>.bad_id` gibi key'ler tanımlayın.

**Config**
- **[Orta]** `services/billing-svc/internal/dunning/query/query.go:49-58` — Tenant'a "varsayılan" dunning gecikmeleri gösterilirken `saga.DefaultDunningLive/Sandbox` sabitleri kullanılıyor, konfigüre edilmiş `PAYRUDDER_DUNNING_LIVE/SANDBOX` değerleri değil. Operatör env ile değiştirirse API yanlış varsayılanı gösterir. **Öneri:** Engine'in etkin config'ini (`eng.DunningDefaults()`) `dunning` modülüne enjekte edin.
- **[Düşük]** `services/billing-svc/internal/saga/engine.go:120-125,142-152` (`StepTimeout=2m`, `TransferPoll=6h`, `DunningLive/Sandbox`) ve `internal/runs/manager.go:117-118` (`WatermarkDelay=30s`) — Go tarafındaki varsayılanlar `config.default.yaml`'daki `PAYRUDDER_STEP_TIMEOUT`, `PAYRUDDER_TRANSFER_POLL`, `PAYRUDDER_DUNNING_*` ve `PAYRUDDER_WATERMARK_DELAY` değerlerini kopyalıyor. İki kaynak birbirinden kayabilir. **Öneri:** Go'daki fallback'leri kaldırın, `NewEngine`/`NewManager` sıfır değeri hata saysın. Ya da test kodu için açıkça `TestConfig()` verin.
- **[Düşük]** `services/billing-svc/cmd/billing-svc/main.go:82-83` — `S3_ACCESS_KEY` ve `S3_SECRET_KEY` opsiyonel. S3 açık olduğunda zorunlu tutulmuyor. **Öneri:** `S3_ENDPOINT` set edilmişse `validate()` içinde zorunlu kılın.

**SQL**
- **[Düşük]** `services/billing-svc/internal/store/queries/links.sql:85` — `COALESCE(s.id, '00000000-…')` ile nullable alan sentinel'e çevriliyor. Filtre ihlali değil, ama `emit_pointers_for_null_types` varken gereksiz. **Öneri:** Nullable bırakın.

**Seremoni (boş ya da sarmalayıcı command/query)**
- **[Düşük]** `services/billing-svc/internal/payments/query/query.go:14-16` — Tek satırlık bir passthrough. Handler kullanmıyor, yalnız `invoices/query` kullanıyor. **Öneri:** Gerçek bir okuma (ödeme listesi RPC'si) gelene kadar `invoices/query` içinde tutun ya da olduğu gibi bırakın.
- **[Düşük]** `billingruns/query/query.go:17-26`, `rerating/query/query.go:16-25`, `rerating/command/command.go:18-34`, `creditnotes/command/command.go:20-54`, `billingruns/command/command.go:39-53` — Bunlar `runs.Manager` metotlarının hata eşleyen sarmalayıcıları. CQRS ayrımı biçimsel, kurallar `runs` içinde. **Öneri:** "Modül dışı kod" bölümündeki taşımayla birlikte kuralları bu command'lara indirin.

---

### Özet

| | Düz `status.Error/Errorf` | Bunların `Internal` dışı olanı | Ek key'siz kaynaklar |
|---|---|---|---|
| plans-svc | 79 | 60 | `convert.Err` (123 çağrı), `domain.Invalid` / `Violations.Err` (yaklaşık 77 + 52) |
| billing-svc | 118 | 74 | — |

- **Query yazma:** Hiçbir query paketi DB'ye yazmıyor. Yine de dört sorunlu yer var: plans `GetSession` handler'ı okuma RPC'sinde yazıyor; billing `ListDeadLetters` broker'da requeue yapıyor; `LookupInvitation` ve `UnpaidInvoices` command paketinde olduğu halde yalnızca okuyor.
- **Modül sınırı (en ciddi grup):** plans-svc'de tek bir sqlc paketi var ve `subscriptions` tablosuna üç modül yazıyor (halef kuralı üç kopya). Kupon tablosuna fees ve changes modülleri yazıyor. Query paketleri ortak mapper/util olarak kullanılıyor. billing-svc'de `credits/query` → `credits/command` ve `saga` → `credits/command` bağımlılıkları var; handler'lar başka modüllerin handler paketlerinden mapper import ediyor.
- **Modül dışı kod:** plans'ta `admin` (kalın AdminService) ve `webhooks`; billing'de `runs` (yaklaşık 2.700 satır iş kuralı), `saga`, `replies`, `tenantevents`, `dlq`, `rating`. billing'in `billingruns`, `rerating` ve `creditnotes` modülleri büyük ölçüde `runs` sarmalayıcısı; ADR uyumu orada biçimsel kalıyor.
- **Error key:** Kodda geçen key'lerin hepsi listede var. Asıl sorun key'siz istemci hatalarının çokluğu. Ayrıca `identity.*` ve `einvoice.*` key'leri "service first" kuralına uymuyor.
- **Transport sızıntısı:** Command'lar, domain paketleri ve `runs` gRPC status döndürüyor; command'lar "id veya hata" yerine proto döndürüyor. billing'de upstream hata metni ve `err.Error()` istemciye sızıyor.
- **ADR ile `errkey` çelişkisi:** `pkg/errkey/errkey.go:24` key'i `ErrorInfo.Metadata["key"]` içine koyuyor ve `Reason`'ı boş bırakıyor. ADR 0020 ise key'in `errdetails.ErrorInfo.reason` ile taşındığını söylüyor. Ya ADR'yi ya da `errkey.New`'u düzeltin.

**Önerilen öncelik sırası:**
1. plans: `subscriptions` tablosuna yazmayı tek modülde toplayın, sqlc'yi modül başına bölün.
2. billing: `runs` ve `saga` paketlerini modüllere taşıyın.
3. `convert.Err` ve `Violations` hatalarına key ekleyin, billing'de modül başına `errors.go` açın.
4. `dunning/query` varsayılanlarını konfigürasyona bağlayın.
5. `GetSession` ve `ListDeadLetters` okuma yollarını yan etkisiz hale getirin.
