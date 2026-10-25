## services/gateway

Kapsam: `services/gateway` altındaki tüm üretim dosyaları (cmd/gateway, internal/auth, internal/oidc, internal/ratelimit, internal/httpapi) baştan sona okundu; testler yalnızca test-kodu tekrarı için tarandı. "Kullanılmıyor" iddiaları `.claude/worktrees` ve `gen/` hariç repo genelinde grep ile doğrulandı.

### Kod tekrarı

- **[Yüksek]** `services/gateway/internal/httpapi/routes.go:255-262`, `routes.go:385-392`, `paymentlinks.go:79-86`, `webhooks.go:71-78`, `admin.go:30-37`, `admin.go:50-57`, `assets.go:140-147`, `billing.go:17-24`, `billing.go:233-240`, `credits.go:118-125`, `accounting.go:29-36`, `einvoice.go:18-24` — `page_size` sorgu parametresi 12 yerde aynı 7 satırla (`strconv.ParseInt(v,10,32)` + `invalid("page_size", …)`) ayrıştırılıyor; ikisi 1..200 aralığı kontrol ediyor, diğerleri etmiyor, yani aynı parametrenin kuralı route'a göre değişiyor. Aynı kalıp `limit` için de 5 kez var: `paymentlinks.go:130-138`, `payments_list.go:39-47` (burada `strconv.Atoi`), `admin.go:93-100`, `billing.go:178-185`, `analytics.go:67-74`. **Öneri:** `queryInt32(q url.Values, name string, min, max int32) (int32, bool, error)` gibi tek bir yardımcı yazın; sayfa boyutu sınırını tek sabitte toplayın.

- **[Yüksek]** `services/gateway/internal/httpapi/overview.go:345-376` ve `dashboard.go:537-563`, `dashboard.go:598-600` — `appOverview` ile `appDashboard` neredeyse aynı: `environment` parametresi (`"live"` varsayılanı, `live|sandbox` kontrolü), `GetMyApp` çağrısı, tenant seçme döngüsü, `identity.tenant.not_found` hatası, `principalOf(t)` ile tenant context'i ve timeout kurulumu birebir kopyalanmış. **Öneri:** `s.appTenant(w, r) (appID, env string, t *plansv1.Tenant, ok bool)` ve `tenantContext(ctx, t, timeout)` yardımcılarına çıkarın.

- **[Yüksek]** `services/gateway/internal/httpapi/overview.go:388-403` ↔ `dashboard.go:619-633`; `overview.go:407-421` ↔ `dashboard.go:679-693`; `overview.go:482-491` ↔ `dashboard.go:754-763`; `overview.go:509-515` ↔ `dashboard.go:942-948`; `overview.go:250-258` ↔ `dashboard.go:851-856` — overview ve dashboard'un "section" altyapısı (mutex + `down` listesi; biri `sync.WaitGroup`, diğeri `errgroup` ile), payments + provider-accounts bölümü, webhooks bölümü, `healthView` oluşturma ve "ledger'daki en eski satır" döngüsü kopyalanmış. Bir düzeltme yalnızca birine yapılırsa iki sayfa birbirinden ayrışır. **Öneri:** `sectionRunner` tipi (`run(name, fn)`, `wait() []string`), `s.paymentsHealth(ctx, tenantID)`, `s.webhookHealth(ctx)` ve `oldestOccurred(rows)` gibi ortak fonksiyonlar çıkarın.

- **[Yüksek]** `services/gateway/internal/httpapi/paymentsclient.go:66-86` ↔ `overview.go:164-176` — `ledgerRow` ve `ledgerEntry`, payments-worker ledger satırının neredeyse aynı iki modeli (`ID`/`AmountMinor` `json.Number`, aynı alanlar, ikisinde de `amount()`). Durum sınıflandırması da iki kez yazılmış: `refineState` (`paymentsclient.go:121-141`) ve `rank`/`foldLedger` (`overview.go:184-243`) `"declined","failed"` gibi değerleri ayrı ayrı ele alıyor. `payments_list.go:380-381` ise `ledgerRow.id()`'nin aynısını satır içinde yeniden yazıyor. **Öneri:** Tek bir `ledgerEntry` tipi (gerekirse `ProviderRef` alanıyla) ve `amount()`/`id()` metodları kullanın; durum eşlemesini (`succeeded/declined/failed/pending` → gösterilen durum) tek fonksiyonda tutun.

- **[Orta]** `services/gateway/internal/httpapi/server.go:390-406`, `session.go:212-228`, `events.go:61-69` — body okuma, `MaxBytesError` → `errTooLarge` eşlemesi ve boş body kontrolü üç kez birebir kopyalanmış (`decode`, `decodeJSON`, `parseEvents`); fark yalnızca son unmarshal adımında. **Öneri:** `readBody(r) ([]byte, error)` yardımcısını çıkarın; `decode` ve `decodeJSON` bu yardımcının üstüne birer satırlık sarmalayıcı olsun.

