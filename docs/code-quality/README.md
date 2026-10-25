# Kod kalitesi raporu

Tarih: 2026-10-21 · Kapsam: tüm quotify reposu (`services/*`, `pkg/`, `web/admin`, `tests/`, `tools/`, `deploy/`, build dosyaları) · Commit: `7e9178d`

Rapor beş başlıkta toplanıyor:

1. Kod tekrarı
2. Overengineering
3. Spagetti kod
4. Clean code ihlalleri
5. Mantık hataları ve [ADR 0020](../adr/0020-modules-cqrs-errors-config.md) (modül tabanlı CQRS) uyumu

Her bulgu bir `dosya:satır` konumu, açıklama, önem derecesi (Yüksek / Orta / Düşük) ve somut bir öneri içeriyor. Mantık hatası bulgularında bir de senaryo ve güven seviyesi (Kesin / Muhtemel) var. Bulgular kod okunarak üretildi; "kullanılmıyor" iddiaları repo genelinde grep ile kontrol edildi. Yüksek önemli bulguların bir kısmı ayrıca elle yeniden doğrulandı. Hiçbir kaynak dosya değiştirilmedi.

## Dosyalar

| Dosya | Konu | Yüksek | Orta | Düşük | Toplam |
|---|---|---:|---:|---:|---:|
| **Mantık hataları** | | | | | |
| [logic-billing-svc.md](logic-billing-svc.md) | billing-svc: tahsilat, iade, kredi, vergi, re-rating | 6 | 8 | 5 | 19 |
| [logic-plans-svc.md](logic-plans-svc.md) | plans-svc: plan değişikliği, yaşam döngüsü, kupon, kimlik | 2 | 7 | 8 | 17 |
| [logic-gateway-payments.md](logic-gateway-payments.md) | gateway, payments-worker, invoice-worker | 1 | 12 | 12 | 25 |
| [logic-event-services.md](logic-event-services.md) | aggregator, ingest, limits, notifier, assets, analytics | 1 | 8 | 9 | 18 |
| [logic-pkg-web.md](logic-pkg-web.md) | pkg ve web/admin | 2 | 8 | 15 | 25 |
| **Modül tabanlı CQRS / ADR 0020** | | | | | |
| [cqrs-plans-billing.md](cqrs-plans-billing.md) | plans-svc ve billing-svc | 7 | 22 | 20 | 49 |
| [cqrs-other-services.md](cqrs-other-services.md) | diğer 9 servis | 12 | 27 | 16 | 55 |
| **Tekrar, overengineering, spagetti, clean code** | | | | | |
| [cross-service.md](cross-service.md) | Servisler arası kod tekrarı (19 tema) | 4 | 7 | 8 | 19 |
| [billing-svc.md](billing-svc.md) | billing-svc | 4 | 45 | 50 | 99 |
| [plans-svc.md](plans-svc.md) | plans-svc | 4 | 45 | 37 | 86 |
| [gateway.md](gateway.md) | gateway | 5 | 29 | 37 | 71 |
| [payments-invoice-workers.md](payments-invoice-workers.md) | payments-worker, invoice-worker | 2 | 30 | 34 | 66 |
| [limits-svc.md](limits-svc.md) | limits-svc | 3 | 25 | 30 | 58 |
| [small-services.md](small-services.md) | aggregator, ingest, analytics, notifier, assets | 0 | 26 | 63 | 89 |
| [pkg.md](pkg.md) | Paylaşılan kütüphane | 2 | 22 | 38 | 62 |
| [web-admin.md](web-admin.md) | Operatör ve ürün konsolu (Vue) | 10 | 62 | 32 | 104 |
| [tests-tools-deploy.md](tests-tools-deploy.md) | tests/, tools/, deploy/, Dockerfile, Makefile | 2 | 19 | 31 | 52 |
| **Toplam** | | **67** | **402** | **445** | **914** |

## Önce düzeltilmesi gerekenler

Aşağıdakiler kalite değil, doğruluk sorunu. Çoğunda para ya da veri kaybı var.

**Para hareketi**

