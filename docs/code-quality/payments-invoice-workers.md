Kapsam: `services/payments-worker` ve `services/invoice-worker` altındaki bütün üretim dosyaları okundu (sqlc tarafından üretilen kod hariç). Testlere yalnızca tekrar açısından bakıldı. Kullanılmadığı ya da ölü olduğu söylenen her şey repo genelinde grep ile doğrulandı.

| Servis | Kod tekrarı | Overengineering | Spagetti kod | Clean code ihlalleri |
|---|---|---|---|---|
| payments-worker | 21 (19 production + 2 test) | 8 | 9 | 17 |
| invoice-worker | 4 | 1 | 2 | 4 |
| Toplam | 25 | 9 | 11 | 21 |

## services/payments-worker

### Kod tekrarı
- **[Orta]** `services/payments-worker/internal/webhook/api.go:395-550` — Beş handler (`previewChange`, `applyChange`, `pause`, `resume`, `paymentMethodLink`) aynı açılışı tekrarlıyor: `s.target`, ardından `SubscriptionAPI` assertion, `unsupported`, isteğe bağlı `decodeBody` ve `s.providerSubscription`. Her birinde 15-20 satır aynı. **Öneri:** `withSubscription(func(w, r, tg, api, sub))` sarmalayıcısı ya da `subscriptionTarget(w, r)` yardımcısı.
- **[Orta]** `services/payments-worker/internal/webhook/api.go:256-327` — `ensureCustomer` ve `portalLink` aynı `CustomerAPI` assertion'ını ve aynı `if status == 0 { providerFail } else { fail(status) }` dalını içeriyor (266-274 ile 312-320). **Öneri:** `customerRefs` HTTP kodu değil tipli hata dönsün, eşleme tek bir `writeCustomerErr` içinde yapılsın.
- **[Orta]** `services/payments-worker/internal/webhook/invoicepay.go:157-251` — `startCheckout` ve `startInstructions` üç bloğu kopyalıyor:
  - `Resolver.Named`, interface assertion ve `CanCharge` (168-177 / 219-228)
  - `uuid.NewSHA1(linkNS, key)` ile `Attempts.Start(... attemptRequest{})` (193-194 / 231-232)
  - pending ledger satırı ve hata logu (210-213 / 242-245)

  **Öneri:** `liveProvider`, `startLinkAttempt` ve `recordPending(detail)` yardımcıları.
- **[Orta]** `services/payments-worker/internal/handler/transfer.go:39-92` ve `services/payments-worker/internal/transfers/query/query.go:85-127` — Banka havalesinin kuralları iki yerde ayrı ayrı yazılmış. Pencere seçimi her ikisinde de hesap ayarı, sonra deployment ayarı, sonra `DefaultTransferWindow` sırasıyla yapılıyor. Durum kararı da her ikisinde var: alınan tutar ≥ toplam ise ödendi, closure varsa reddedildi ya da süresi doldu, pencere geçtiyse süresi doldu. Kurallar birbirinden ayrılırsa saga'nın gördüğü cevap (`manualStatus`) ile operatörün gördüğü (`query.Get`) çelişir. **Öneri:** Kural tek bir saf `Evaluate(charge, received, pending, closure, window, now)` fonksiyonunda dursun, `manualStatus` cevabını bu sonuçtan üretsin.
- **[Orta]** `services/payments-worker/internal/webhook/plans.go:20-22` — `as()` yardımcısı zaten var, buna rağmen `reconcile.go:34,38,43` ve `transfers/handler.go:199` `tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: id}))` ifadesini elle tekrarlıyor. Bu ifade servislerde toplam 12 kez geçiyor. **Öneri:** `pkg/tenant` içine `OutgoingFor(ctx, id)` eklenmeli.
- **[Orta]** "sandbox"/"live" işleme mantığı dağınık:
  - string doğrulaması 3 kopya: `webhook/webhook.go:159-163`, `webhook.go:497-504`, `webhook/einvoice.go:27-34`
  - enum'dan string'e çevirme 3 kopya: `handler/handler.go:130-135`, `einvoice/handler.go:99-103`, `services/invoice-worker/internal/handler/handler.go:94-97`

  **Öneri:** pkg'de tek bir `env.Name` / `env.Parse`.
