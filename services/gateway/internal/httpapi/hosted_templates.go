package httpapi

import "html/template"

// The templates of the payment pages. Everything is printed through html/template (escaped by context); there is no script and no external resource.

const pageCSS = hostedCSS + `
.top{display:flex;align-items:center;gap:12px;margin-bottom:8px}.logo{max-height:40px;max-width:160px;object-fit:contain}
a:not(.btn){color:#1d5fd1;text-underline-offset:3px}a:not(.btn):hover{color:#0f3f95}
.banner{background:#fff4d6;color:#6b4e00;border-radius:8px;padding:6px 12px;font-size:.85rem;margin-bottom:12px}
.badge.open{background:#e8ebf0}.badge.processing{background:#dbe9fd;color:#0b3d91}.badge.failed{background:#fde2e1;color:#8a1c17}
.hint{margin:16px 0 0;padding:10px 12px;border-radius:8px;background:#eef1f5}
.pay{margin-top:24px;border-top:1px solid #eceef2;padding-top:16px}.pay h2{font-size:1.05rem;margin:0 0 12px}
.pay form{display:inline-block;margin:0 8px 8px 0}
button.btn,a.btn{display:inline-block;margin:0;padding:12px 18px;border:0;border-radius:8px;background:#14171c;color:#fff;text-decoration:none;font:inherit;cursor:pointer;min-height:44px}
a.btn.alt{background:transparent;color:inherit;border:1px solid #c9ced6;margin-top:20px;margin-inline-end:8px}
.big{font-size:2rem;font-weight:700;margin:8px 0}.wrap{overflow-x:auto}.foot{margin-top:16px;text-align:center}
dl.kv{display:grid;grid-template-columns:max-content 1fr;gap:6px 16px}dl.kv dt{color:#5b6472}dl.kv dd{margin:0;word-break:break-all}
code.ref{display:block;padding:10px 12px;margin:8px 0;border-radius:8px;background:#eef1f5;font-size:1.05rem;word-break:break-all}
h2.sec{font-size:1.1rem;margin:28px 0 8px}
@media (prefers-color-scheme:dark){a:not(.btn){color:#8bb8ef}a:not(.btn):hover{color:#c4dcfa}.banner{background:#3b3000;color:#ffe9a6}.hint,code.ref{background:#262c35}.badge.open{background:#262c35}button.btn,a.btn{background:#e8eaee;color:#14171c}a.btn.alt{background:transparent;color:inherit;border-color:#3a414c}.pay{border-color:#262c35}.badge.processing{background:#16315e;color:#cfe0ff}.badge.failed{background:#4a1b19;color:#ffd0cd}}
@media print{body{background:#fff;color:#000}.card{border:0;padding:0}.noprint,.foot{display:none}}
@media (max-width:480px){main{padding:12px 8px}.card{padding:16px;border-radius:8px}.big{font-size:1.6rem}}`

const pageHead = `{{define "head"}}<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow">{{if .Refresh}}<meta http-equiv="refresh" content="5">{{end}}
<title>{{.Title}}</title><style>` + pageCSS + `</style></head><body><main><div class="card">
{{if .Test}}<div class="banner">{{.L.test_mode}}</div>{{end}}{{if .Logo}}<div class="top"><img class="logo" src="{{.Logo}}" alt=""></div>{{end}}{{end}}
{{define "foot"}}</div><p class="muted foot">{{.L.powered}} {{.Brand}}</p></main></body></html>{{end}}`