- **Dunning çift tahsilat yapıyor.** Kısmi havale kaydedilmiş olsa bile tahsilat her zaman faturanın tamamını çekiyor (`services/billing-svc/internal/saga/engine.go:446`). → [logic-billing-svc.md](logic-billing-svc.md)
- **Void iadesi credit note'ları yok sayıyor.** İade her zaman faturanın tamamı kadar isteniyor (`engine.go:456`). Void sırasında yeni refund credit note da kesilebiliyor. → [logic-billing-svc.md](logic-billing-svc.md)
- **Void edilen faturaya uygulanmış krediler kayboluyor.** Geri açılmıyorlar. → [logic-billing-svc.md](logic-billing-svc.md)
- **Prorasyon kredisi yüzünden fazla KDV kesiliyor.** Kredi `KindCredit` olarak yazıldığı için matrahtan düşülmüyor. → [logic-billing-svc.md](logic-billing-svc.md)
- **Re-rating muafiyetleri yok sayıyor.** Deneme, advance-fee ve provider-owned aboneliklere baz ücreti yeniden faturalıyor. → [logic-billing-svc.md](logic-billing-svc.md)
- **Sağlayıcı geçişinde ücret alınmıyor.** Halef her zaman arrears oluyor, yıllık abonelik bir yıl boyunca ücretsiz kalıyor (`providermigrations/command/command.go:60-70`). → [logic-plans-svc.md](logic-plans-svc.md)
- **Kuponlu plan değişikliğinde fazla kredi veriliyor.** Kupon hesaba katılmıyor (`changes/query/query.go:128-138`). → [logic-plans-svc.md](logic-plans-svc.md)
- **iyzico onayında sahte "paid" mümkün görünüyor.** Onay, imzasız gövdedeki conversation id ile faturayı buluyor ve dönen ödemeyle eşleştirmiyor. Boş `PaymentStatus` da başarı sayılıyor. MockPSP'de 404 dönen bir tahsilat başarılı kabul ediliyor. → [logic-pkg-web.md](logic-pkg-web.md)

**Veri doğruluğu**

- **Aggregator replay promote'u veri siliyor.** Replay aralığı dışındaki dönemlerin kullanımı siliniyor ve bu verification raporunda görünmüyor (`replay.go:620-636`). → [logic-event-services.md](logic-event-services.md)
- **Ingest dedup penceresi çok kısa.** Pencere yaklaşık 28 gün, izin verilen gecikme ise 35 gün. Geç gelen retry kullanımı iki kez sayıyor. → [logic-event-services.md](logic-event-services.md)
- **Konsolda 16 para birimi 100–1000 kat yanlış.** Kesir hanesi backend tablosundan değil Intl'den alınıyor. Tutarlar hem yanlış gösteriliyor hem yanlış kaydediliyor (HUF, IDR, COP, IQD…; `web/admin/src/shared/lib/money.ts:42`). → [logic-pkg-web.md](logic-pkg-web.md)
- **Hosted fatura sayfası "İşleniyor"da kalıyor.** Terk edilen checkout ya da açılan havale talimatı sayfayı kalıcı olarak kilitliyor ve müşteri ödeyemiyor. → [logic-gateway-payments.md](logic-gateway-payments.md)
- **`ledger_fact_once` indeksinde `tenant_id` yok.** Fatura numarası çakışan iki tenant'ın ledger satırı sessizce düşüyor. → [logic-gateway-payments.md](logic-gateway-payments.md)

**Eşzamanlılık ve dayanıklılık**

- **`workerx` ve invoice-worker kilit map'lerinde yarış var.** Aynı komut paralel çalışabiliyor. → [pkg.md](pkg.md), [payments-invoice-workers.md](payments-invoice-workers.md)
- **`rabbitx` kapanışta uçuştaki handler'ları beklemiyor.** Gecikmesiz requeue da mesajları birkaç milisaniyede DLQ'ya atıyor. → [pkg.md](pkg.md), [logic-pkg-web.md](logic-pkg-web.md)
- **Bozuk mesaj worker'ı çökertiyor.** `uuid.MustParse` panic'e yol açıyor (`payments-worker/internal/einvoice/handler.go:169`). → [payments-invoice-workers.md](payments-invoice-workers.md)
- **Parola kilidi paralel istekle atlatılabiliyor.** Kilitlenmeden önce yüzlerce tahmin yapılabiliyor. → [logic-plans-svc.md](logic-plans-svc.md)