- **[Orta]** Aynı HTTP sunucusunda yardımcılar çiftlenmiş:
  - JSON yazımı iki kez: `webhook.writeJSON` (`webhook.go:137-141`) ve `transfers.write` (`transfers/handler.go:154-158`)
  - gRPC'den HTTP'ye eşleme üç kez: `planFail` switch'i (`webhook.go:807-818`), `httpCodes` map'i (`transfers/handler.go:160-168`) ve gateway (`services/gateway/internal/httpapi/errors.go:46`)

  **Öneri:** `internal/httpx` paketi: tek WriteJSON, tek hata zarfı, tek kod eşlemesi.
- **[Orta]** `services/payments-worker/cmd/payments-worker/main.go:280-293` — `serveHTTP`, `pkg/health/health.go:114-132` içindeki `health.Serve`'ün kopyası; yalnızca timeout değerleri farklı. Kopyada `http.ErrServerClosed` kontrolü de eksik. **Öneri:** pkg'de `*http.Server` alan ortak bir `Serve`.
- **[Orta]** `services/payments-worker/cmd/payments-worker/main.go:231-235` ve `309-313` — `secretbox.ParseKey` ve `secretbox.New` iki kez çağrılıyor, ikisinde de `key, _ :=`. `store.EInvoiceAccounts{DB, Box}` da iki kez kuruluyor (275 ve 314). **Öneri:** box `start()` içinde bir kez kurulup iki yere parametre olarak verilsin.
- **[Orta]** "plans-svc is not configured" 503 kontrolü beş yerde elle yazılmış: `webhook.go:682-685`, `webhook.go:795-798`, `api.go:133-136`, `api.go:280-282`, `api.go:341-344`. **Öneri:** `s.needPlans(next)` middleware'i.
- **[Düşük]** `services/payments-worker/internal/webhook/webhook.go:603-612` — `join`, `strings.Join(names, ", ")` ile aynı işi yapıyor; `strings` zaten import edilmiş (satır 18). **Öneri:** `join` silinsin.
- **[Düşük]** Standart kütüphanede karşılığı olan yardımcılar (Go 1.27 kullanılıyor):
  - `api.go:434-439` `orCurrency` ve `einvoice/handler.go:239-244` `orDefault` aslında `cmp.Or`
  - `reconcile.go:237-244` `sortedKeys` aslında `slices.Sorted(maps.Keys(m))`

  **Öneri:** Üçü de silinsin.
- **[Düşük]** `services/payments-worker/internal/webhook/reconcile.go:84,93` — YYYY-MM için ayrı bir regex var; `pkg/period.Bounds` aynı doğrulamayı zaten yapıyor. **Öneri:** `period.Bounds(p, time.UTC)` kullanılsın.
- **[Düşük]** `services/payments-worker/internal/webhook/reconcile.go:246-249` — `ledgerCharge` alan alan `store.Charge` (`store/ledger.go:127-130`) ile aynı; satır 129'daki `ledgerCharge(c)` dönüşümü de bunu kanıtlıyor. **Öneri:** Doğrudan `store.Charge` kullanılsın.
- **[Düşük]** `services/payments-worker/internal/store/store.go:23-37` — `found` / `notFound` yardımcıları var ama şu yerlerde `pgx.ErrNoRows` hâlâ elle ele alınıyor: `accounts.go:82-87`, `einvoice.go:67-72`, `einvoice.go:96-102`, `events.go:50-53`. **Öneri:** Bu yerler de `found(...)` kullansın.
- **[Düşük]** `services/payments-worker/internal/store/transfers.go:123-124` ve `196-197` — sqlc satırından `Receipt`'e çeviri iki kez yazılmış. **Öneri:** `receiptFromRow`.
- **[Düşük]** `services/payments-worker/internal/handler/handler.go:334-346` — `refund`, `chargeOutcome`'daki (290-314) transient/timeout/default eşlemesini tekrarlıyor. **Öneri:** Ortak `providerErrorReply(err)`.
- **[Düşük]** Reply kurucuları dağınık:
  - `einvoice/handler.go:318` ve `347` — `State: "cancelled"` reply'ı iki kez literal olarak kuruluyor
  - status reply'ları üç ayrı kurucuya dağılmış: `handler/transfer.go:30-36`, `handler/transfer.go:94-97`, `handler/handler.go:389-400`

  **Öneri:** `cancelledReply()` ve tek bir `statusReply(state, ref, code)`.