const invoiceTmpl = `{{define "invoice"}}{{template "head" .}}
<h1>{{.L.invoice}} {{.Number}}</h1><div class="muted">{{.Seller}}</div>
<div class="meta">
<div><b>{{.L.from}}</b>{{.Seller}}{{range .SellerAddress}}<br><span class="muted">{{.}}</span>{{end}}</div><div><b>{{.L.to}}</b>{{.Customer}}{{range .CustomerAddress}}<br><span class="muted">{{.}}</span>{{end}}</div><div><b>{{.L.date}}</b>{{.Date}}</div>
<div><b>{{.L.status}}</b><span class="badge {{.State}}">{{.StateLabel}}</span></div></div>
<div class="wrap"><table><thead><tr><th>{{.L.description}}</th><th class="n">{{.L.amount}} ({{.Currency}})</th></tr></thead><tbody>
{{range .Lines}}<tr><td>{{.Description}}{{if .Period}}<div class="muted">{{.Period}}</div>{{end}}</td><td class="n{{if .Negative}} neg{{end}}">{{.Amount}}</td></tr>
{{end}}</tbody><tfoot><tr><td>{{.L.subtotal}}</td><td class="n">{{.Subtotal}}</td></tr><tr><td>{{.L.total}}</td><td class="n">{{.Total}} {{.Currency}}</td></tr></tfoot></table></div>
{{if .Hint}}<p class="hint">{{.Hint}}</p>{{end}}
{{with .Bank}}<div class="pay"><b>{{$.L.payBy}}</b><dl><dt>{{$.L.bank}}</dt><dd>{{.Name}}</dd><dt>{{$.L.holder}}</dt><dd>{{.Holder}}</dd><dt>{{$.L.iban}}</dt><dd class="mono">{{.IBAN}}</dd>{{if .BIC}}<dt>{{$.L.bic}}</dt><dd class="mono">{{.BIC}}</dd>{{end}}<dt>{{$.L.bankRef}}</dt><dd class="mono">{{.Reference}}</dd></dl><p class="muted">{{$.L.referenceHint}}</p></div>{{end}}
{{if .Payable}}<div class="pay"><h2>{{.L.pay_title}}</h2>
{{range .Methods}}<form method="post" action="{{$.Pay}}"><input type="hidden" name="method" value="{{.ID}}"><button class="btn" type="submit">{{.Label}}</button></form>{{end}}
{{if .NoMethod}}<p class="muted">{{.L.pay_none}}</p>{{end}}</div>{{end}}
<div class="noprint">{{if .HasPDF}}<a class="btn alt" href="{{.PDF}}">{{.L.pdf}}</a>{{end}}{{if .Receipt}}<a class="btn alt" href="{{.Receipt}}">{{.L.receipt_view}}</a>{{end}}{{if .Back}}<a class="btn alt" href="{{.Back}}">{{.L.back}}</a>{{end}}</div>
{{template "foot" .}}{{end}}`

const adhocTmpl = `{{define "adhoc"}}{{template "head" .}}
<div class="muted">{{.Seller}}</div><h1>{{.Description}}</h1>
{{with .Pack}}<div class="muted">{{$.L.cr_you_get}}</div><div class="big">{{.Credits}} {{$.L.cr_unit}}</div><p class="muted">{{.Valid}}{{if gt .Quantity 1}} · {{$.L.cr_qty}} {{.Quantity}}{{end}}</p>{{end}}
{{with .Quote}}{{if .Bonus}}<p class="promo-line"><b>+{{.Bonus}} {{$.L.promo_bonus}}</b></p>{{end}}{{end}}
{{if .Quote}}{{if .Quote.HasDiscount}}<div class="muted">{{.L.promo_before}}</div><div class="was">{{.Amount}} {{.Currency}}</div><div class="muted">{{.L.promo_discount}}</div><div class="neg">-{{.Quote.Discount}} {{.Currency}}</div><div class="muted">{{.L.promo_pay}}</div><div class="big">{{.Quote.Total}} {{.Currency}}</div>{{else}}<div class="muted">{{.L.adhoc_amount}}</div><div class="big">{{.Amount}} {{.Currency}}</div>{{end}}{{else}}<div class="muted">{{.L.adhoc_amount}}</div><div class="big">{{.Amount}} {{.Currency}}</div>{{end}}
{{if .Consumed}}<p class="hint">{{.L.adhoc_used}}</p>{{else}}
<form method="post" action="{{.PromoAction}}" class="promo"><label for="code">{{.L.promo_label}}</label><div class="promo-row"><input id="code" name="code" value="{{.Code}}" maxlength="40" autocomplete="off" autocapitalize="characters" spellcheck="false"><button class="btn alt" type="submit">{{.L.promo_apply}}</button></div></form>
{{if .PromoError}}<p class="err" role="alert">{{.PromoError}}</p>{{end}}
<form method="post" action="{{.Start}}">{{if .Quote}}<input type="hidden" name="code" value="{{.Code}}">{{end}}<button class="btn" type="submit">{{.L.adhoc_continue}}</button></form>{{end}}
{{template "foot" .}}{{end}}`

const receiptTmpl = `{{define "receipt"}}{{template "head" .}}
<h1>{{.L.receipt}} · {{.Number}}</h1><div class="muted">{{.Seller}}</div>
<div class="meta"><div><b>{{.L.from}}</b>{{.Seller}}{{range .SellerAddress}}<br><span class="muted">{{.}}</span>{{end}}</div><div><b>{{.L.to}}</b>{{.Customer}}{{range .CustomerAddress}}<br><span class="muted">{{.}}</span>{{end}}</div>
<div><b>{{.L.paid_on}}</b>{{if .PaidOn}}{{.PaidOn}}{{else}}{{.Date}}{{end}}</div>{{if .PaidWith}}<div><b>{{.L.method}}</b>{{.PaidWith}}</div>{{end}}</div>
<div class="wrap"><table><thead><tr><th>{{.L.description}}</th><th class="n">{{.L.amount}} ({{.Currency}})</th></tr></thead><tbody>
{{range .Lines}}<tr><td>{{.Description}}</td><td class="n{{if .Negative}} neg{{end}}">{{.Amount}}</td></tr>
{{end}}</tbody><tfoot><tr><td>{{.L.total}}</td><td class="n">{{.Total}} {{.Currency}}</td></tr></tfoot></table></div>
<p><span class="badge paid">{{.L.st_paid}}</span></p>
{{template "foot" .}}{{end}}`

