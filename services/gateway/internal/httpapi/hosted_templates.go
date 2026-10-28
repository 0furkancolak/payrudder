package httpapi

import "html/template"

// The templates of the payment pages. Everything is printed through html/template (escaped by context); there is no script and no external resource.

const pageCSS = hostedCSS + `
.top{display:flex;align-items:center;gap:12px;margin-bottom:12px}.logo{max-height:40px;max-width:160px;object-fit:contain}
a:not(.btn){color:var(--accent-foreground);text-underline-offset:3px}a:not(.btn):hover{text-decoration-thickness:2px}
.banner{background:var(--warning-soft);color:var(--warning);border-radius:calc(var(--radius) - 6px);padding:6px 12px;font-size:.85rem;font-weight:500;margin-bottom:16px}
.badge.open{background:var(--secondary)}.badge.processing{background:var(--info-soft);color:var(--info)}.badge.failed{background:var(--destructive-soft);color:var(--destructive)}
.hint{margin:16px 0 0;padding:10px 12px;border-radius:calc(var(--radius) - 6px);background:var(--muted)}
.pay{margin-top:24px}.pay h2{font-size:1.05rem;margin:0 0 12px}
.pay form{display:inline-block;margin:0 8px 8px 0}
button.btn,a.btn{display:inline-flex;align-items:center;justify-content:center;margin:0;padding:0 18px;border:0;border-radius:calc(var(--radius) - 6px);background:var(--primary);color:var(--primary-foreground);text-decoration:none;font:inherit;font-weight:500;cursor:pointer;min-height:44px}
button.btn:hover,a.btn:hover{filter:brightness(1.08)}
a.btn.alt,button.btn.alt{background:transparent;color:inherit;border:1px solid var(--input);margin-top:20px;margin-inline-end:8px}
.promo-row .btn.alt{margin:0}
.big{font-size:2rem;font-weight:700;letter-spacing:-.02em;margin:8px 0;font-variant-numeric:tabular-nums}.wrap{overflow-x:auto}.foot{margin-top:20px;text-align:center}
dl.kv{display:grid;grid-template-columns:max-content 1fr;gap:6px 16px}dl.kv dt{color:var(--muted-foreground)}dl.kv dd{margin:0;word-break:break-all}
code.ref{display:block;padding:10px 12px;margin:8px 0;border-radius:calc(var(--radius) - 6px);background:var(--muted);font-size:1.05rem;word-break:break-all}
h2.sec{font-size:1.05rem;margin:32px 0 10px;padding-top:24px;border-top:1px solid var(--border)}
.nw td{white-space:nowrap}.seller{font-weight:600;font-size:.95rem;margin-bottom:2px}.stat{display:flex;align-items:baseline;gap:8px;padding:16px;border-radius:calc(var(--radius) - 4px);background:var(--muted)}.stat .big{margin:0}
.packs{display:grid;gap:12px;margin:0;padding:0;list-style:none}
.pack{display:grid;grid-template-columns:1fr auto;gap:12px 16px;align-items:center;padding:16px;border:1px solid var(--border);border-radius:calc(var(--radius) - 4px)}
.pack .price{font-weight:600;font-variant-numeric:tabular-nums;text-align:end}.pack .name{font-weight:600}
.pack form{grid-column:1/-1;display:flex;gap:8px;margin:0}.pack form input.code{max-width:none;margin:0}
/* The page is a split layout like the checkout and portal pages of Stripe and Paddle: the seller and what the page is about on a coloured panel, the work on a calm pane. */
.shell{min-height:100vh;display:grid;grid-template-columns:minmax(300px,38%) 1fr}
.side{position:relative;display:flex;flex-direction:column;justify-content:space-between;gap:32px;padding:48px 44px;color:#fff;background:linear-gradient(158deg,oklch(.46 .21 266),oklch(.3 .15 272))}
.side .brandrow{display:flex;align-items:center;gap:14px;min-height:44px}.side .logochip{display:inline-flex;align-items:center;background:#fff;border-radius:12px;padding:6px 10px}.side .logo{max-height:34px;max-width:150px}
.side .seller{font-size:1.15rem;font-weight:650;letter-spacing:-.01em;margin:0}
.side .sidetitle{font-size:2rem;line-height:1.15;font-weight:700;letter-spacing:-.025em;margin:0}
.side .sideamt{margin-top:14px;font-size:2.6rem;font-weight:700;letter-spacing:-.03em;font-variant-numeric:tabular-nums}
.side .sidenote{margin-top:6px;opacity:.78}
.side .sidefoot{font-size:.82rem;opacity:.7}
.pane{width:100%;max-width:700px;margin:0;padding:56px 48px 48px}
.pane .card{background:none;border:0;border-radius:0;box-shadow:none;padding:0}
.pane h1{font-size:1.75rem;letter-spacing:-.02em;margin:0 0 6px}
h2.sec{border-top:0;padding-top:0;margin:44px 0 12px;font-size:.78rem;color:var(--muted-foreground);font-weight:650;text-transform:uppercase;letter-spacing:.07em}
.pane table{border-collapse:separate;border-spacing:0}.pane td{padding:14px 8px 14px 0;border-bottom:1px solid var(--border)}.pane th{padding:8px 8px 8px 0}
.pane .wrap{border-top:1px solid var(--border)}.pane thead th{border-bottom:1px solid var(--border)}
.pane .stat{background:var(--muted);border-radius:16px;padding:20px 22px}.pane .pack{border-radius:16px;padding:18px 20px;transition:border-color .15s,box-shadow .15s}.pane .pack:hover{border-color:var(--ring);box-shadow:var(--shadow)}
.pane .pay{border:0;padding:0;margin-top:28px}.pane .pay h2{font-size:.78rem;font-weight:650;text-transform:uppercase;letter-spacing:.07em;color:var(--muted-foreground);margin:0 0 12px}
.pane .pay form{display:block;margin:0 0 10px}.pane .pay button.btn{width:100%;min-height:52px;font-size:1rem;border-radius:14px;box-shadow:0 1px 2px rgb(16 24 40/.12),0 6px 16px -8px var(--primary)}
.pane button.btn,.pane a.btn{border-radius:12px;min-height:46px}
.pane .promo-row input,.pane input.code{height:48px;border-radius:12px}
.pane .meta{padding:18px 20px;border-radius:16px;background:var(--muted);margin:22px 0}
.pane .hint{border-radius:14px}
.pane .foot{display:none}
@media (max-width:860px){.shell{grid-template-columns:1fr}.side{padding:22px 20px;gap:12px}.side .sidetitle{display:none}.side .sideamt{font-size:1.8rem;margin-top:6px}.side .sidefoot{display:none}.side .sidemid{display:flex;flex-direction:column}.pane{max-width:none;padding:28px 20px 40px}}
@media print{.side{display:none}.shell{display:block}.pane{padding:0;max-width:none}}
@media print{body{background:#fff;color:#000}.card{border:0;padding:0;box-shadow:none}.noprint,.foot{display:none}}
@media (max-width:480px){main{padding:12px 8px 24px}.card{padding:18px;border-radius:calc(var(--radius) - 4px)}.big{font-size:1.6rem}.pack form{flex-direction:column}.pack form input.code{flex:none;width:100%}.pack form .btn{width:100%}td,th{padding:10px 6px}}`