- **[Düşük]** `services/payments-worker/internal/webhook/einvoice.go:81,84,94,106,109,134` — Hata anahtarları `errkey.Key("einvoice.account.invalid")` gibi literal yazılıyor; oysa `internal/einvoice/handler.go:44-50` bunları `KeyAccountInvalid` vb. olarak export ediyor. **Öneri:** Export edilmiş sabitler kullanılsın ya da sabitler `pkg/einvoice`'a taşınsın.
- **[Orta] (test)** `services/payments-worker/internal/itest/*.go` — Yaklaşık 25 satırlık kurulum 12 testte kopyalanmış. Kurulum adımları: StartPostgres, CreateServiceDB, Migrate, NewPool, secretbox, registry, Resolver, Handler, `webhook.Server`, httptest. Kopyalar:
  - `accounts_integration_test.go:78-102`
  - `paddle_integration_test.go:172-197`, `paddle_integration_test.go:423-`, `paddle_integration_test.go:470-`
  - `api_integration_test.go:113-143`
  - `banktransfer_integration_test.go:90-115`, `banktransfer_integration_test.go:328-`
  - `link_integration_test.go:92-124`
  - `einvoice_integration_test.go:133-154`
  - `reconcile_integration_test.go:50-95`
  - `payments_integration_test.go:104-`, `payments_integration_test.go:182-`

  Ayrıca `secretbox.New` hatası her seferinde `_` ile atılıyor. **Öneri:** `newEnv(t, opts)` fixture'ı.
- **[Düşük] (test)** `services/payments-worker/internal/itest/` — HTTP çağrı closure'ları (`do`, `call`, `post`, `get`) 9 kez yazılmış: `accounts:105`, `api:153`, `einvoice:156`, `banktransfer:119,412`, `link:137`, `paddle:207,503`, `reconcile:97`. Mutex'li `count()` 4 kez tekrarlanmış: `accounts:65`, `paddle:84`, `api:94`, `einvoice:112`. **Öneri:** Ortak `apiClient` ve sayaçlı `fakeServer`.

### Overengineering
- **[Orta]** `services/payments-worker/internal/transfers/handler.go`, `transfers/command/command.go`, `transfers/query/query.go` — 3 endpoint için 3 paket açılmış (ADR 0020). Sonuçları:
  - `command.Deps`, `query.Deps`'i gömüyor; bu yüzden `h.Deps.Deps` (`transfers/handler.go:62`) ve `transfercommand.Deps{..., Deps: transferquery.Deps{...}}` (`main.go:271-272`) gibi yapılar çıkıyor.
  - `query` ve `command`, havale sabitleri için kuyruk handler paketini import ediyor (`query.go:12,87,120`, `command.go:17,135-139`).

  **Öneri:** Tek bir `transfers` paketi ve bir Service; `TransferExpired`, `TransferRejected`, `DefaultTransferWindow` ve `DeclineCode` oraya taşınsın.