const instructionsTmpl = `{{define "instructions"}}{{template "head" .}}
<h1>{{.L.instr_title}}</h1><div class="muted">{{.L.invoice}} {{.Number}} · {{.Seller}}</div>
<div class="big">{{.Total}} {{.Currency}}</div>
{{if .Reference}}<p>{{.L.instr_hint}}</p><code class="ref">{{.Reference}}</code>{{end}}
{{if .Lines}}<dl class="kv">{{range .Lines}}<dt>{{.Label}}</dt><dd>{{.Value}}</dd>{{end}}</dl>{{end}}
<a class="btn alt" href="{{.Back}}">{{.L.instr_back}}</a>
{{template "foot" .}}{{end}}`

const messageTmpl = `{{define "message"}}{{template "head" .}}
<h1>{{.Heading}}</h1><p class="hint">{{.Text}}</p>{{if .Back}}<a class="btn alt" href="{{.Back}}">{{.L.instr_back}}</a>{{end}}
{{template "foot" .}}{{end}}`

const portalTmpl = `{{define "portal"}}{{template "head" .}}
<div class="muted">{{.Seller}}</div><h1>{{.L.portal_title}}</h1><div class="muted">{{.Customer}}</div>
<h2 class="sec">{{.L.portal_invoices}}</h2>
{{if .Invoices}}<div class="wrap"><table><thead><tr><th>{{.L.number}}</th><th>{{.L.date}}</th><th>{{.L.status}}</th><th class="n">{{.L.total}}</th><th></th></tr></thead><tbody>
{{range .Invoices}}<tr><td>{{.Number}}</td><td>{{.Date}}</td><td><span class="badge {{.State}}">{{.StateLabel}}</span></td><td class="n">{{.Total}} {{.Currency}}</td><td><a href="{{.Href}}">{{if .Payable}}{{$.L.pay_title}}{{else}}{{$.L.view}}{{end}}</a></td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.portal_none_inv}}</p>{{end}}
<h2 class="sec">{{.L.portal_payments}}</h2>
{{if .Payments}}<div class="wrap"><table><thead><tr><th>{{.L.date}}</th><th>{{.L.type}}</th><th>{{.L.number}}</th><th>{{.L.status}}</th><th>{{.L.provider}}</th><th class="n">{{.L.amount}}</th></tr></thead><tbody>
{{range .Payments}}<tr><td>{{.Date}}</td><td>{{.Kind}}</td><td>{{.Number}}</td><td>{{.Status}}</td><td>{{.Provider}}</td><td class="n{{if .Negative}} neg{{end}}">{{.Amount}} {{.Currency}}</td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.portal_none_pay}}</p>{{end}}
{{if .ShowCredits}}<h2 class="sec">{{.L.cr_title}}</h2>
<p><span class="muted">{{.L.cr_balance}}</span> <span class="big">{{.Balance}}</span> {{.L.cr_unit}}</p>
{{if .Lots}}<div class="wrap"><table><thead><tr><th class="n">{{.L.cr_left}}</th><th>{{.L.cr_expires}}</th></tr></thead><tbody>
{{range .Lots}}<tr><td class="n">{{.Remaining}} / {{.Granted}}</td><td>{{if .Expires}}{{.Expires}}{{else}}{{$.L.cr_never}}{{end}}</td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.cr_none}}</p>{{end}}
{{if .Packs}}<h2 class="sec">{{.L.cr_buy_title}}</h2>
<div class="wrap"><table><thead><tr><th>{{.L.cr_pack}}</th><th class="n">{{.L.cr_unit}}</th><th class="n">{{.L.cr_price}}</th><th></th></tr></thead><tbody>
{{range .Packs}}<tr><td>{{.Name}}<div class="muted">{{.Valid}}</div></td><td class="n">{{.Credits}}</td><td class="n">{{.Price}} {{.Currency}}</td>
<td>{{if $.CanBuy}}<form method="post" action="{{$.Buy}}"><input type="hidden" name="pack_id" value="{{.ID}}"><input class="code" name="code" maxlength="40" autocomplete="off" placeholder="{{$.L.promo_label}}" aria-label="{{$.L.promo_label}}"><button class="btn" type="submit">{{$.L.cr_buy}}</button></form>{{end}}</td></tr>
{{end}}</tbody></table></div>{{end}}{{end}}
{{template "foot" .}}{{end}}`

var pageTemplates = template.Must(template.New("pages").Parse(pageHead + invoiceTmpl + adhocTmpl + receiptTmpl + instructionsTmpl + messageTmpl + portalTmpl))