const pageHead = `{{define "head"}}<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow">{{if .Refresh}}<meta http-equiv="refresh" content="5">{{end}}
<title>{{.Title}}</title><style>` + pageCSS + `</style></head><body><div class="shell">
<aside class="side"><div class="brandrow">{{if .Logo}}<span class="logochip"><img class="logo" src="{{.Logo}}" alt=""></span>{{end}}{{if .SideSeller}}<span class="seller">{{.SideSeller}}</span>{{end}}</div>
<div class="sidemid"><p class="sidetitle" aria-hidden="true">{{.Title}}</p>{{if .SideAmount}}<div class="sideamt">{{.SideAmount}}</div>{{end}}{{if .SideNote}}<div class="sidenote">{{.SideNote}}</div>{{end}}</div>
<div class="sidefoot">{{.L.powered}} {{.Brand}}</div></aside>
<main class="pane"><div class="card">
{{if .Test}}<div class="banner">{{.L.test_mode}}</div>{{end}}{{end}}
{{define "foot"}}</div></main></div></body></html>{{end}}`

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
<form method="post" action="{{.PromoAction}}" class="promo" autocomplete="off"><label for="code">{{.L.promo_label}}</label><div class="promo-row"><input id="code" name="code" value="{{.Code}}" type="text" maxlength="40" autocomplete="off" autocapitalize="characters" autocorrect="off" spellcheck="false" enterkeyhint="done" data-1p-ignore data-lpignore="true" data-bwignore data-form-type="other"><button class="btn alt" type="submit">{{.L.promo_apply}}</button></div></form>
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