- **[Düşük]** `services/payments-worker/internal/handler/transfer.go:22` — `DefaultTransferWindow` fallback'i (`transfer.go:43-46` ve `query.go:85-88`) production'da hiç çalışmıyor. `Config.Validate` `PAYMENTS_TRANSFER_WINDOW_DAYS` için 1..90 aralığını zorunlu tutuyor (`main.go:101`) ve YAML varsayılanı zaten 14. Yani 14 gün iki yerde tanımlı. **Öneri:** Tek kaynak YAML olsun, fallback kaldırılsın.
- **[Düşük]** `services/payments-worker/internal/handler/handler.go:137` — `errored(retryable bool, ...)` her çağrıda `false` alıyor (`grep 'errored(true'` 0 sonuç). Boolean parametre ölü. **Öneri:** Parametre kaldırılsın.
- **[Düşük]** `services/payments-worker/internal/einvoice/handler.go:30-35` — `Issuances` interface'inin tek implementasyonu var; itest bile gerçek store'u kullanıyor (`itest/einvoice_integration_test.go:152`). **Öneri:** Somut tip kullanılsın.
- **[Düşük]** `services/payments-worker/internal/einvoice/handler.go:184-186` — Ölü kod: `provider()` sandbox için her zaman mock döndürüyor (119-120), dolayısıyla `Name != mock.Name && sandbox` koşulu asla doğru olamaz. **Öneri:** Satır silinsin.
- **[Düşük]** `services/payments-worker/internal/store/transfers.go:153-161` — `CheckReceipt` export edilmiş ve `paid` dönüyor, ama tek çağıran (200) bu değeri atıyor. Ayrıca iş kuralı store paketinde duruyor. **Öneri:** Domain paketine taşınsın ve yalnızca `error` dönsün.
- **[Düşük]** `services/payments-worker/internal/webhook/reconcile.go:115` `Reconcile` ve `invoicepay.go:31` `PaymentMethod` export edilmiş, ama yalnızca kendi paketlerinde kullanılıyorlar. **Öneri:** Unexport edilsinler.
- **[Düşük]** `services/payments-worker/internal/handler/resolver.go:17-51` — Cache hiç eviction yapmıyor, silinen hesaplar bellekte kalıyor. Üstelik her çağrıda zaten DB'ye ve secretbox'a gidiliyor; cache yalnızca `Registry.Build` adımını kurtarıyor. **Öneri:** TTL ya da boyut sınırı eklensin, ya da cache'in gerçekten gerekip gerekmediği ölçülsün.

### Spagetti kod
- **[Yüksek]** `services/payments-worker/internal/webhook/webhook.go:39-63, 103-125` — Server bir god struct haline gelmiş:
  - `Server`'ın 17 alanı var.
  - Kendisi 14 route kaydediyor, buna `api.go`'dan 11 ve `einvoice.go`'dan 3 route ile modül route'ları ekleniyor.
  - 818 satırlık dosya bildirim inbox'ını, ledger'ı, abonelik senkronunu, migration'ı ve hesap CRUD'unu bir arada tutuyor.

  **Öneri:** `notifications.go`, `accounts.go` ve `migrations.go` olarak bölünsün; her parça kendi bağımlılıklarıyla `RouteRegistrar` desenini kullansın.