- **[Orta]** `services/gateway/internal/httpapi/recovery.go:20-50` ↔ `hosted.go:170-187` ↔ `hosted_page.go:83-92` — `supportedLocales` ve `hostedLangs` aynı liste (`en,tr,de,fr,es,ar`); `localeOf` ve `pickLang` aynı Accept-Language ayrıştırmasını iki farklı biçimde yapıyor (biri 200 karakterle kesiyor, diğeri kesmiyor); `langSuffix` aynı döngüyü üçüncü kez yazıyor. **Öneri:** Tek bir `languages` listesi ve tek bir `matchLanguage(asked, acceptLanguage)` fonksiyonu kullanın; listede arama için `slices.Contains`.

- **[Orta]** `services/gateway/internal/httpapi/usage.go:44`, `entitlements.go:55`, `entitlements.go:75`, `paymentlinks.go:139`, `billing.go:26`, `analytics.go:48` — `credits.go:54-61` içinde `s.customerID(w, r)` yardımcısı varken `ResolveCustomer` + hata yazma kalıbı 6 yerde daha satır içinde tekrarlanmış. **Öneri:** Yardımcıyı external id'yi parametre alacak şekilde genelleştirip (`s.resolveCustomer(w, r, externalID)`) her yerde kullanın.

- **[Orta]** `services/gateway/internal/httpapi/app.go:57-62`, `app.go:68-73`, `app.go:79-84`, `members.go:30-35`, `members.go:48-53`, `members.go:59-64`, `members.go:80-85`, `members.go:94-99`, `members.go:105-110`, `members.go:123-128` — `sess := sessionFrom(...)` + `accountOf(r, sess)` + hata yazma girişi 10 handler'da aynı. **Öneri:** `s.withAccount(func(w, r, sess session, accountID string))` sarmalayıcısını yazın, route tablosunda handler'ları bununla sarın.

- **[Orta]** `services/gateway/internal/httpapi/server.go:291-305` ↔ `internal/auth/auth.go:162-182` ↔ `dashboard.go:573-576` ↔ `app.go:121-130` ↔ `overview.go:347-353` — proto `Environment`/`TenantTier` ↔ string eşlemesi beş ayrı yerde, her seferinde `"free"`, `"standard"`, `"sandbox"`, `"live"` literalleriyle yeniden yazılmış. **Öneri:** `auth` paketindeki `envName`/`tierName` fonksiyonlarını dışa açın (veya `pkg/tenant` içine taşıyın) ve `principalOf` ile dashboard'da bunları kullanın; ters yön için `parseEnvironment(string)` ekleyin.

- **[Orta]** `services/gateway/internal/httpapi/payments.go:17-115`, `einvoice.go:121-131` — 15 proxy handler'ın her biri yalnızca bir path closure'ı ile `paymentsProxy` çağırıyor. Ayrıca `"/admin/tenants/" + url.PathEscape(...)` öneki burada ve `paymentsclient.go`, `overview.go:417`, `dashboard.go:689`, `hosted_page.go:567` içinde toplam yaklaşık 18 kez elle kuruluyor. **Öneri:** `s.proxyTenant(suffix string, pathVars ...string)` gibi tablo tabanlı tek bir fabrika kullanın; `httputil.ReverseProxy`'yi her istekte yeniden oluşturmak yerine `New` içinde bir kez kurun.

- **[Orta]** `services/gateway/internal/httpapi/paymentsclient.go:106-108`, `paymentsclient.go:165-167`, `paymentsclient.go:194-196`, `overview.go:417`, `dashboard.go:689`, `hosted_page.go:573-582` — `paymentsDo` `(status, error)` döndürdüğü için her çağıran `code/100 != 2` kontrolünü kendisi tekrar yazıyor. **Öneri:** 2xx dışı yanıtları `errPaymentsDown` sarmalı bir hata olarak döndüren bir `paymentsJSON(ctx, method, path, q, in, out) error` varyantı ekleyin; ham kodu yalnızca `invoicePay` gibi buna gerçekten ihtiyaç duyan yerler kullansın.

- **[Orta]** `services/gateway/internal/httpapi/billing.go:44-51` ↔ `admin.go:72-84`, `einvoice.go:43-50` ↔ `einvoice.go:77-89`, `einvoice.go:28-36` ↔ `einvoice.go:52-65`, `billing.go:38-41` ↔ `admin.go:62-70` — operatör ve tenant varyantları yalnızca kullandıkları context'te (`operatorAs(r)` ile `r.Context()`) ayrılıyor. Gövdeler kopyalanmış. **Öneri:** `s.asPathTenant(h http.HandlerFunc) http.HandlerFunc` gibi bir middleware ile operatör route'larını tenant handler'larının üstüne sarın.

- **[Orta]** `services/gateway/internal/httpapi/billing.go:50`, `admin.go:83`, `assets.go:163`, `einvoice.go:49`, `einvoice.go:88` — `{"url":…, "expires_at": …Format("2006-01-02T15:04:05Z07:00")}` yanıtı 5 kez kopyalanmış. Bu layout `time.RFC3339` ile aynı, ve `hosted.go:30` ile `paymentlinks.go:115` zaten `time.RFC3339` kullanıyor. **Öneri:** `writeSignedURL(w, url string, exp *timestamppb.Timestamp)` yardımcısı yazın ve `time.RFC3339` kullanın.

