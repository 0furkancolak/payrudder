/**
 * The text of real documents as the PDF text layer or OCR gives it (company names, addresses and figures are made up, the layouts are real): what the parser is tested
 * against. Add a sample here whenever a document is read wrongly.
 */
export const FIXTURES = {
  /** A SaaS invoice, English, US layout (month-first slash date, dollars, tax line). */
  usSaas: `Acme Cloud, Inc.
548 Market St, San Francisco, CA 94104
Invoice
Invoice number INV-2026-10443
Invoice date 10/26/2026
Due date 11/25/2026
Bill to Drift Kings Ltd
Description Qty Unit price Amount
Pro plan (Oct 2026) 1 $49.00 $49.00
Overage - API calls 1 $69.96 $69.96
Subtotal $118.96
Sales tax (8.25%) $9.81
Total $128.77
Amount due $128.77 USD`,

  /** A Turkish e-Arşiv fatura: dotted date, comma decimals, KDV lines, TL. */
  trFatura: `Mavi Bulut Bilişim Hizmetleri A.Ş.
Maslak Mah. Büyükdere Cad. No:12 Sarıyer / İstanbul
Vergi Dairesi: Maslak  Vergi No: 1234567890
e-Arşiv Fatura
Fatura No: MBB2026000001234
Fatura Tarihi: 26.10.2026
Sayın Piksel Korsanları Ltd. Şti.
Mal / Hizmet Miktar Birim Fiyat Tutar
Sunucu barındırma (Ekim 2026) 1 Adet 1.250,00 TL 1.250,00 TL
Mal Hizmet Toplam Tutarı 1.250,00 TL
Toplam İskonto 0,00 TL
KDV Hariç Tutar 1.250,00 TL
Hesaplanan KDV (%20) 250,00 TL
Vergiler Dahil Toplam Tutar 1.500,00 TL
Ödenecek Tutar 1.500,00 TL`,

  /** A German Rechnung: euro with the symbol after the figure, dotted date, thousands dot. */
  deRechnung: `Nordlicht Software GmbH
Hafenstraße 4 · 20457 Hamburg
USt-IdNr.: DE123456789
Rechnung
Rechnungsnummer 2026-0815
Rechnungsdatum 21.10.2026
Zahlbar bis 04.11.2026
Lizenzen Jahresabo 12 Nutzer 1.200,00 €
Zwischensumme 1.200,00 €
zzgl. 19 % MwSt. 228,00 €
Gesamtbetrag 1.428,00 €`,

  /** A French facture: written month, spaces as thousands separators, TVA. */
  frFacture: `Atelier Lumière SAS
12 rue des Lilas, 75011 Paris
Facture
Date : 5 octobre 2026
Prestation de conception graphique 2 400,00 €
Total HT 2 400,00 €
TVA 20 % 480,00 €
Total TTC 2 880,00 €
Net à payer 2 880,00 €`,

  /** A Spanish factura with "de" in the date. */
  esFactura: `Servicios Digitales Sol S.L.
Calle Mayor 10, 28013 Madrid
Factura
Fecha: 3 de octubre de 2026
Mantenimiento web 300,00 EUR
Base imponible 300,00 EUR
IVA 21% 63,00 EUR
Total a pagar 363,00 EUR`,

  /** A till receipt that is a photo: noisy OCR output with no vendor suffix and a month-name date. */
  ocrReceipt: `BLUE BOTTLE COFFEE
Oct 24, 2026 08:41
1 Latte 5.50
1 Croissant 4.25
Subtotal 9.75
Tax 0.81
TOTAL $10.56
VISA ****1234`,

  /** An invoice whose figures are in a currency with no decimals. */
  jpInvoice: `Tokyo Hosting KK
Invoice
Date 2026-10-02
Server rental 12,800 JPY
Total 12,800 JPY`,
} as const;