- **[Orta]** `services/payments-worker/internal/webhook/webhook.go:212-343` — `delivery` adımları "cevap yazıldıysa false döner" sözleşmesiyle çalışıyor. Cevap yazma, metrik ve inbox güncellemesi beş metoda dağılmış. **Öneri:** Adımlar `(outcome, error)` dönsün; cevap, metrik ve inbox `process` içinde tek yerde ele alınsın.
- **[Orta]** `services/payments-worker/internal/webhook/webhook.go:409-469` — `recordSubscription` 60 satır: resolver, migration tamamlama, upsert, plans senkronu ve katalog eşlemesi tek fonksiyonda. Aynı 9 argümanlı `Subscriptions.Upsert` iki dalda tekrar ediyor (438 ve 444). **Öneri:** Upsert tek kez yapılsın; `completeMigration` ve `syncToPlans` ayrı fonksiyonlara çıkarılsın.
- **[Orta]** `services/payments-worker/internal/webhook/webhook.go:371-407` — `ledgerEntry` adına rağmen yan etki içeriyor: `ChargeByKey` ile DB sorgusu yapıyor (385) ve hatayı sessizce yutuyor. **Öneri:** Zenginleştirme hatayı yukarı taşıyan ayrı bir adım olsun.
- **[Orta]** `services/payments-worker/internal/handler/handler.go:69-105` — `providerFor`, `charge bool` ve `named` parametreleriyle üç farklı politikayı tek fonksiyonda yönetiyor; 3 seviyelik bir `else if x, ok, err :=` zinciri var. **Öneri:** `providerForCharge(named)` ve `providerForFollowUp(invoice)` olarak ikiye bölünsün.
- **[Orta]** `services/payments-worker/internal/einvoice/handler.go:151-237` — `issue` 87 satır ve şunları tek fonksiyonda yapıyor: idempotency, provider, credit-note orijinali, tip seçimi, UBL ve hata→key eşlemesi, Begin, Issue, Status, store, Complete. **Öneri:** `prepareDocument`, `buildUBL` ve `issueAndConfirm` adımlarına bölünsün.
- **[Orta]** `services/payments-worker/cmd/payments-worker/main.go:230-277` — `webhookHandler` 47 satır. Kuyruk handler'ını da değiştiriyor (`h.Resolver = ...`, 237). Bu yüzden live tenant yönlendirmesi gizlice HTTP sunucusu kurulumuna bağlı. `SecretsKey` boşsa `h.Resolver` nil kalıyor. **Öneri:** Resolver `start()` içinde kurulsun ve iki tarafa açıkça verilsin.
- **[Orta]** `services/payments-worker/internal/transfers/query/query.go:69-128` — `Get` 60 satır, her çağrıda 5 DB okuması ve bir provider build yapıyor. `Confirm` onu 2 kez çağırıyor (86, 117), `Reject` de 2 kez (146, 169); istek başına 10'dan fazla sorgu ediyor. **Öneri:** Okuma bir loader'a, hesap saf bir fonksiyona ayrılsın; durum mevcut veriden yeniden hesaplansın.
- **[Düşük]** `services/payments-worker/internal/webhook/reconcile.go:176-205, 216-233, 274-279` — N+1 sorgu: fatura başına `GetInvoice` çağrılıyor (`paidByHand`, `unpaidCharges`) ve üst sınır sihirli 500. **Öneri:** Toplu billing API kullanılsın ya da `ListInvoices` ödemeleri de dönsün.

### Clean code ihlalleri
- **[Yüksek]** `services/payments-worker/internal/einvoice/handler.go:169` — `uuid.MustParse(orDefault(c.GetOriginalDocumentId(), ...))`: kuyruktan bozuk bir `original_document_id` gelirse worker panic ile çöker. Mesaj poison olarak işaretlenmeden tekrar tekrar teslim edilebilir. **Öneri:** `uuid.Parse` kullanılsın, hata durumunda `workerx.ErrPoison` dönülsün.
- **[Orta]** `services/payments-worker/internal/handler/handler.go:193` — `tenantID, _ := uuid.Parse(...)`: bozuk tenant `uuid.Nil` olarak DB'ye ve resolver'a gidiyor. `einvoice/handler.go:95-97` ise aynı durumu poison sayıyor, yani davranış tutarsız. **Öneri:** Burada da `ErrPoison` dönülsün.
- **[Orta]** `services/payments-worker/internal/webhook/reconcile.go:108` — billing-svc hataları `planFail` üzerinden aktarılıyor ve mesajın başına "plans-svc: " ekleniyor (`webhook.go:816`). Yanlış servis adı gösteriliyor. **Öneri:** `grpcFail(w, svc, err)`.
- **[Orta]** `services/payments-worker/internal/webhook/api.go:424` — Preview cevabında `"timing": "CHANGE_TIMING_IMMEDIATELY"` sabit yazılmış. Oysa `planChange` başka timing değerlerini de provider'a geçiriyor (388-392). **Öneri:** Gerçek `ch.Timing` dönülsün.
- **[Orta]** Aynı sunucuda iki ayrı hata zarfı kullanılıyor:
  - `fail` (`webhook.go:143-145`): `{"error":"msg"}`
  - `keyFail` ve `transfers.fail` (`webhook/einvoice.go:23-25`, `transfers/handler.go:172-188`): `{"error":{code,key,message}}`

  `webhook/einvoice.go` ikisini karıştırıyor: 51, 114 ve 132'de `fail`, diğer satırlarda `keyFail`. **Öneri:** Anahtarlı zarfta birleşilsin.