- **[Orta]** `services/gateway/internal/httpapi/hosted_page.go:489-503` ↔ `hosted_page.go:226-251` ve `portal.go:77-83` ↔ `hosted_page.go:176-187` — `receipt`, satıcı/müşteri/adres/tarih/satır/logo/test alanlarını `invoiceView`'ın kopyasıyla dolduruyor. `portalPage` de `stateOf`'u bir map literal hilesiyle (`map[string]string{"paid":"paid","void":"void"}[…]`) yeniden yazıyor. **Öneri:** Ortak bir `invoiceHeader(resp, sc, lang)` oluşturucu ve `paymentState(paymentState, status string)` fonksiyonu çıkarın.

- **[Orta]** `services/gateway/internal/httpapi/session.go:196-209` ↔ `signin_methods.go:78-94` — `me` ve `respondSignedIn` aynı "marshal → `withOperator` → hata olursa Internal → header + Write" dizisini tekrarlıyor. **Öneri:** `s.writeWithOperator(w, r, u *plansv1.User, msg proto.Message)` yardımcısını çıkarın.

- **[Orta]** `services/gateway/internal/httpapi/hosted.go:100-117` ↔ `session.go:129-136` — `hostedAllowed`, `hostedPayAllowed` ve `authAllowed` aynı "limit aşıldı → `Retry-After: 60` → 429" kuyruğunu kopyalıyor. `hostedPayAllowed` ayrıca `clientIP(r)` ve `time.Now()`'ı iki kez çağırıyor. **Öneri:** Tek bir `rejectRateLimited(w, r, jsonBody bool)` yardımcısı yazın; IP ve zamanı bir kez hesaplayın.

- **[Orta]** `services/gateway/internal/httpapi/usage.go:33-38`, `dashboard.go:581-584`, `overview.go:384-387` — "timezone yüklenemezse UTC kullan" kalıbı üç kez yazılmış. **Öneri:** `tenantLocation(tz string) *time.Location` yardımcısı ekleyin.

- **[Düşük]** `services/gateway/internal/httpapi/entitlements.go:39-46` ↔ `usage.go:40-43`, `billing.go:65-68`, `analytics.go:63` — `periodQuery` yardımcısı var, ama dönem doğrulaması (`periodParam.MatchString` + `"must look like 2026-11"`) üç yerde daha satır içinde tekrarlanıyor. `pkg/period.Bounds` de aynı biçimi doğruluyor. **Öneri:** Tek bir `validPeriod(id)` kullanın (ya `period.Bounds` ya regex), mesajı tek yerde tutun.

- **[Düşük]** `services/gateway/internal/httpapi/dashboard.go:399` ↔ `dashboard.go:717` — "aktif, deneme ya da gecikmiş abonelik" koşulu iki kez uzun bir boolean ifade olarak yazılmış. **Öneri:** `isLive(sub *plansv1.Subscription) bool` yardımcısı ekleyin.

- **[Düşük]** `services/gateway/internal/httpapi/errors.go:64-75` — `errTooLarge` ve `errTooManyEvents` dalları aynı 413 gövdesini kopyalıyor. **Öneri:** İki tipi tek bir `errTooLarge{msg string}` altında birleştirin ya da ortak bir `interface{ tooLarge() }` ile tek dala indirin.

- **[Düşük]** `services/gateway/internal/httpapi/server.go:309-312` ↔ `session.go:46-49` — `operatorOK`, `bearerToken` yardımcısı varken `strings.CutPrefix(..., "Bearer ")`'ı kendisi tekrar yapıyor; `server.go:251` de aynısını yapıyor. **Öneri:** Her yerde `bearerToken(r)` kullanın.

- **[Düşük]** `services/gateway/internal/httpapi/signin_methods.go:40-50` ↔ `signin_methods.go:56-64` — e-posta normalizasyonu (`strings.ToLower(strings.TrimSpace(...))`) iki kez yazılmış. **Öneri:** `normalizeEmail(string)` yardımcısı ekleyin.

- **[Düşük]** `services/gateway/cmd/gateway/ready.go:11-18` ↔ `internal/auth/auth.go:141-147` — "NotFound/InvalidArgument = servis ayakta" sınıflandırması iki yerde yazılmış; `main.go:248` de `newAPI`'de zaten yaratılan `AdminServiceClient`'ı yeniden yaratıyor. **Öneri:** Readiness kontrolüne `newAPI`'deki client'ı geçirin; kod kümesini tek yerde tutun.

- **[Düşük]** `services/gateway/internal/httpapi/usage.go:7` ↔ `cmd/gateway/main.go:35` — `time/tzdata` iki kez import ediliyor; `pkg/tzdata` paketi bunun için var ve main zaten onu kullanıyor. **Öneri:** `usage.go`'daki blank import'u silin.

- **[Düşük]** `services/gateway/internal/httpapi/accounting.go:101` ↔ `paymentsclient.go:182` — CSV export kendi `n := func(v int64) string { return strconv.FormatInt(v, 10) }` closure'ını tanımlıyor; paket içinde aynı işi yapan `itoa` zaten var. `feesCell` (`accounting.go:117`) de `strconv.FormatInt`'i tekrar yazıyor. **Öneri:** `itoa` kullanın.