const referralTmpl = `{{define "referral"}}{{template "head" .}}
<h1>{{.L.ref_title}}</h1><p class="hint">{{.L.ref_text}}</p><a class="btn alt" href="{{.Privacy}}">{{.L.ref_privacy}}</a>
{{template "foot" .}}{{end}}`

const privacyTmpl = `{{define "privacy"}}{{template "head" .}}
<h1>{{.L.priv_title}}</h1><p>{{.L.priv_intro}}</p>
<h2 class="sec">{{.L.priv_h_cookie}}</h2><p>{{.L.priv_cookie}}</p>
<h2 class="sec">{{.L.priv_h_counts}}</h2><p>{{.L.priv_counts}}</p>
<h2 class="sec">{{.L.priv_h_control}}</h2><p>{{.L.priv_control}}</p>
{{template "foot" .}}{{end}}`

const portalTmpl = `{{define "portal"}}{{template "head" .}}
<h1>{{.L.portal_title}}</h1><div class="muted">{{.Customer}}</div>
<h2 class="sec">{{.L.portal_invoices}}</h2>
{{if .Invoices}}<div class="wrap"><table class="nw"><thead><tr><th>{{.L.number}}</th><th>{{.L.date}}</th><th>{{.L.status}}</th><th class="n">{{.L.total}}</th><th></th></tr></thead><tbody>
{{range .Invoices}}<tr><td>{{.Number}}</td><td>{{.Date}}</td><td><span class="badge {{.State}}">{{.StateLabel}}</span></td><td class="n">{{.Total}} {{.Currency}}</td><td><a href="{{.Href}}">{{if .Payable}}{{$.L.pay_title}}{{else}}{{$.L.view}}{{end}}</a></td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.portal_none_inv}}</p>{{end}}
<h2 class="sec">{{.L.portal_payments}}</h2>
{{if .Payments}}<div class="wrap"><table class="nw"><thead><tr><th>{{.L.date}}</th><th>{{.L.type}}</th><th>{{.L.number}}</th><th>{{.L.status}}</th><th>{{.L.provider}}</th><th class="n">{{.L.amount}}</th></tr></thead><tbody>
{{range .Payments}}<tr><td>{{.Date}}</td><td>{{.Kind}}</td><td>{{.Number}}</td><td>{{.Status}}</td><td>{{.Provider}}</td><td class="n{{if .Negative}} neg{{end}}">{{.Amount}} {{.Currency}}</td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.portal_none_pay}}</p>{{end}}
{{if .ShowCredits}}<h2 class="sec">{{.L.cr_title}}</h2>
<div class="stat"><span class="muted">{{.L.cr_balance}}</span> <span class="big">{{.Balance}}</span> <span class="muted">{{.L.cr_unit}}</span></div>
{{if .Lots}}<div class="wrap"><table class="nw"><thead><tr><th class="n">{{.L.cr_left}}</th><th>{{.L.cr_expires}}</th></tr></thead><tbody>
{{range .Lots}}<tr><td class="n">{{.Remaining}} / {{.Granted}}</td><td>{{if .Expires}}{{.Expires}}{{else}}{{$.L.cr_never}}{{end}}</td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.cr_none}}</p>{{end}}
{{if .Packs}}<h2 class="sec">{{.L.cr_buy_title}}</h2>
<ul class="packs">{{range .Packs}}<li class="pack"><div><div class="name">{{.Name}}</div><div class="muted">{{.Credits}} {{$.L.cr_unit}} · {{.Valid}}</div></div><div class="price">{{.Price}} {{.Currency}}</div>
{{if $.CanBuy}}<form method="post" action="{{$.Buy}}" autocomplete="off"><input type="hidden" name="pack_id" value="{{.ID}}"><input class="code" type="text" name="code" maxlength="40" autocomplete="off" autocapitalize="characters" autocorrect="off" spellcheck="false" enterkeyhint="done" data-1p-ignore data-lpignore="true" data-bwignore data-form-type="other" placeholder="{{$.L.promo_label}}" aria-label="{{$.L.promo_label}}"><button class="btn" type="submit">{{$.L.cr_buy}}</button></form>{{end}}</li>
{{end}}</ul>{{end}}{{end}}
{{template "foot" .}}{{end}}`

var pageTemplates = template.Must(template.New("pages").Parse(pageHead + invoiceTmpl + adhocTmpl + receiptTmpl + instructionsTmpl + messageTmpl + portalTmpl + referralTmpl + privacyTmpl))