- **[Orta]** Yutulan hatalar:
  - `webhook.go:294` `_ = Events.Forget`; aynı hata `retry()` içinde (234-236) loglanıyor.
  - `webhook.go:337` Outcomes hatası yok sayılıyor.
  - `webhook.go:385` ChargeByKey hatası yok sayılıyor.
  - `webhook.go:543` ParseBool hatası yok sayılıyor; `default=yes` sessizce false oluyor.
  - `webhook.go:574` `syncBankTransfer` hatası loglanmıyor; `putAccount` (550-551) aynı durumda logluyor.
  - `api.go:110` ByRef hatası.
  - `query.go:89` `Accounts.Named` hatası.
  - `einvoice/handler.go:278,288` FetchUBL ve FetchPDF hataları.

  **Öneri:** En azından Warn seviyesinde loglansın; ParseBool hatası 400 dönsün.
- **[Orta]** Loglanmadan 500 dönen yerler:
  - `webhook.go:513,571,597,621,766`
  - `api.go:158,164,249`
  - `invoicepay.go:71,76,180,188,196,233,281`
  - `webhook/einvoice.go:51,132`

  Buna karşılık `putAccount` (545) ve `reconcile` (104) logluyor. **Öneri:** `s.internalError(w, r, err)` yardımcısı.
- **[Orta]** Sihirli domain string'leri:
  - ledger kind/status/source değerleri `"charge"`, `"refund"`, `"succeeded"`, `"declined"`, `"failed"`, `"pending"`, `"command"`, `"webhook"`, `"manual"` şu yerlerde geçiyor: `handler.go:250,264,280,283,299,303,337`, `webhook.go:372-399`, `invoicepay.go:193,210,231,242`, `command.go:165`, `transfer.go:70,88`
  - `"live"` ve `"sandbox"` 30'dan fazla yerde
  - `"price"`, `"product"`, `"discount"`, `"customer"`: `api.go:109,162,211,231,236,247,291,381,575`
  - `"provider"` owner değeri `api.go:351,472` ve `webhook.go:745`'te literal; oysa `payments.OwnerProvider` mevcut (`webhook.go:702`)

  **Öneri:** Tipli sabitler tanımlansın.
- **[Orta]** String ile kodlanmış gizli bağımlılıklar:
  - `invoicepay.go:211` ledger detail'ine `"checkout:"+URL` yazıyor, `store/linkpay.go:53` bunu `TrimPrefix` ile çözüyor.
  - `"instructions:"` hem method id'si (`invoicepay.go:23`) hem detail ön eki (243) olarak kullanılıyor.
  - `priceKey "a|b|c"` biçiminde kuruluyor (`api.go:88-90`) ve `Split` ile çözülüyor (113-115).
  - Closure detail'i `"code: reason"` biçiminde (`transfer.go:24-28`, `command.go:163`).

  **Öneri:** Ayrı kolonlar ya da encode/decode çiftleri.
- **[Düşük]** Uzun parametre listeleri:
  - `Attempts.Start`: 9 parametre (`store/store.go:64`)
  - `Subscriptions.Upsert`: 9 parametre (`store/subscriptions.go:29`)
  - `Issuances.Complete`: 7 parametre (`store/einvoice.go:108`)
  - `webhookHandler`: 6 parametre (`main.go:230`)

  Ayrıca `planChange` (`api.go:359`) 5 değer dönüyor ve HTTP status da bunlardan biri; `customerRefs` (`api.go:279`) da HTTP kodu dönüyor. **Öneri:** Parametre struct'ları ve tipli hatalar.
- **[Düşük]** `services/payments-worker/internal/einvoice/handler.go:151,309` — `env string` ve `sandbox bool` aynı bilgiyi taşıyor (99-103). **Öneri:** Tek parametre.
- **[Düşük]** `services/payments-worker/internal/einvoice/handler.go:75,78-83` — `truncate(msg, 400)` byte bazında kesiyor; Türkçe metinde UTF-8 karakteri ortadan bölebilir. Ayrıca 400 sihirli sayı. **Öneri:** Rune'a göre kesme ve bir sabit.
- **[Düşük]** `services/payments-worker/internal/store/store.go:1-2` — Paket yorumu "payment_attempts table" diyor ama paket 8 tabloya dokunuyor. Metotlar da dağınık:
  - `Attempts` metotları `store.go`, `events.go:48-61` ve `linkpay.go:64-68` arasına bölünmüş.
  - `Ledger` metotları `ledger.go` ile `linkpay.go` arasına bölünmüş.
  - `Transfers.LatestCharge` (`transfers.go:31`) `payment_attempts`'ı okuyor.

  **Öneri:** Her tip kendi dosyasına alınsın, yorum düzeltilsin.