## Genel temalar

- **ADR 0020 büyük ölçüde kağıt üzerinde.**
  - Gerçek modül yapısı yalnızca plans-svc'de var, orada da `subscriptions` tablosuna üç modül yazıyor. billing-svc'de kuralların çoğu modül dışındaki `runs` ve `saga` paketlerinde; `billingruns`, `rerating` ve `creditnotes` modülleri bunların ince sarmalayıcıları.
  - Diğer servislerde modül yapısı yok. Gateway ise iş kurallarını hesaplayan bir BFF.
  - İstemciye giden yüzlerce hata error key taşımıyor.
  - Gateway'in verdiği `common.*` anahtarlarının hiçbiri `docs/error-keys.md` içinde değil.
  - Ayrıntı: [cqrs-plans-billing.md](cqrs-plans-billing.md), [cqrs-other-services.md](cqrs-other-services.md).
- **Bilerek yapılmış kopyalar ayrışmış.** aggregator ile limits-svc'nin "kasıtlı ayna" olan usage-event kuralları artık farklı (`event_time` kontrolü). analytics-svc üçüncü ve daha gevşek bir sürüm kullanıyor. Aynı durum abonelik halefi kuralında da var: üç kopya, farklı kupon davranışı. → [cross-service.md](cross-service.md)
- **Ortak altyapı eksik.** Bootstrap, DB/pool kurulumu, tenant ve meter önbellekleri, UUID→InvalidArgument dönüşümü, sayfalama, dönem regex'i, para biçimlendirme ve test harness'leri her serviste baştan yazılmış.
- **Hatalar sessizce yutuluyor.** `_ =` ile atılan hatalar, loglanmadan dönen "internal error"lar ve hata dalında `false` dönen yardımcılar çok yaygın. Yukarıdaki doğruluk sorunlarının bir kısmı da buradan kaynaklanıyor.
- **Sihirli değerler ve çift varsayılanlar.** Domain durumları, ortam adları ve TTL'ler string ya da sayı literali olarak dağılmış. Varsayılanlar hem YAML'da hem Go'da tanımlı.
- **Uzun fonksiyonlar ve god struct'lar.** Örnekler: `buildDashboard` (335 satır), `runs/rate.go` (892 satır), `webhook.Server` (818 satır, 28'den fazla route), `TestCorrectness` (316 satır), `DashboardView.vue` (342 satır).

## Önerilen sıra

1. **Doğruluk hataları:** "Önce düzeltilmesi gerekenler" listesi. Her madde kendi raporunda bir senaryo ve öneri içeriyor.
2. **Hata sözleşmesi:** `errkey.Default` anahtarlarını listeye ekleyin; `convert.Err`, `Violations`, `invalid()` ve payments-worker `fail()` çağrılarına key verin. Bu bir kez yapıldığında konsol ve SDK hataları çevirebilir hâle gelir.
3. **Ortak paketler:** `pkg/usage` (event kuralları), `pkg/cachex` (tenant ve meter önbellekleri), `pkg/app` (bootstrap), `period.Valid`, `money.FormatMinor`.
4. **ADR 0020 taşımaları:**
   - plans-svc: `subscriptions` yazımlarını tek modülde toplayın; `admin` ve `webhooks` paketlerini modüle çevirin.
   - billing-svc: `runs` ve `saga` paketlerini modüllere taşıyın.
   - Diğer servisler: tek "service" paketlerini (assets, ingest, payments-worker `webhook`) modüllere bölün.
   - Gateway: dashboard ve overview hesaplarını sahibi olan servislerin `query` modüllerine taşıyın.
5. **Temizlik:** Ölü kod ve kullanılmayan export'ları silin. Dockerfile'ları tek dosyada birleştirin. Test harness'lerini `pkg/testkit` altında toplayın. Uzun fonksiyonları ve bileşenleri bölün.
