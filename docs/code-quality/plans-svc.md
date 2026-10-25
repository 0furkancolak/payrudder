## services/plans-svc

Kapsam: `cmd/plans-svc`, `internal/**` (üretilmiş `internal/store/sqlc` hariç) ve `migrations/embed.go`. Testlere yalnızca test kodu tekrarı için bakıldı. Tüm yollar repo köküne göredir. "Kullanılmıyor" iddiaları `.claude/worktrees` dışlanarak tüm repoda grep ile doğrulandı.

Bulgu sayıları: Kod tekrarı 35 (2'si test kodu), Overengineering 11, Spagetti kod 14, Clean code ihlalleri 26.

### Kod tekrarı

- **[Yüksek]** `services/plans-svc/internal/subscriptions/command/planchange.go:147-174` ↔ `services/plans-svc/internal/changes/command/command.go:52-89` (ayrıca `services/plans-svc/internal/providermigrations/command/command.go:41-85`) — `replacePlan`, `SwitchTo`'nun neredeyse birebir kopyası. İkisi de aboneliği bitirip "n != 1 → Aborted" kontrolü yapıyor, eskiyi Cancelled yapıyor, `ChangedFromID` ile halef ekliyor ve eski/yeni entitlement'ı yayınlıyor. Fark yalnızca şu: `SwitchTo` kuponu taşıyıp provider bağını bırakıyor, `replacePlan` bunları yapmıyor. Yani plan değişikliğinde kupon eski abonelikte kalıyor; bunun bilinçli mi yoksa tekrarın yan etkisi mi olduğu belirsiz. `cutover` aynı iskeleti üçüncü kez kuruyor. **Öneri:** `replacePlan` ve `cutover`'ı `changescmd.SwitchTo(..., Successor{FeeTiming: row.FeeTiming, Owner: ...})` üzerine kurun. Farklı davranış gerekiyorsa `Successor`'a açık bir alan ekleyin.
- **[Yüksek]** `services/plans-svc/internal/reports/query/query.go:36-51` — `monthBounds`, `pkg/period`'daki `PeriodOf` + `Bounds`'u (`pkg/period/period.go:12-25`) kendi regex'i, `Sscanf`'i ve `AddDate(0,1,0)`'ı ile yeniden yazıyor. `services/plans-svc/internal/subscriptions/command/create.go:222-225` (`monthStart`) aynı ay başı hesabını üçüncü kez yapıyor. **Öneri:** `period.Bounds(period.PeriodOf(now, loc), loc)` ya da `period.Bounds(month, loc)` kullanın; ayrı regex'i ve `monthStart`'ı silin.
- **[Yüksek]** Üyelik/yetki kontrolü 5 kez elle yazılmış:
  - `services/plans-svc/internal/identity/query/query.go:149-161`
  - `services/plans-svc/internal/identity/query/query.go:176-186`
  - `services/plans-svc/internal/identity/query/invitations.go:20-35`
  - `services/plans-svc/internal/identity/command/command.go:313-341`
  - `services/plans-svc/internal/identity/command/members.go:66-80`

  Hepsi aynı dizi: iki ParseID → NotFound(account); GetMembership NoRows ise NotFound; CanManage değilse forbidden. `actor` zaten var, ama query paketi ve `CreateApp` onu kullanmıyor. **Öneri:** `query` paketine tek bir `Membership(ctx, q, userID, accountID)` ve `RequireManager(...)` yazıp beş yerden çağırın.
- **[Yüksek]** `services/plans-svc/internal/identity/command/members.go:316-351` ↔ `:368-402` — `UpdateMemberRole` ve `RemoveMember` aynı bloğu taşıyor: `actor` → `LockAccount` → kilit altında çağıranın rolünü yeniden okuma → hedefin rolünü okuma → son sahip sayımı. **Öneri:** `lockedRoles(...)` ve `requireAnotherOwner(...)` yardımcılarına çıkarın.
- **[Orta]** Interval proto↔domain eşlemesi 6 yerde elle yazılmış, oysa `convert.IntervalToProto` (`services/plans-svc/internal/convert/helpers.go:17-22`) zaten var.
  - Proto→domain: `services/plans-svc/internal/domain/catalog.go:394-402`, `services/plans-svc/internal/subscriptions/command/create.go:61-69`, `services/plans-svc/internal/subscriptions/command/sync.go:172-177`, `services/plans-svc/internal/changes/query/query.go:183-193`
  - Domain→proto: `services/plans-svc/internal/convert/convert.go:276-280`, `services/plans-svc/internal/plans/query/query.go:94-97`

  **Öneri:** `convert.IntervalFromProto(i, fallback)` ekleyin; iki yönde de tek fonksiyon kullanılsın.
- **[Orta]** Dönem hesabı (PeriodIndex → PeriodStart → PeriodEnd) üç kez: `services/plans-svc/internal/subscriptions/command/lifecycle.go:248-258`, `services/plans-svc/internal/fees/query/query.go:36-50`, `services/plans-svc/internal/changes/query/query.go:329-340`. **Öneri:** `pkg/subscription`'a `PeriodAt(anchor, iv, t, loc) (start, end, err)` ekleyin.
- **[Orta]** "Aboneliğin sabit ücreti" dizisi (GetPlanVersionByID + `plansqry.FeeIn(..., t.Currency, row.Currency, iv)`) 5 yerde:
  - `services/plans-svc/internal/fees/command/command.go:62-69`
  - `services/plans-svc/internal/fees/query/query.go:93-101`
  - `services/plans-svc/internal/changes/query/query.go:95-111`
  - `services/plans-svc/internal/changes/query/query.go:320-328`
  - `services/plans-svc/internal/subscriptions/command/planchange.go:85-101`

  "Duraklatılmış değilse kupon indirimi" mantığı da `fees/command/command.go:59-74` ile `fees/query/query.go:102-108` arasında kopya. **Öneri:** `plansqry.SubscriptionFee(...)` ve `FeeWithDiscount(...)` yardımcıları.
- **[Orta]** "EndSubscription, n != 1 ise Aborted `subscription changed concurrently; retry`" 6 kez:
  - `services/plans-svc/internal/subscriptions/command/lifecycle.go:59-65`, `:278-282`, `:294-298`
  - `services/plans-svc/internal/subscriptions/command/planchange.go:148-152`
  - `services/plans-svc/internal/changes/command/command.go:54-58`
  - `services/plans-svc/internal/providermigrations/command/command.go:47-51`

  **Öneri:** `subsqry.EndAt(ctx, q, id, at) error` yardımcısı.
- **[Orta]** `q.GetSubscriptionForUpdate` + `sqlc.GetSubscriptionRow(cur)` 9 kez:
  - `services/plans-svc/internal/subscriptions/command/lifecycle.go:51-55`, `:130-134`, `:214-218`
  - `services/plans-svc/internal/changes/command/command.go:103-107`, `:213-217`
  - `services/plans-svc/internal/coupons/command/command.go:178-182`
  - `services/plans-svc/internal/providermigrations/command/command.go:111-115`
  - `services/plans-svc/internal/subscriptions/command/planchange.go:69-73`
  - `services/plans-svc/internal/subscriptions/command/sync.go:87-91`

  **Öneri:** `subsqry.Lock(ctx, q, id)`.
- **[Orta]** `tenantsqry.Current` + `tenantsqry.Location` çifti 8 kez:
  - `services/plans-svc/internal/fees/command/command.go:38-45`
  - `services/plans-svc/internal/fees/query/query.go:72-79`
  - `services/plans-svc/internal/subscriptions/command/create.go:94-101`
  - `services/plans-svc/internal/subscriptions/command/lifecycle.go:206-213`
  - `services/plans-svc/internal/subscriptions/command/planchange.go:61-68`
  - `services/plans-svc/internal/changes/query/query.go:82-89`, `:312-319`
  - `services/plans-svc/internal/reports/query/query.go:72-81`

  **Öneri:** `tenantsqry.CurrentIn(ctx, q) (row, *time.Location, error)`.
- **[Orta]** "Değiştirilebilir mi" guard'ı 5 varyantla tekrar ediyor (RequirePayrudderOwned + OpenEnded/CancelAtPeriodEnd + Active/Trialing):
  - `services/plans-svc/internal/providermigrations/command/command.go:117-127`
  - `services/plans-svc/internal/subscriptions/command/planchange.go:115-133`
  - `services/plans-svc/internal/changes/query/query.go:146-161`
  - `services/plans-svc/internal/subscriptions/command/lifecycle.go:94-100`, `:135-140`

  **Öneri:** tek bir `domain.RequireOpen(row, now, allowed...)`.
- **[Orta]** `services/plans-svc/internal/convert/convert.go:89-107`, `:159-177`, `:180-198` — Address/Legal/BankTransfer JSON ↔ proto fonksiyonları, tip dışında aynı gövdenin üç kopyası. **Öneri:** generic `toJSON` / `fromJSON[T]`.
- **[Orta]** UUID ayrıştırma + "must be a UUID" dört ayrı fonksiyonda ve satır içinde de tekrar ediyor.
  - `services/plans-svc/internal/admin/admin.go:199-205` ile `services/plans-svc/internal/webhookendpoints/query/query.go:26-32` birebir aynı.
  - Diğer varyantlar: `services/plans-svc/internal/subscriptions/query/query.go:29-35`, `services/plans-svc/internal/identity/query/query.go:128-131` (bool döndüren).
  - Satır içi: `services/plans-svc/internal/plans/query/query.go:55-58`, `services/plans-svc/internal/customers/query/query.go:54-57`, `services/plans-svc/internal/providermigrations/command/command.go:165-168`, `:238-241`, `services/plans-svc/internal/providermigrations/query/query.go:43-46`, `services/plans-svc/internal/changes/command/command.go:261-264`, `services/plans-svc/internal/subscriptions/command/sync.go:167-169`, `services/plans-svc/internal/admin/admin.go:284-287`.

  **Öneri:** tek bir `domain.ParseID(field, raw)`.
- **[Orta]** Sayfalama her listede farklı sayılarla yeniden yazılmış:

  | Yer | Varsayılan / üst sınır | Aşımda |
  |---|---|---|
  | `services/plans-svc/internal/customers/query/query.go:71-77` | 100 / 500 | hata |
  | `services/plans-svc/internal/admin/admin.go:226-232` | 500 / 1000 | hata |
  | `services/plans-svc/internal/subscriptions/query/query.go:100-106` | 50 / 200 | hata |
  | `services/plans-svc/internal/webhookendpoints/query/query.go:84-90` | 50 / 200 | hata |
  | `services/plans-svc/internal/fees/query/query.go:65-69` | 100 / 500 | sessiz kırpma |
  | `services/plans-svc/internal/changes/query/query.go:260-264` | 100 / 500 | sessiz kırpma |

  Token biçimleri de farklı: base64, çıplak UUID, `zaman|uuid`. **Öneri:** `domain.PageSize(req, def, max)` ve generic `page[T]`; aşım davranışını tekleştirin.
- **[Orta]** Parola hash'leme ve uzunluk kontrolü üç kez kopya:
  - "Slot içinde `credential.Hash`" + `password hashing failed`: `services/plans-svc/internal/identity/command/command.go:148-155`, `members.go:473-480`, `verify.go:195-202`
  - Uzunluk kontrolü: `command.go:136-138`, `members.go:447-449`, `verify.go:183-185`. Buradaki "between 10 and 256" metni `MinPasswordLen`/`MaxPasswordLen` sabitlerinden bağımsız sabit sayı yazıyor.

  **Öneri:** `c.hashPassword` ve `validatePassword` yardımcıları; mesajı sabitlerden üretin.
- **[Orta]** `services/plans-svc/internal/identity/command/command.go:158-170` ↔ `services/plans-svc/internal/identity/command/signin.go:175-185` — kullanıcı + hesap + sahip üyeliği oluşturma iki kez yazılmış. **Öneri:** `createUserWithAccount`.
- **[Orta]** `services/plans-svc/internal/identity/command/members.go:481-513` ↔ `command.go:255-276` — `AcceptInvitation`, `openSession`'ın oturum açma kısmını kopyalıyor. **Öneri:** `openSession`'ı yeniden kullanın.
- **[Orta]** `services/plans-svc/internal/identity/command/command.go:312-360` ↔ `services/plans-svc/internal/admin/admin.go:119-153` — self-servis `CreateApp`, operatör `CreateApp`'in akışını kopyalıyor: sahte `CreateAccountRequest` ile doğrulama, `ResolveSlug`, ve aynı `plans.app.slug_taken` errkey eşlemesi (`admin.go:146-148`, `command.go:352-354`). **Öneri:** `admin`'de ortak bir `CreateAppIn(ctx, q, ...)`.
- **[Orta]** `services/plans-svc/internal/identity/command/members.go:147-216` ↔ `:232-273` — `InviteMember` ile `ResendInvitation` aynı adımları tekrarlıyor: inviter yükleme, doğrulanmış e-posta kontrolü, token, RefreshInvite, mail kurulumu. **Öneri:** `loadVerifiedInviter` ve `sendInvite`.
- **[Orta]** `services/plans-svc/internal/identity/credential/credential.go:124-131` ↔ `:109-116` ve `:140-142` ↔ `:119-121` — `NewToken` aslında `NewPrefixedToken(TokenPrefix)`, `WellFormedToken` aslında `WellFormed(TokenPrefix, s)`; gövdeler kopya, `43`/`32` sihirli sayıları iki yerde. **Öneri:** tek satırlık sarmalayıcılar ve isimli sabitler.
- **[Orta]** `services/plans-svc/internal/identity/grpc.go:67`, `:160`, `:168`, `:234` — `Opened` → yanıt eşlemesi dört kez. **Öneri:** tek bir dönüştürücü.
- **[Orta]** `services/plans-svc/internal/identity/query/query.go:195-205` ↔ `services/plans-svc/internal/admin/admin.go:186-195` — app'lere tenant gruplayan O(n·m) döngü iki yerde birebir aynı. **Öneri:** map ile gruplayan `convert.Apps(apps, tenants)`.
- **[Orta]** `services/plans-svc/internal/webhookendpoints/command/command.go:95-103` ↔ `:180-187` — `newSecret` + `box.Seal` Create ve Rotate'te kopya. **Öneri:** `c.sealedSecret(id)`.
- **[Orta]** `services/plans-svc/cmd/plans-svc/main.go:104` ↔ `services/gateway/internal/auth/auth.go:193`, `:212` — Redis anahtarı `"gw:apikey:"+prefix` iki serviste ayrı literal (gizli bağımlılık; biri değişirse iptal edilen anahtar cache'te kalır). **Öneri:** `pkg/apikey`'e `CacheKey(prefix)` ekleyin.
- **[Düşük]** `^[A-Z]{2}$` regex'i üç kez: `services/plans-svc/internal/domain/tenant.go:215`, `services/plans-svc/internal/domain/routing.go:19`, `services/plans-svc/internal/tax/command/command.go:18`. **Öneri:** tek `domain.ValidCountry`.
- **[Düşük]** `ContainsAny(s, "\n\r\t\x00")` üç kez (`services/plans-svc/internal/domain/address.go:29`, `bank_transfer.go:19`, `einvoice.go:30`); oysa `oneLine` (`einvoice.go:29-31`) zaten var. **Öneri:** `oneLine`'ı paylaşın.
- **[Düşük]** Para birimi doğrulaması ve aynı metin tekrar ediyor: `services/plans-svc/internal/domain/routing.go:40-42`, `tenant.go:100-101`, `:207-210`, `services/plans-svc/internal/subscriptions/command/create.go:150-151`. `bank_transfer.go:34-38` ise `LookupCurrency` yerine yalnızca `len==3` bakıyor (tutarsız). **Öneri:** `(*Violations).Currency(...)`.
- **[Düşük]** Metin kısaltma üç yerde, farklı kurallarla: `services/plans-svc/internal/identity/usermail/usermail.go:163-169` (bayt keser, UTF-8'i bölebilir), `services/plans-svc/internal/identity/command/signin.go:193-199`, `services/plans-svc/internal/webhooks/dispatcher.go:220-225`. **Öneri:** rune-güvenli tek bir yardımcı.
- **[Düşük]** Kupon alanları ve eşlemeleri:
  - Nullable alanları açma kodu `Message` ile `Discount` arasında kopya: `services/plans-svc/internal/coupons/query/query.go:36-50` ↔ `:86-97`.
  - `durationToDB`/`durationFromDB` iki pakete bölünmüş: `services/plans-svc/internal/coupons/command/command.go:26-30` ↔ `query.go:25-29`.

  **Öneri:** `termsOf(row)` yardımcısı; iki yönlü eşleme tek dosyada.
- **[Düşük]** Boş string → `*string` elle yapılıyor: `services/plans-svc/internal/customers/command/command.go:45-58`, `services/plans-svc/internal/routing/command/command.go:36-42`. **Öneri:** `nonEmpty(s) *string`.
- **[Düşük]** Identity query/command arasında küçük kopyalar:
  - Aynı forbidden errkey: `services/plans-svc/internal/identity/query/invitations.go:34` ↔ `command.go:306-308`
  - Davet eşlemesi: `invitations.go:42-43` ↔ `members.go:119-122`
  - `Session` içinde `User` eşlemesi elle: `query.go:88-89` ↔ `:124`

  **Öneri:** `query` paketinde tek `Forbidden()`, `Invitation()`, `User()`.
- **[Düşük]** `services/plans-svc/internal/webhooks/subscription_event.go:20-23` ↔ `webhooks.go:65-68` — `HasWebhookTargets` her abonelik olayında iki kez sorgulanıyor. **Öneri:** `Emit`'in hedef kontrolsüz iç kısmını ayrı fonksiyona alın; `EmitSubscription` o iç fonksiyonu çağırsın.
- **[Düşük]** İki küçük kopya grubu:
  - `convert.Meter` + `convert.Err(err,"meter")` üç kez: `services/plans-svc/internal/meters/command/command.go:68-71`, `meters/query/query.go:42-45`, `:61-64`.
  - Aynı 9 argümanlı `Assemble` çağrısı iki kez: `services/plans-svc/internal/plans/query/query.go:63`, `:73`.

  **Öneri:** `meterMessage(row)` yardımcısı; `Assemble`'a satır struct'ı geçin.
- **[Düşük — test]** `services/plans-svc/internal/itest/harness_integration_test.go:142-157` ↔ `services/plans-svc/cmd/plans-svc/main.go:256-272` — harness üretim wiring'ini satır satır kopyalıyor. **Öneri:** ortak bir `grpcapi.Register(srv, Deps)`.
- **[Düşük — test]** CreatePlan + PublishPlanVersion + `t.Fatal` kurulum bloğu 9+ kez tekrar ediyor: `coupons_integration_test.go:23-29`, `dashboard_integration_test.go:25-30`, `changes_integration_test.go:25`, `cancel_credit_integration_test.go:19-24`, `lifecycle_integration_test.go:24`, `:778`, `:817`, `multicurrency_integration_test.go:39`, `webhooks_integration_test.go:140`. `must` yardımcısı yalnızca `plans_integration_test.go:70` içinde yerel. `code()` (`plans_integration_test.go:61-64`) `status.Code`'un gereksiz sarmalayıcısı. **Öneri:** `e.plan(...)` ve paket düzeyinde `must`.

### Overengineering

- **[Orta]** `services/plans-svc/internal/identity/command/command.go:45-54` — `AppFactory` ve `KeyAdmin` arayüzlerinin tek uygulaması `admin.Server`; main (`main.go:272`) ve test (`harness:157`) aynı nesneyi iki kez geçiyor. Paket zaten `admin`'i import ediyor (`command.go:24`), yani arayüz ayrıştırma da sağlamıyor. **Öneri:** somut `*admin.Server` tek parametre olsun.
- **[Orta]** Modül başına grpc/command/query üçlüsü gereğinden ağır:
  - 13 adet saf geçiş yapan `grpc.go` var (ör. `services/plans-svc/internal/tax/grpc.go:21-35`).
  - Bazı paketler kalıba zorla uyuyor: `services/plans-svc/internal/tenants/query/query.go` (tip yok), `services/plans-svc/internal/entitlements/command/command.go` (tek fonksiyon), `services/plans-svc/internal/subscriptions/command/command.go:1-18` (yalnızca struct).
  - Komutlar `Message`, `Rules`, `ParseID` gibi yardımcıları query paketlerinden import ediyor; `fees/command` 6 iç paket import ediyor.

  **Öneri:** küçük modüllerde command/query'yi tek pakette birleştirin.
- **[Orta]** `services/plans-svc/internal/grpcapi/grpcapi.go:55-95` — 12 tip takma adı, bir seviye derine gömülen `unimplemented` ve bunu doğrulayan yansıma testi (`grpcapi_test.go:12-39`). Hangi metodun kazandığı görünmüyor. **Öneri:** açık (üretilmiş) delegasyon ya da modül başına ayrı servis.
- **[Düşük]** `services/plans-svc/internal/identity/usermail/usermail.go:104-108` — `Discard` hiçbir yerde kullanılmıyor (testler `recordingMail` kullanıyor). **Öneri:** silin.
- **[Düşük]** `services/plans-svc/internal/webhooks/dispatcher.go:43-46`, `:51-67` — `Every`/`Batch`/`Workers` ne main'de ne testte set ediliyor. **Öneri:** sabite çevirin.
- **[Düşük]** `services/plans-svc/internal/grpcapi/grpcapi.go:98` — `_ *slog.Logger` parametresi kullanılmıyor; `db`/`now` alanları (`:93-94`) yalnızca `NewWebhooks` (`:123-125`) için tutuluyor. **Öneri:** parametreyi kaldırın; webhook handler'ını `New` içinde kurun.
- **[Düşük]** `services/plans-svc/internal/identity/command/command.go:71-76` — `WithPepper` kurulumdan sonra nesneyi değiştiren bir builder. **Öneri:** pepper `New`'e parametre olarak verilsin.
- **[Düşük]** Gereksiz küçük sarmalayıcılar:
  - `services/plans-svc/internal/convert/helpers.go:11-14`: `Str`/`UUIDStr` gizli `str`/`logoID`'yi sarıyor.
  - `logo.go:5-10`: `logoID` genel bir nil-UUID→string fonksiyonu ama logoya özel adla.
  - `services/plans-svc/internal/admin/util.go:9`: tek fonksiyonluk dosya, `convert.ts`'nin kopyası.

  **Öneri:** `logo.go`/`util.go` silinsin.
- **[Düşük]** Yalnızca kendi paketinde kullanılan dışa açık semboller (grep ile doğrulandı): `services/plans-svc/internal/domain/violations.go:22` (`Empty`), `webhooks/webhooks.go:43` (`Types`), `webhooks/dispatcher.go:130` (`Envelope`), `entitlements/command/command.go:19` (`Topic`), `identity/command/command.go:35-42`. **Öneri:** küçük harfe çevirin.
- **[Düşük]** `services/plans-svc/internal/convert/convert.go:266-269` — `Now` sıfırsa `time.Now()`'a düşüyor; tek çağıran (`subscriptions/query/query.go:39-44`) her zaman `Now` veriyor, enjekte edilen saati baypas etme riski var. **Öneri:** fallback'i kaldırın.
- **[Düşük]** `services/plans-svc/internal/domain/tenant.go:140-148` — `ValidateGrace` girdiyi değiştirmeden geri döndürüyor. **Öneri:** yalnızca `error` döndürsün.

### Spagetti kod

- **[Orta]** `services/plans-svc/internal/admin/admin.go:255-324` — `UpdateTenantSettings` ~70 satır, 8 alanın doğrulaması tek fonksiyonda. `:270-272`'deki `else` gereksiz (zaten sıfır değer). **Öneri:** `domain.ValidateTenantSettings(req)` ve alan başına yardımcılar.
- **[Orta]** `services/plans-svc/internal/domain/catalog.go:237-316` — `priceToStored` ~80 satır: döngü içinde iç içe switch, `ok` bayrağı, ve hataları iki kanaldan bildiriyor (`*Violations`'ı değiştirip ayrıca `errSkip` döndürüyor). **Öneri:** model başına üç fonksiyon; hata yalnızca `Violations` ile bildirilsin.
- **[Orta]** `services/plans-svc/internal/changes/command/command.go:92-162` — `Change`'in ~70 satırlık transaction closure'ı 6 işi birden yapıyor. **Öneri:** `scheduleChange` / `applyNow` olarak ayırın.
- **[Orta]** `services/plans-svc/internal/changes/command/command.go:197-257` — `ApplyScheduled` ~60 satır. **Öneri:** döngü gövdesini `applyOne(...)`'a çıkarın.
- **[Orta]** `services/plans-svc/internal/providermigrations/command/command.go:88-161` ve `:164-234` — `Start` ~73 ve `Complete` ~70 satır; doğrulama, durum makinesi ve cutover karışık. `cutover` (`:41-42`) 8 parametre alıyor. **Öneri:** durum geçiş tipi ve `cutoverArgs` struct'ı.
- **[Orta]** `services/plans-svc/internal/subscriptions/command/sync.go` akışı karışık:
  - `syncProvider` (`:85-115`) `id`'yi iki kez yeniden atıyor.
  - `followProviderPlan` (`:163-209`) 7 parametre ve `linked bool` alıyor, hata durumunda bile `row.ID` döndürüyor.
  - `currentInChain` (`:119-136`) DB hatalarını yutup sihirli `64` ile döngü kuruyor.

  **Öneri:** bir struct üzerinde yöntemler; zincir yürüyüşü recursive CTE ile yapılsın.
- **[Orta]** `services/plans-svc/internal/identity/command/members.go:132-217` — `InviteMember` ~85 satır, `open2` gibi isimler var. **Öneri:** yenile/ekle yolları ve kota kontrolü ayrı fonksiyonlara.
- **[Orta]** `services/plans-svc/internal/identity/command/signin.go:207-263` — iç içe switch (`:221-251`). **Öneri:** `resolveIdentityUser(...)`.
- **[Orta]** `services/plans-svc/internal/identity/command/members.go:444-523` — `AcceptInvitation` ~80 satır. **Öneri:** `hashPassword` ve `openSession`'ı yeniden kullanın.
- **[Düşük]** `services/plans-svc/internal/changes/query/query.go:292-346` — `UnusedFee` 4 değer döndürüyor; `return 0, time.Time{}, false, err` 13 kez yazılmış. **Öneri:** 3 değer ya da sonuç struct'ı.
- **[Düşük]** `services/plans-svc/internal/domain/catalog.go:177-233` — iki döngü aynı meter kontrolünü yapıyor. **Öneri:** `checkMeter(...)`.
- **[Düşük]** N+1 sorgu: `services/plans-svc/internal/fees/query/query.go:71-115` ve `services/plans-svc/internal/changes/query/query.go:266-282` satır başına `GetSubscription` (+ plan sürümü) çağırıyor. **Öneri:** `ANY($1)` ile toplu sorgu.
- **[Düşük]** `services/plans-svc/cmd/plans-svc/main.go:272` — tek satırda dört iç içe kurucu. **Öneri:** ara değişkenlere bölün.
- **[Düşük]** `services/plans-svc/internal/reports/query/query.go:71-100` — iki ayrı transaction açılıyor; `:59`'daki `tenant.Require` gereksiz. **Öneri:** tek `Tx`.

### Clean code ihlalleri

- **[Orta]** Sahip ve ücret zamanlaması sihirli string: `"advance"` 11, `"arrears"` 5, `"payrudder"` 8, `"provider"` 16 kez. Örnekler: `services/plans-svc/internal/subscriptions/command/create.go:133`, `:198-210`, `coupons/command/command.go:146`, `providermigrations/command/command.go:60`, `:69`, `:134`, `changes/command/command.go:70`, `:138`, `subscriptions/command/sync.go:49`, `:101`, `:199`, `domain/subscription.go:17`, `convert/convert.go:258`, `:295`. Oysa `pkg/payments/capabilities.go:58-59`'da `OwnerPayrudder`/`OwnerProvider` var ve kullanılmıyor. **Öneri:** bu sabitleri kullanın; FeeTiming sabitlerini `pkg/subscription`'a ekleyin.
- **[Orta]** Yutulan ya da yanlış sınıflanan hatalar:
  - `services/plans-svc/internal/coupons/command/command.go:149`: `err == nil` dışındaki her hata "kupon yok" sayılıyor. Aynı kontrol `changes/query/query.go:307-311`'de doğru yapılmış.
  - `changes/command/command.go:113`: hata yutuluyor.
  - `changes/command/command.go:175-178`: her hata FailedPrecondition'a dönüşüyor.
  - `subscriptions/command/sync.go:121`, `:127`: hatalar yutuluyor; `:150`: satır sayısı yok sayılıyor.
  - `identity/command/command.go:237`: `RecordFailedLogin` hatası yok sayılıyor, yani kilitleme sessizce çalışmayabilir.

  **Öneri:** `pgx.ErrNoRows`'u açıkça ayırın; kalan hataları döndürün ya da loglayın.
- **[Orta]** `_` ile atılan değerler:
  - ID ayrıştırma: `services/plans-svc/internal/identity/command/command.go:392`, `:414`, `:431`
  - `json.Marshal`: `domain/catalog.go:105`, `:314`
  - `convert/convert.go:93-96`, `:163-166`, `:184-187`: marshal hatasında veri sessizce `{}` olup siliniyor
  - `changes/command/command.go:286-291`: hata → `"null"`
  - `reports/query/query.go:46`: `Sscanf`
  - `cmd/plans-svc/main.go:250`: `ParseKey`

  **Öneri:** hatayı döndürün ya da `must` ile açıkça belgeleyin.
- **[Orta]** `services/plans-svc/internal/convert/convert.go:45-50` — her exclusion violation "overlapping subscription", her raise exception "tenant immutable" diye raporlanıyor; yeni bir constraint ya da trigger eklenince mesaj yanlış olur. **Öneri:** `pgxx.ConstraintName` ile eşleyin.
- **[Orta]** Hata eşleme tutarsız:
  - `services/plans-svc/internal/reports/query/query.go:101-103`, `:136-138` doğrudan `codes.Internal` döndürüyor (iptal/deadline de Internal oluyor).
  - `admin/admin.go:145-148` `pgxx.ErrCode` yerine çıplak `"23505"` kullanıyor.
  - `admin/admin.go:409-411` `convert.Err`'in zaten ürettiği NotFound'u elle tekrar üretiyor.

  **Öneri:** her yerde `convert.Err` + `pgxx.ErrCode`.
- **[Orta]** Tek istekte tutarsız saat:
  - `services/plans-svc/internal/changes/command/command.go:218` `req.GetNow()` kullanıyor; `:230`, `:243`, `:247` ise `c.now()`.
  - `subscriptions/command/lifecycle.go:37`, `:45`, `:68`, `:72`, `:81`, `:85`: aynı transaction'da 6 ayrı `c.now()`.
  - `lifecycle.go:222` `now` alıyor ama `:237`, `:239` yine `c.now()` kullanıyor.

  **Öneri:** transaction başında tek `now`.
- **[Orta]** Zaman damgası doğrulaması tutarsız. `CheckValid()` şu yerlerde çağrılmıyor: `services/plans-svc/internal/subscriptions/command/lifecycle.go:38-40`, `providermigrations/command/command.go:209-211`, `subscriptions/command/sync.go:57-59`, `webhookendpoints/command/command.go:274-276`, `subscriptions/query/query.go:149`. Oysa Start, ChangePlan ve parseCreate çağırıyor. **Öneri:** `domain.OptionalTime(...)`.
- **[Orta]** Deterministik olmayan hata mesajı: `services/plans-svc/internal/domain/address.go:20-25` ve `bank_transfer.go:15` alanları map üzerinde dolaşıyor, `violations.go:29` mesajı `list[0]`'dan kuruyor. Aynı geçersiz istek her çağrıda farklı mesaj üretebilir. **Öneri:** sıralı slice.
- **[Orta]** `services/plans-svc/internal/identity/usermail/texts.go:18`, `:24`, `:30`, `:42` (+ 5 dil daha) — metinlerdeki 24 saat / 1 saat / 7 gün / 10 dakika, `VerifyTTL`, `ResetTTL`, `InviteTTL`, `CodeTTL` sabitlerinin metne gömülmüş kopyası; sabit değişirse maillerdeki süre yanlış olur. **Öneri:** `{ttl}` yer tutucusu ya da bunu yakalayan bir test.
- **[Orta]** `services/plans-svc/internal/subscriptions/query/query.go:169` — `< 500`, SQL'deki `LIMIT 500` ile (`internal/store/sqlc/plans.sql.go:1781`) gizlice bağlı. **Öneri:** limit parametre olsun ve aynı sabit iki tarafta kullanılsın.
- **[Orta]** Doğrulama katmanı tutarsız. Vergi (`services/plans-svc/internal/tax/command/command.go:27-49`), kupon (`coupons/command/command.go:63-119`), webhook (`webhookendpoints/command/command.go:49-69`) ve kayıt (`identity/command/command.go:132-147`) doğrulaması komut katmanında. `domain` ise dört ayrı hata türü üretiyor (`einvoice.go:26`, `subscription.go:22`, `tenant.go:128-134`). **Öneri:** doğrulama domain'e toplansın, tek hata türü kullanılsın.
- **[Orta]** `services/plans-svc/internal/admin/admin.go:1-3` — yorum "system pool'u tutan tek paket" diyor, ama `identity/command/command.go:79` ve `identity/query/query.go:47` de system pool (BYPASSRLS) kullanıyor. Yorum kodla çelişiyor ve güvenlik varsayımını yanıltıyor. **Öneri:** yorumu düzeltin ya da identity'nin erişimini daraltın.
- **[Orta]** `tenant.Require` 12 kez yalnızca ID almak için transaction'dan önce çağrılıyor (ör. `services/plans-svc/internal/subscriptions/command/create.go:77`, `lifecycle.go:41`, `:200`, `changes/command/command.go:97`, `:201`, `providermigrations/command/command.go:105`, `:172`). `store/db.go:24` aynı kontrolü zaten yapıyor. **Öneri:** `Tx` tenantID'yi fonksiyona geçsin.
- **[Düşük]** Uzun parametre listeleri: `services/plans-svc/internal/plans/query/query.go:83-84` (9), `:143` (7), `entitlements/command/command.go:26-27` (8), `convert/convert.go:214` (5 konumsal), `identity/command/members.go:66` (5 dönüş değeri). **Öneri:** struct'lar.
- **[Düşük]** Boolean bayrak parametreleri: `services/plans-svc/internal/grpcapi/grpcapi.go:123`, `subscriptions/command/sync.go:163`, `subscriptions/command/lifecycle.go:94`. **Öneri:** seçenek struct'ı.
- **[Düşük]** Diğer sihirli değerler:
  - Fiyat modelleri: `services/plans-svc/internal/domain/catalog.go:72-76`, `:246`, `:294`, `:309`, `:322`, `:328`, `:342`
  - Migration durumları: `providermigrations/command/command.go:129`, `:191`, `:205`, `:249-255`
  - Değişiklik durumları: `changes/command/command.go:114`, `:179`, `:220`, `:235`
  - Teslimat durumları: `webhooks/dispatcher.go:144-159` ↔ `webhookendpoints/query/query.go:39-43`, `:100-108`
  - `"sandbox"`/`"live"`: `admin/admin.go:90-91`, `:101`, `:350`, `domain/tenant.go:55-57`, `:167` (oysa `tenant.EnvSandbox` var)
  - `200` uzunluk sınırı 16 kez
  - `admin.go:406`: mesajda `8` (oysa `apikey.PrefixLen` var)
  - `subscriptions/query/query.go:107` ve `webhookendpoints/query/query.go:109`: `MustParse("ffff…")` (oysa uuid v1.6.0'da `uuid.Max` var)

  **Öneri:** isimli sabitler ya da enum tipleri.
- **[Düşük]** İsimlendirme: `services/plans-svc/internal/webhooks/webhooks.go:65` (`any` yerleşik tipi gölgeliyor), `identity/command/members.go:192` (`open2`), `convert/convert.go:156` (`addressJSON` banka ve legal için de kullanılıyor), `convert/logo.go:5`, `identity/usermail/usermail.go:110` (yorum ile tip adı uyuşmuyor), `itest/apps_integration_test.go:86-87` (`nil2`). **Öneri:** anlamlı adlar.
- **[Düşük]** `services/plans-svc/internal/identity/command/command.go:286` — `domainInvalid`, `domain.Invalid`'in yalnızca `:205`'te kullanılan bir takma adı. **Öneri:** silin.
- **[Düşük]** `services/plans-svc/internal/convert/convert.go:236-247` — iş kuralı olan `SubscriptionState` mapping paketinde duruyor ve 9 kez aynı 5 argümanla çağrılıyor. **Öneri:** domain'e ya da `subsqry.Effective(row, now)`'a taşıyın.
- **[Düşük]** `services/plans-svc/internal/customers/command/command.go:117-137` — `SetPaymentMethod` `external_id`'yi kontrol etmiyor, doğrulamayı tx içinde yapıyor ve hatayı çift sarıyor (aynı çift sarma `domain/tenant.go:197-199`'da da var). **Öneri:** doğrulama `Violations`'a eklensin.
- **[Düşük]** Locale önceliği her akışta farklı: `services/plans-svc/internal/identity/command/command.go:131`, `members.go:209-212`, `verify.go:170-173`, `signin.go:70-73`. **Öneri:** tek bir `pickLocale`.
- **[Düşük]** `services/plans-svc/internal/identity/query/query.go:134-145` — `NotFound(what string)` bilinmeyen her değeri sessizce "api key" hatasına düşürüyor. **Öneri:** ayrı fonksiyonlar.
- **[Düşük]** `services/plans-svc/internal/identity/query/query.go:232`, `:248-252` — sqlc tipleri alan alan elle kopyalanıyor; tabloya alan eklenince sessizce eksik kalır. **Öneri:** `sqlc.embed`.
- **[Düşük]** `services/plans-svc/internal/subscriptions/command/planchange.go:123` — gRPC hata metninde gateway'in REST yolu geçiyor. **Öneri:** RPC adını yazın.
- **[Düşük]** `services/plans-svc/internal/identity/usermail/texts.go:169-172` — İspanyolca KindCode metni resmi "usted", diğer İspanyolca metinler samimi "tú". **Öneri:** hitabı tutarlı yapın.
- **[Düşük]** `services/plans-svc/cmd/plans-svc/main.go:16` ve `:38` — `time/tzdata` ve `pkg/tzdata` birlikte import ediliyor; `pkg/tzdata` zaten yalnızca `time/tzdata`'yı import ediyor. **Öneri:** yalnızca `pkg/tzdata` kalsın.

### Özet

- En büyük tema abonelik yaşam döngüsündeki tekrar. "Bitir + halef + entitlement" (`SwitchTo`/`replacePlan`/`cutover`), dönem ve ücret hesabı, kilitleme ve guard'lar birkaç pakette kopya. Bu kopyalar şimdiden davranış farkı üretiyor: plan değişikliğinde kupon taşınmıyor.
- Identity modülünde yoğun iç kopya var: üyelik kontrolü 5 kez, parola hash/uzunluk 3 kez, kullanıcı+hesap oluşturma 2 kez, oturum açma 2 kez, rol kilidi 2 kez. Var olan `pkg/period` ve `payments.Owner*` kullanılmıyor.
- Sihirli string'ler (`"advance"`, `"provider"`, durum adları) ve gizlice bağlı değerler (SQL `LIMIT 500`, mail metnindeki TTL'ler, `gw:apikey:` anahtarı) iş kurallarını kırılgan yapıyor.
- Hata işleme tutarsız: `err == nil` ile yutulan DB hataları, `_` ile atılan marshal/parse hataları, karışık `convert.Err` / `codes.Internal` / `"23505"` kullanımı, domain'de dört ayrı hata türü. Saat tek istek içinde birden çok kez ve farklı kaynaklardan okunuyor.
- Mimari tören maliyeti yüksek: modül başına grpc/command/query üçlüsü, embed + takma ad + yansıma testli `grpcapi.Server`, tek uygulamalı arayüzler, kullanılmayan ayar alanları. Sayfalama, ID ayrıştırma ve doğrulama gibi kesişen işler ortak yardımcılara taşınmamış.