- **[Düşük]** (test) `services/gateway/internal/httpapi/credits_test.go:110-114` ↔ `product_test.go:466-470` — `errKey` ve `errKeyOf` birebir aynı. `server_test.go:171-185`, `product_test.go:448-464` ve `assets_test.go:114-127` içindeki `do`/`p.call`/`postForm` istek yardımcıları da aynı gövdeyi paylaşıyor. **Öneri:** Tek bir `testhelpers_test.go` dosyasında `errField(m, "key")` ve başlık parametreli tek bir `doReq` tutun.

- **[Düşük]** (test) `services/gateway/internal/httpapi/server_test.go:132-165` ↔ `product_test.go:398-433` — bufconn gRPC sunucusu + client + `Deps` kurulumu iki kez yazılmış; sahte payments-worker de üç ayrı biçimde var (`product_test.go:422-436`, `paymentlinks_test.go:183-…` `fakeWorker`, `payments_test.go:17-33`). `slog.New(slog.NewTextHandler(io.Discard, nil))` 7 kez geçiyor. **Öneri:** `newBufconnDeps(t, register func(*grpc.Server))` ve tek bir yapılandırılabilir `fakeWorker` kullanın; `discardLog` adında bir test sabiti tanımlayın.

### Overengineering

- **[Orta]** `services/gateway/internal/ratelimit/limiter.go:89-105` ↔ `internal/httpapi/server.go:55-57`, `server.go:233-234` — `Limiter.Allow` hiçbir koşulda hata döndürmüyor (Redis hatası yerel fallback'e düşüyor, dönüş her zaman `nil`). Buna rağmen imza ve `httpapi.Limiter` arayüzü `error` taşıyor ve `chargeKey` `err == nil &&` diye ölü bir dal kontrol ediyor. **Öneri:** Dönüşü `Decision` yapın; hata gerçekten gerekirse fail-open politikasını açıkça çağıran tarafa bırakın.

- **[Düşük]** `services/gateway/internal/httpapi/server.go:159-160` — `Routes()` "kontrat testi için" diye dışa açılmış, ama repo genelinde hiç kullanılmıyor; `openapi_test.go:32` doğrudan `(&Server{}).routes()` çağırıyor. **Öneri:** `Routes()`'u silin.

- **[Düşük]** `services/gateway/internal/httpapi/errors.go:54-60`, `internal/ratelimit/limiter.go:35-54` — `HTTPStatus`, `Tiers` ve `LimitFor` dışa açık, ama yalnızca kendi paketlerinde kullanılıyor (grep doğrulandı). `Tiers` dışa açık bir **değişken** map olduğu için başka paketler bunu değiştirebilir. **Öneri:** Hepsini küçük harfe çekin; `Tiers` değiştirilmemeli.

- **[Düşük]** `services/gateway/internal/auth/auth.go:129`, `auth.go:154`, `internal/ratelimit/limiter.go:108-110`, `internal/httpapi/dashboard.go:704-706` — `a.cache != nil`, `l.rdb == nil` ve `s.d.Limits == nil` kontrolleri hiçbir çağrı yolunda gerçekleşmiyor: main her zaman Redis cache, Redis client ve Limits client veriyor, testler de nil geçmiyor. Bu kontroller nil-güvenli görünen ama test edilmemiş dallar yaratıyor. **Öneri:** Kurucuda nil'i reddedin ve bu kontrolleri silin.

- **[Düşük]** `services/gateway/internal/oidc/oidc.go:57-58`, `oidc.go:99-100`, `cmd/gateway/main.go:89-98`, `main.go:225` — "Google açık mı" kararı üç katmanda ayrı ayrı veriliyor: config doğrulaması, `if cfg.GoogleClientID != ""` ve `Config.Enabled()`/`Client.Enabled()`. `Config.Enabled` dışa açık ama yalnızca `Client.Enabled` kullanıyor. **Öneri:** Kararı tek yerde verin: client yalnızca yapılandırılmışsa yaratılsın ve `Enabled` yalnızca `c != nil` kontrol etsin.

- **[Düşük]** `services/gateway/internal/httpapi/dashboard.go:993`, `signin_methods.go:164-166`, `hosted.go:64-66` — `tsOf` (`timestamppb.New`'in takma adı), `stateKey` (`s.d.Google.StateKey()`'i sarıyor) ve `hostedLimiter.allow` (`allowN(…, hostedPerMinute)`'i sarıyor) hiçbir şey eklemeyen tek satırlık katmanlar. **Öneri:** Doğrudan çağırın.

- **[Düşük]** `services/gateway/internal/httpapi/dashboard.go:623-633`, `dashboard.go:770` — `errgroup.Group` kullanılıyor ama her goroutine `nil` döndürüyor, `g.Wait()`'in sonucu da `_ =` ile atılıyor. Bu kod pratikte bir `sync.WaitGroup`; overview aynı işi gerçekten `WaitGroup` ile yapıyor (`overview.go:390`). **Öneri:** Ortak `sectionRunner`'a geçin (Kod tekrarı bölümüne bakın) ve tek bir mekanizma kullanın.

### Spagetti kod

- **[Yüksek]** `services/gateway/internal/httpapi/dashboard.go:616-951` — `buildDashboard` 335 satır: 9 paralel bölüm, iç içe bir errgroup (`usage`), 15'ten fazla closure-yakalanan değişken (`cur`, `prev`, `stats`, `subsNow`, `ledger`, `usageByCustomer`, `failedMore`, …) ve ardından bölüm bölüm birleştirme, sıralama, ARPU hesabı ve durum belirleme tek fonksiyonda. Hangi değişkeni hangi goroutine'in yazdığını izlemek zor. **Öneri:** Her bölümü bir tip ve metod olarak ayırın (`fetchRevenue`, `fetchUsage`, …) ve sonuçları bir `dashSources` struct'ına toplayın; birleştirmeyi `assembleDashboard(sources)` gibi saf bir fonksiyona taşıyın. Böylece birleştirme tek başına test edilebilir olur.

- **[Orta]** `services/gateway/internal/httpapi/overview.go:381-518` — `buildOverview` 137 satır ve aynı kalıbı taşıyor (5 bölüm, closure ile paylaşılan durum, ardından birleştirme). `invoices` bölümü tek başına 40 satırlık iç bir fonksiyon. **Öneri:** Dashboard ile aynı ayrıştırmayı uygulayın; ortak bölümleri paylaşın.

- **[Orta]** `services/gateway/internal/httpapi/server.go:162-222` — `wrap` 60 satır: body limiti, dört erişim türü için kimlik doğrulama, rate limit, context zenginleştirme ve debug başlığı tek closure'da. `server.go:216`'daki debug koşulu üç seviye iç içe boolean. **Öneri:** Erişim türü başına `authorize(rt, r) (context.Context, error)` fonksiyonları (bir `map[Access]func` ile) ve ayrı bir `allowDebug(rt, ctx)` yardımcısı çıkarın.

- **[Orta]** `services/gateway/internal/httpapi/hosted_page.go:532-600` — `invoicePay` 68 satır: form ayrıştırma, billing çağrısı, payments-worker'a elle kurulan 13 alanlı bir map, iki ayrı `switch` ile hata eşleme, redirect URL doğrulama ve talimat sayfası render'ı. `s.message(...)` aynı argümanlarla 6 kez çağrılıyor. **Öneri:** `startProviderPayment(ctx, p, method, back) (paymentStart, error)` ve `fail := func(code int, key string)` gibi yerel bir yardımcı çıkarın.

- **[Orta]** `services/gateway/internal/httpapi/portal.go:48-126` — `portalPage` 78 satır ve fatura satırlarını, ledger satırlarını, kredi lotlarını ve paketleri tek fonksiyonda kuruyor; `statusKey` map'i her istekte yeniden yaratılıyor. **Öneri:** `portalInvoices`, `portalPayments` ve `portalCredits` olarak üç saf fonksiyona bölün.

- **[Orta]** `services/gateway/internal/httpapi/assets.go:261-335` — `uploadOne` 75 satır: `serverDone` bayrağı, `*total` çıkış pointer'ı, 4 farklı `cancel()` çağrısı ve iç içe `switch`/`if` içeren bir okuma döngüsü. `uploadAssets` (`assets.go:194-258`, 64 satır) da metadata, sayım ve rate-limit şarjını tek döngüde karıştırıyor. **Öneri:** Okuma/gönderme döngüsünü `pumpPart(stream, part, budget *uploadBudget) (rejectReason, error)` olarak çıkarın; toplam bütçeyi pointer yerine küçük bir struct'ta tutun.

- **[Orta]** `services/gateway/internal/httpapi/overview.go:247-319` — `aggregatePayments` 72 satır ve beş işi birden yapıyor: kesilme tespiti, pencere filtresi, durum/para birimi sayımı, günlük seri ve "son N kayıt". **Öneri:** `truncatedSince`, `countByStatus` ve `dailySeries` olarak bölün; dashboard'daki `paymentsByCurrency` ile paylaşılabilecek kısımları birleştirin.

- **[Orta]** `services/gateway/internal/oidc/oidc.go:131-147`, `oidc.go:367-407` — `discover` ve `key`, `c.mu`'yu tutarken sağlayıcıya HTTP isteği atıyor (10 sn timeout). Sağlayıcı yavaşsa bütün Google girişleri bu kilitte sıraya giriyor. `key` önce `discover`'ı çağırıp kilidi bırakıyor, sonra tekrar alıyor; iki aşamalı kilitleme takip edilmesi zor. **Öneri:** Fetch'i kilit dışında yapın (`singleflight` ya da "kilitle → kopyala → bırak → fetch → kilitle → yaz" düzeni).

- **[Orta]** `services/gateway/internal/httpapi/hosted.go:69-90` — `allowN`, `len(l.window) > hostedMaxKeys` olduğunda her çağrıda bütün map'i mutex altında tarıyor. Pencere içindeki taze anahtarlar silinmediği için saldırı altında map sınırsız büyüyor ve her istek 50 000'den fazla girişlik bir O(n) taramaya dönüşüyor; anahtar sınırı da uygulanmıyor. **Öneri:** Taramayı yalnızca zaman tabanlı tutun ve sınır aşıldığında en eskiyi düşürün (ya da yeni anahtarı reddedin); alternatif olarak `ratelimit` paketindeki GCRA'yı yeniden kullanın.

- **[Orta]** `services/gateway/internal/httpapi/server.go:100-114`, `server.go:60-97`, `cmd/gateway/main.go:232-242` — `Server` 9 ayrı `hostedLimiter` alanı taşıyor; dakika başı limitleri dört dosyaya dağılmış (`session.go:124-127`, `signin_methods.go:26-30`, `recovery.go:14-18`, `hosted.go:57-62`). `Deps` 25 alanlı bir god struct ve main'de tek bir okunaksız literal ile dolduruluyor. **Öneri:** Limitleri `publicLimits struct{ signIn, signUp, … limiter }` altında, sabitleriyle birlikte tek dosyada toplayın; `Deps`'i alt gruplara bölün (`Upstreams`, `Payments`, `Identity` ayarları).

- **[Orta]** `services/gateway/internal/httpapi/dashboard.go` (993 satır), `routes.go:243-628` — `dashboard.go` hem 20 DTO tipini, hem dönem ayrıştırmayı, hem cache'i, hem endpoint'leri, hem de birleştirmeyi tutuyor. `routes.go` adına rağmen route tablosunun yanında ~40 plans/admin handler'ı barındırıyor. **Öneri:** `dashboard_types.go`, `dashboard_period.go` ve `dashboard_cache.go` olarak bölün; handler'ları `customers.go`, `subscriptions.go`, `plans.go` ve `admin_tenants.go` dosyalarına taşıyın.

### Clean code ihlalleri

- **[Orta]** `services/gateway/internal/httpapi/assets.go:65`, `assets.go:110`, `assets.go:344`, `billing.go:59-64`, `routes.go:299` — bu handler'lar `decode`/`decodeJSON` yerine doğrudan `json.NewDecoder(r.Body)` kullanıyor. Sonuç olarak `MaxBytesError` 413 yerine 400 (`invalid("body", …)`) olarak dönüyor ve boş body davranışı route'a göre değişiyor; hata işleme tutarsız. **Öneri:** Hepsini `decodeJSON` üzerinden geçirin; alan doğrulamasını decode'dan sonra yapın.

- **[Orta]** `services/gateway/internal/httpapi/hosted_page.go:255`, `hosted_page.go:504` — `rows, _ := s.ledgerLookup(...)` hatası sessizce yutuluyor. Aynı çağrı `portal.go:94-97`'de loglanıyor, `paymentlinks.go:166-171`'de ise 503 dönüyor; aynı hatanın üç farklı ele alınışı var. **Öneri:** En azından `WarnContext` ile loglayın ve politikayı (sayfada degrade et ya da reddet) tek bir yardımcıda belgeleyin.

- **[Orta]** `services/gateway/internal/httpapi/server.go:303` — `id, _ := uuid.Parse(t.GetId())`: plans-svc bozuk bir id dönerse tenant context `uuid.Nil` ile kuruluyor ve istek downstream servislere sıfır tenant ile gidiyor. **Öneri:** Hatayı yukarı taşıyın (`principalOf` `(auth.Principal, error)` döndürsün) ve 503/500 dönün.

- **[Orta]** `services/gateway/internal/httpapi/hosted.go:101`, `hosted.go:111` ↔ `session.go:103-120` — hosted sayfalar istemciyi `clientIP(r)` ile sayıyor, auth route'ları ise `X-Forwarded-For`'u dikkate alan `clientAddr(r)` ile. Console'un nginx'i arkasında tüm hosted ziyaretçiler tek kovaya düşüyor; `TrustForwardedFor` ayarı yarım uygulanmış. **Öneri:** Tüm public limitlerde `s.clientAddr(r)` kullanın.

- **[Orta]** `services/gateway/internal/httpapi/session.go:139-142`, `overview.go:378`, `signin_methods.go:34`, `hosted.go:128` — "önbelleğe alma" başlığı dört farklı biçimde ayarlanıyor (`noStore`: `no-store` + `Pragma`; `noStoreJSON`: `private, no-store`; `methods`: elle `no-store`; `hostedHeadersCSP`: `no-store`). İsimler ne yaptıklarını ayırt etmiyor. **Öneri:** Tek bir `noStore(w)` kullanın; gerçekten farklı bir semantik gerekiyorsa ismi bunu söylesin.

- **[Düşük]** `services/gateway/internal/httpapi/assets.go:243` — `s.charge(w, r, p, "write", 1)` sihirli string kullanıyor; tip `ratelimit.ClassWrite` olmalı. **Öneri:** Sabiti kullanın.

- **[Düşük]** `services/gateway/internal/httpapi/server.go:177`, `server.go:296-302`, `internal/auth/auth.go:173-182`, `internal/ratelimit/limiter.go:36-51` — tier isimleri (`"free"`, `"standard"`, `"enterprise"`) dört yerde literal; oturum sahipleri için `"standard"` tier'ı açıklamasız sabit kodlanmış. **Öneri:** `ratelimit` paketinde `TierFree`/`TierStandard`/`TierEnterprise` sabitleri ve `sessionTier` adında bir sabit tanımlayın.

- **[Düşük]** `services/gateway/internal/httpapi/hosted_page.go:235`, `hosted_page.go:278`, `hosted_page.go:320`, `hosted_page.go:494`, `hosted_page.go:595`, `portal.go:66`, `paymentsclient.go:151`, `dashboard.go:541-543`, `overview.go:349-351`, `app.go:123-126` — `"sandbox"`/`"live"` literalleri 10'dan fazla yerde geçiyor; `pkg/tenant` içinde `EnvSandbox`/`EnvLive` zaten tanımlı. **Öneri:** Sabitleri kullanın.

- **[Düşük]** `services/gateway/internal/httpapi/` (12 yer, örn. `hosted_page.go:243`, `hosted_page.go:248`, `portal.go:88`, `overview.go:284`, `overview.go:305`, `dashboard.go:248`) — `"2006-01-02"` layout'u tekrar tekrar yazılmış; `dashboard.go:232`'de `dayStr` yardımcısı var ama yalnızca dashboard'da kullanılıyor. `"2006-01"` de `dashboard.go:658`, `662`, `821`, `826`'da tekrar ediyor. **Öneri:** `time.DateOnly` ve paket düzeyinde bir `monthLayout` sabiti kullanın.

- **[Düşük]** `services/gateway/internal/httpapi/dashboard.go:520`, `dashboard.go:527`, `dashboard.go:650`, `dashboard.go:697`, `dashboard.go:708`, `dashboard.go:727`, `dashboard.go:767`, `dashboard.go:868`, `dashboard.go:872`, `dashboard.go:914`, `overview.go:467`, `overview.go:498`, `paymentsclient.go:53`, `hosted_page.go:544`, `billing.go:119`, `signin_methods.go:260`, `hosted_page.go:609` — isimsiz sihirli sayılar: cache kapasitesi 128, Top 5, sayfa boyutları 200/50/6/500, eşzamanlılık limiti 5, aktivite sınırları 8 ve 12, yenileme ufku 30 gün ve para birimi başına 6, body limiti `1<<20`, method uzunluğu 64, actor 120, OAuth code 2048, varsayılan üs 2. **Öneri:** Mevcut `const` bloklarına anlamlı isimlerle ekleyin (`dashActivityMax`, `dashRenewalHorizonDays`, …).

- **[Düşük]** `services/gateway/internal/httpapi/server.go:95` ↔ `cmd/gateway/main.go:228`, `main.go:69-70` — `Deps.AppURL` yorumu redirect adresinin "AppURL + /auth/google/callback" olduğunu söylüyor, kod ise `/gw/auth/google/callback` kullanıyor. **Öneri:** Yorumu düzeltin.

- **[Düşük]** `services/gateway/internal/httpapi/signin_methods.go:22` — yorum "signin.go's signIn" diyor, ama `signin.go` yok; `signIn` `session.go:170`'te. **Öneri:** Yorumu güncelleyin.

- **[Düşük]** `services/gateway/cmd/gateway/main.go:77` — yorum mock'un `tools/mockoidc` altında olduğunu söylüyor, ama gerçek yeri `pkg/oidctest/cmd/mockoidc` (`oidc.go:3` de doğru yolu, `pkg/oidctest`'i gösteriyor). **Öneri:** Yolu düzeltin.

- **[Düşük]** `services/gateway/internal/httpapi/analytics.go:63-65` — dönem hatalıyken de hata `invalid("meter", …)` ile `meter` alanına yazılıyor; istemci yanlış alanı vurgular. `analytics.go:23` paket düzeyinde `var err error` tanımlıyor, `analytics.go:48` ise onu gölgeliyor. **Öneri:** Alanları ayrı doğrulayın; `err`'i kullanıldığı yerde tanımlayın.

- **[Düşük]** `services/gateway/internal/httpapi/accounting.go:102-109` — `_ = cw.Write(...)` ile her yazma hatası yutuluyor ve `cw.Error()` hiç kontrol edilmiyor. **Öneri:** `Flush` sonrası `cw.Error()`'u loglayın.

- **[Düşük]** `services/gateway/internal/httpapi/assets.go:332` — `raw, _ := protojson.MarshalOptions{UseProtoNames: true}.Marshal(asset)` hatayı yutuyor ve paket düzeyindeki `marshal` ayarından farklı olarak `EmitUnpopulated` vermiyor; aynı `Asset` `/v1/assets/{id}` ile form upload sonucunda farklı alan kümesiyle dönüyor. **Öneri:** `marshal.Marshal` kullanın ve hatayı reddedilen sonuç olarak işleyin.

- **[Düşük]** `services/gateway/internal/httpapi/portal.go:141-145` — geçersiz `quantity` değerine `-1` sentinel'i atanıp billing'e gönderiliyor; doğrulama dolaylı olarak billing-svc'ye bırakılmış. **Öneri:** Ayrıştırma hatasında doğrudan "bulunamadı/geçersiz" mesaj sayfasını gösterin.

- **[Düşük]** `services/gateway/internal/httpapi/hosted_page.go:114` — `message(w, r, code, lang, title, text, back)` yedi parametre alıyor, üçü aynı tipte string ve yer değiştirmeye açık. `invoiceView(ctx, r, …)` (`hosted_page.go:225`) hem `ctx`'i hem `r`'yi alıyor, oysa `ctx == r.Context()`. **Öneri:** `messageView` struct'ını doğrudan geçirin; `ctx` parametresini kaldırın.

- **[Düşük]** `services/gateway/internal/httpapi/server.go:376-387` — `errTooLarge.files bool` bir boolean bayrakla tipin anlamını değiştiriyor (bayt ya da dosya sayısı). **Öneri:** Mesajı alan tek bir tip kullanın ya da iki ayrı kurucu tanımlayın (`tooManyBytes`, `tooManyFiles`).

- **[Düşük]** `services/gateway/internal/httpapi/server.go:109-113`, `session.go:129` — sign-in, OTP ve OAuth limitleri `hostedLimiter` tipini kullanıyor; isim yalnızca hosted sayfaları ima ettiği için yanıltıcı. **Öneri:** `windowLimiter` ya da `perMinuteLimiter` gibi nötr bir isim verin.

- **[Düşük]** `services/gateway/internal/httpapi/errors.go:99-112` — `codeName` 17 elemanlı map'i her hata yanıtında yeniden yaratıyor. **Öneri:** Map'i `httpStatus` gibi paket düzeyine taşıyın.

- **[Düşük]** `services/gateway/internal/httpapi/hosted.go:196-204` — `hostedText` her çağrıda 6 map'i birleştirip yeni bir map ayırıyor; `hostedLinkStart` (`hosted_page.go:349-351`) bunu tek istekte 4 kez çağırıyor. **Öneri:** Dil başına birleşik tabloyu `init`'te bir kez hesaplayın.

- **[Düşük]** `services/gateway/internal/httpapi/overview.go:383`, `overview.go:498`, `signin_methods.go:189`, `session.go:130`, `hosted.go:101` — `time.Now()` handler içinden doğrudan ve aynı istek içinde birden fazla kez çağrılıyor; dashboard ise `now`'ı parametre olarak geçiriyor. Tutarsız ve test edilmesi zor. **Öneri:** `Server`'a bir `now func() time.Time` alanı ekleyin ve istek başına bir kez okuyun.

- **[Düşük]** `services/gateway/internal/auth/auth.go:193`, `auth.go:212` — `"gw:apikey:"` anahtar öneki iki kez literal geçiyor; plans-svc de bu anahtarı siliyor (paket yorumu bunu söylüyor), yani servisler arası bir sözleşme literal olarak tekrarlanıyor. **Öneri:** `pkg` altında paylaşılan bir sabit tanımlayın (örn. `apikey.CacheKey(prefix)`).

- **[Düşük]** `services/gateway/internal/httpapi/routes.go:62-123` — route grubu isimleri içeriği yansıtmıyor: `subscriptionRoutes` vergi oranlarını ve kuponları, `reportingAndRoutingRoutes` ise mail ayarlarını da içeriyor. **Öneri:** Grupları gerçek alanlara göre adlandırın ya da bölün.

- **[Düşük]** `services/gateway/cmd/gateway/main.go:133` — `_ = providers.Shutdown(c)`: OTel flush hatası sessizce yutuluyor; kapanışta kaybolan span ya da metrikler görünmez kalıyor. **Öneri:** Hatayı `log.Warn` ile loglayın.

### Özet

- En baskın tema **kopyala-yapıştır handler kalıpları**: `page_size`/`limit` ayrıştırma (17 kopya), `ResolveCustomer`, `accountOf` girişi, operatör ve tenant ikiz handler'ları, 15 proxy closure'ı ve `expires_at` yanıtları. Birkaç küçük yardımcı (`queryInt32`, `resolveCustomer`, `withAccount`, `asPathTenant`, `proxyTenant`) yüzlerce satırı siler.
- **Overview ve dashboard** birbirinin kopyası olan iki kompozisyon katmanı; `buildDashboard` 335 satırlık tek fonksiyon. Ortak bir `sectionRunner` kurup bölümleri ve saf birleştirme fonksiyonlarını ayırmak en büyük kazanç.
- **Aynı kavramın birden fazla modeli** var: `ledgerRow` ile `ledgerEntry`, iki dil listesi ve iki dil seçici, beş ayrı env/tier eşlemesi ve iki body decoder (`decode` ile `decodeJSON`). Ayrıca 5 handler `json.NewDecoder` ile bunları tamamen atlıyor ve 413 davranışını bozuyor.
- **Tutarsız hata işleme**: ledger hatası bir yerde yutuluyor, bir yerde loglanıyor, bir yerde 503 dönüyor; `uuid.Parse`, `protojson.Marshal` ve `csv.Write` hataları `_` ile atılıyor; limiter'ın hiç oluşmayan `error` dönüşü ölü bir dal yaratıyor.
- **Dağınık limit ve sabitler**: 9 limiter alanı ve dört dosyaya yayılmış dakika başı sabitleri, hosted limitlerin XFF'i yok sayması, ve 128/200/50/6/12 gibi isimsiz sayılar. Bunlar tek bir `publicLimits` yapısında ve isimli sabitlerde toplanmalı.