- **[Düşük]** Sihirli sayılar ve tekrarlanan sabitler:
  - `reconcile.go:161` ve `reconcile.go:217`'de 500
  - `webhook.go:586,589`'da 100 ve 1..1000
  - body limitleri tutarsız: 16 KiB (`transfers/handler.go:30`) ve 64 KiB (`webhook.go:86`)
  - `command.go:73,77,144` mesajlarındaki "128" ve "500" değerleri `maxReferenceLen` / `maxNoteLen`'i tekrarlıyor
  - `"XXX"` (`webhook.go:404`, `reconcile.go:200`)
  - `"TR"` (`einvoice/handler.go:251`, `document.go:80,85,98`)
  - `"bank transfer"` (`reconcile.go:266`)

  **Öneri:** İsimli sabitler.
- **[Düşük]** İsimlendirme tutarsızlıkları:
  - `ErrOverpayment` bir tip (`transfers.go:147`); Go geleneğine göre `OverpaymentError` olmalı.
  - `command.Close` struct'ı ile `Transfers.Close` metodu aynı adı paylaşıyor.
  - `s.tenant` receiver'ı kullanmıyor (`webhook.go:488`), `environment` ise düz fonksiyon (497).
  - `Log != nil` kontrolü yalnızca `handler.go:121` ve `einvoice/handler.go:304`'te yapılıyor.
  - `handler.go:154`'teki boş `case err != nil:` örtük bir varsayılana dayanıyor.

  **Öneri:** Tek bir kural seçilsin.
- **[Düşük]** İki ayrı saat enjeksiyonu var: `handler/handler.go:50-55` `Handler.Now` ve `transfers/query/query.go:41-46` `Deps.Now`. Testler ikisini ayrı ayrı bağlıyor (`itest/banktransfer_integration_test.go:110,115`). **Öneri:** Tek bir Clock.
- **[Düşük]** `services/payments-worker/cmd/payments-worker/main.go:172-179` — `banktransfer.Register`, iyzico ve paddle kayıtlarından (173-174) e-fatura wiring'i ile ayrılmış. **Öneri:** Kayıtlar tek bir `newPaymentRegistry()` içinde toplansın.

## services/invoice-worker

### Kod tekrarı
- **[Düşük]** `services/invoice-worker/cmd/invoice-worker/main.go:77-91` — Bucket dışında aynı iki `blob.New(blob.Config{...})` çağrısı var. **Öneri:** `s3Config(bucket)`.
- **[Düşük]** `services/invoice-worker/cmd/invoice-worker/main.go:51-75` — Bootstrap kodu (logx, signal, otelx, shutdown), `payments-worker/cmd/payments-worker/main.go:117-141` ile aynı ve 11 serviste tekrarlanıyor. **Öneri:** `pkg` içinde `Bootstrap` yardımcısı.
- **[Düşük]** `services/invoice-worker/internal/render/render.go:138-153` — "From" ve "Bill to" blokları aynı çizim adımlarını tekrarlıyor. **Öneri:** `drawParty(...)`.
- **[Düşük]** `services/invoice-worker/internal/handler/handler.go:75,94-97` — Ortam enum'u string'e çevriliyor; payments-worker'daki iki kopyayla birlikte bu üçüncü kopya.

### Overengineering
- **[Düşük]** `services/invoice-worker/internal/handler/handler.go:20-31` — `Logos` alanı tam `Store` interface'ini istiyor (Head, Get, Put) ama yalnızca Get kullanılıyor. "nil: no logos" yorumu da geçersiz: `main.go:87-91` Logos'u her zaman kuruyor ve `S3_ASSETS_BUCKET`'ın varsayılanı `"assets"`. **Öneri:** Dar bir interface ve config'e bağlı opsiyonellik.

### Spagetti kod
- **[Orta]** `services/invoice-worker/internal/handler/handler.go:58-65` — Kilit map'inde yarış durumu var. `Unlock` çağrısından sonra `Delete` yapıldığı için:
  - üçüncü bir goroutine yeni bir mutex oluşturabiliyor ve render, eski mutex'i bekleyen goroutine ile aynı anda çalışıyor;
  - bekleyen goroutine, başkasının kaydını silebiliyor.

  Satır 58-59'daki yorumun söz verdiği tekilleştirme bozuluyor. **Öneri:** `golang.org/x/sync/singleflight` (x/sync zaten bağımlılık).
- **[Orta]** `services/invoice-worker/internal/render/render.go:85-187` — `PDF` yaklaşık 100 satır: doğrulama, metadata, watermark, logo, başlık, taraflar, tablo, toplamlar, talimatlar. **Öneri:** `drawWatermark`, `drawLogo`, `drawHeader`, `drawParties`, `drawLines`, `drawTotals` olarak bölünsün.

### Clean code ihlalleri
- **[Orta]** `services/invoice-worker/internal/render/render.go:101-177` — Yerleşim tamamen sihirli sayılarla yapılmış:
  - kolon genişlikleri 110/30/32 ve buna bağlı 140 (110+30)
  - font boyutları, renkler ve koordinatlar (108-124)
  - `"Helvetica"` 12 kez

  **Öneri:** İsimli layout sabitleri.
- **[Düşük]** `services/invoice-worker/internal/render/render.go:125-128` — `ClearError` yalnızca logo hatasını değil, birikmiş bütün hataları siliyor. **Öneri:** Logodan önce `pdf.Err()` kontrol edilsin.
- **[Düşük]** `services/invoice-worker/internal/render/render.go:35-51` — `latin1` yorumu eksik: '—' ve kontrol karakterleri için özel eşleme yapıldığından söz etmiyor. Türkçe ğ, ş ve ı harflerinin '?' olarak basıldığı da belgelenmemiş. **Öneri:** Yorum düzeltilsin, UTF-8 font değerlendirilsin.
- **[Düşük]** `services/invoice-worker/internal/handler/handler.go:69,99` — `"sha256"` metadata anahtarı iki kez literal yazılmış; 94-97'de de literal `"live"` ve `"sandbox"` var. **Öneri:** Sabitler tanımlansın.

### Özet
- **Kopyalanmış iş kuralları:** En riskli tekrar, havale durum ve pencere kuralının iki ayrı yerde yazılmış olması (`handler/transfer.go` ve `transfers/query`). Bunun yanında link-ödeme akışı ve provider-subscription endpoint açılışları da kopyalanmış.
- **Dağınık HTTP katmanı:** Aynı sunucuda 2 hata zarfı, 2 `writeJSON`, 3 gRPC→HTTP eşlemesi ve kopyalanmış bir `health.Serve` var. Birçok 500 loglanmadan dönüyor ve birçok hata yutuluyor.
- **God struct ve uzun fonksiyonlar:** `webhook.Server` (17 alan, 28'den fazla route, 818 satır), `einvoice.issue` (87 satır), `render.PDF` (yaklaşık 100 satır), bayrak parametreli `providerFor` ve "cevap yazıldıysa false" zinciri.
- **Tipsiz domain string'leri:** Ledger kind/status/source değerleri, ortam ve obje türleri literal olarak yazılmış. Detail alanına string kodlama ile katmanlar arası gizli bağımlılıklar kurulmuş.
- **Refactor'dan önce düzeltilmesi gereken somut hatalar:**
  - `uuid.MustParse` worker'ı panic ile çökertiyor (Yüksek).
  - Tenant id parse hatası yok sayılıyor.
  - Reconcile hataları yanlış olarak "plans-svc" etiketiyle dönüyor.
  - Preview cevabındaki timing sabit yazılmış.
  - invoice-worker'ın kilit map'inde yarış durumu var.
