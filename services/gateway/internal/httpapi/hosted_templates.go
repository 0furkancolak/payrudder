package httpapi

import "html/template"

// The templates of the payment pages. Everything is printed through html/template (escaped by context); there is no script and no external resource.

const pageCSS = hostedCSS + `
.top{display:flex;align-items:center;gap:12px;margin-bottom:12px}.logo{max-height:40px;max-width:160px;object-fit:contain}
a:not(.btn){color:var(--accent-foreground);text-underline-offset:3px}a:not(.btn):hover{text-decoration-thickness:2px}
.banner{background:var(--warning-soft);color:var(--warning);border-radius:calc(var(--radius) - 6px);padding:6px 12px;font-size:.85rem;font-weight:500;margin-bottom:16px}
.badge.open{background:var(--secondary)}.badge.processing{background:var(--info-soft);color:var(--info)}.badge.failed{background:var(--destructive-soft);color:var(--destructive)}
.badge.active{background:var(--success-soft);color:var(--success)}.badge.trialing{background:var(--info-soft);color:var(--info)}.badge.past_due{background:var(--destructive-soft);color:var(--destructive)}.badge.paused{background:var(--secondary)}
.hint{margin:16px 0 0;padding:10px 12px;border-radius:calc(var(--radius) - 6px);background:var(--muted)}
.pay{margin-top:24px}.pay h2{font-size:1.05rem;margin:0 0 12px}
.pay form{display:inline-block;margin-block:0 8px;margin-inline:0 8px}
button.btn,a.btn{display:inline-flex;align-items:center;justify-content:center;margin:0;padding:0 18px;border:0;border-radius:calc(var(--radius) - 6px);background:var(--primary);color:var(--primary-foreground);text-decoration:none;font:inherit;font-weight:500;cursor:pointer;min-height:44px}
button.btn:hover,a.btn:hover{filter:brightness(1.08)}
a.btn.alt,button.btn.alt{background:transparent;color:inherit;border:1px solid var(--input);margin-top:20px;margin-inline-end:8px}
.promo-row .btn.alt{margin:0}
.big{font-size:2rem;font-weight:700;letter-spacing:-.02em;margin:8px 0;font-variant-numeric:tabular-nums}.wrap{position:relative;overflow-x:auto}.foot{margin-top:20px;text-align:center}
dl.kv{display:grid;grid-template-columns:minmax(0,max-content) minmax(0,1fr);gap:6px 16px}dl.kv dt{color:var(--muted-foreground);overflow-wrap:anywhere}dl.kv dd{margin:0;word-break:break-all}@media(max-width:480px){dl.kv{grid-template-columns:minmax(0,1fr)}dl.kv dd{margin-bottom:6px}}
code.ref{display:block;padding:10px 12px;margin:8px 0;border-radius:calc(var(--radius) - 6px);background:var(--muted);font-size:1.05rem;word-break:break-all}
h2.sec{font-size:1.05rem;margin:32px 0 10px;padding-top:24px;border-top:1px solid var(--border)}
.nw td{white-space:nowrap}.sr{position:absolute;width:1px;height:1px;margin:-1px;padding:0;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap;border:0}.seller{font-weight:600;font-size:.95rem;margin-bottom:2px}.stat{display:flex;align-items:baseline;gap:8px;padding:16px;border-radius:calc(var(--radius) - 4px);background:var(--muted)}.stat .big{margin:0}
.sub{padding:18px 20px;border-radius:16px;background:var(--muted);margin:0 0 12px}.subhead{display:flex;flex-wrap:wrap;align-items:center;gap:8px 12px}.subhead .name{font-size:1.1rem;font-weight:650}
.subprice{margin-top:4px;font-size:1.35rem;font-weight:700;letter-spacing:-.01em;font-variant-numeric:tabular-nums}.subfacts{margin:10px 0 0;padding:0;list-style:none;color:var(--muted-foreground);font-size:.9rem}.subfacts li+li{margin-top:2px}
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
.pane{width:100%;min-width:0;max-width:700px;margin:0 auto;padding:56px 48px 48px}
.pane .card{background:none;border:0;border-radius:0;box-shadow:none;padding:0}
.pane h1{font-size:1.75rem;letter-spacing:-.02em;margin:0 0 6px}
h2.sec{border-top:0;padding-top:0;margin:44px 0 12px;font-size:.78rem;color:var(--muted-foreground);font-weight:650;text-transform:uppercase;letter-spacing:.07em}
.pane table{border-collapse:separate;border-spacing:0}.pane td{padding-block:14px;padding-inline:0 8px;border-bottom:1px solid var(--border)}.pane th{padding-block:8px;padding-inline:0 8px}
.pane .wrap{border-top:1px solid var(--border)}.pane thead th{border-bottom:1px solid var(--border)}
.pane .stat{background:var(--muted);border-radius:16px;padding:20px 22px}.pane .pack{border-radius:16px;padding:18px 20px;transition:border-color .15s,box-shadow .15s}.pane .pack:hover{border-color:var(--ring);box-shadow:var(--shadow)}
.pane .pay{border:0;padding:0;margin-top:28px}.pane .pay h2{font-size:.78rem;font-weight:650;text-transform:uppercase;letter-spacing:.07em;color:var(--muted-foreground);margin:0 0 12px}
.pane .pay form{display:block;margin:0 0 10px}.pane .pay button.btn{width:100%;min-height:52px;font-size:1rem;border-radius:14px;box-shadow:0 1px 2px rgb(16 24 40/.12),0 6px 16px -8px var(--primary)}
.pane button.btn,.pane a.btn{border-radius:12px;min-height:46px}
.pane .promo-row input,.pane input.code{height:48px;border-radius:12px}
.pane .meta{padding:18px 20px;border-radius:16px;background:var(--muted);margin:22px 0}
.pane .hint{border-radius:14px}
.pane .foot{display:none}
.agree{display:block}.agree h3{font-size:.78rem;font-weight:650;text-transform:uppercase;letter-spacing:.07em;color:var(--muted-foreground);margin:0 0 10px}
details.contract{border:1px solid var(--border);border-radius:14px;padding:6px 16px;margin:10px 0}details.contract summary{cursor:pointer;font-weight:600;padding:8px 0}
.contract .text{max-height:300px;overflow:auto;padding:4px 0 10px;font-size:.92rem}.contract .text h3,.contract .text h4,.contract .text h5{font-size:1rem;text-transform:none;letter-spacing:0;color:inherit;margin:14px 0 6px}
.contract .text ul,.contract .text ol{padding-inline-start:1.3rem;margin:6px 0}
.fld{display:block;width:100%;margin:6px 0 16px;padding:10px 12px;border:1px solid var(--input);border-radius:12px;background:var(--card);color:inherit;font:inherit}.fld:focus-visible{outline:2px solid var(--ring);outline-offset:2px}
label.lbl{display:block;font-size:.78rem;font-weight:650;text-transform:uppercase;letter-spacing:.07em;color:var(--muted-foreground)}.pane form.wd button.btn{margin-inline-end:8px}
.check{display:flex;gap:10px;align-items:flex-start;margin:12px 0;cursor:pointer}.check input{margin-top:.3rem;flex:none;width:18px;height:18px}
.agree fieldset{border:0;padding:0;margin:14px 0}.agree legend{font-size:.78rem;font-weight:650;text-transform:uppercase;letter-spacing:.07em;color:var(--muted-foreground);margin-bottom:4px}
@media (max-width:860px){.shell{grid-template-columns:minmax(0,1fr)}.side{padding:22px 20px;gap:12px}.side .sidetitle{display:none}.side .sideamt{font-size:1.8rem;margin-top:6px}.side .sidefoot{display:none}.side .sidemid{display:flex;flex-direction:column}.pane{max-width:none;padding:28px 20px 40px}}
/* Arabic letters join: any tracking (the small-caps labels, the tight headings) breaks the words apart. */
html:lang(ar) *{letter-spacing:0!important}
html:lang(ar) body{font-size:16px;line-height:1.7}html:lang(ar) :is(h2.sec,th,.meta b,.pane .pay h2,.promo label,.agree h3,.agree legend,.badge,.muted,.subfacts){font-size:.9rem}
@media print{.side{display:none}.shell{display:block}.pane{padding:0;max-width:none}}
@media print{body{background:#fff;color:#000}.card{border:0;padding:0;box-shadow:none}.noprint,.foot{display:none}}
@media (max-width:480px){main{padding:12px 8px 24px}.card{padding:18px;border-radius:calc(var(--radius) - 4px)}.big{font-size:1.6rem}.pack form{flex-direction:column}.pack form input.code{flex:none;width:100%}.pack form .btn{width:100%}td,th{padding:10px 6px}}`

const pageHead = `{{define "head"}}<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow">
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
<div class="wrap" tabindex="0"><table><thead><tr><th>{{.L.description}}</th><th class="n">{{.L.amount}} ({{.Currency}})</th></tr></thead><tbody>
{{range .Lines}}<tr><td>{{.Description}}{{if .Period}}<div class="muted">{{.Period}}</div>{{end}}</td><td class="n{{if .Negative}} neg{{end}}">{{.Amount}}</td></tr>
{{end}}</tbody><tfoot><tr><td>{{.L.subtotal}}</td><td class="n">{{.Subtotal}}</td></tr><tr><td>{{.L.total}}</td><td class="n">{{.Total}} {{.Currency}}</td></tr></tfoot></table></div>
{{if .Hint}}<p class="hint">{{.Hint}}</p>{{end}}{{if .CheckAgain}}<a class="btn alt" id="check-again" href="{{.Again}}">{{.L.check_again}}</a>{{end}}
{{with .Bank}}<div class="pay"><b>{{$.L.payBy}}</b><dl><dt>{{$.L.bank}}</dt><dd>{{.Name}}</dd><dt>{{$.L.holder}}</dt><dd>{{.Holder}}</dd><dt>{{$.L.iban}}</dt><dd class="mono">{{.IBAN}}</dd>{{if .BIC}}<dt>{{$.L.bic}}</dt><dd class="mono">{{.BIC}}</dd>{{end}}<dt>{{$.L.bankRef}}</dt><dd class="mono">{{.Reference}}</dd></dl><p class="muted">{{$.L.referenceHint}}</p></div>{{end}}
{{if .Payable}}<div class="pay"><h2>{{.L.pay_title}}</h2>
{{if and .Contracts .Methods}}{{template "agree" .}}{{else}}{{range .Methods}}<form method="post" action="{{$.Pay}}"><input type="hidden" name="method" value="{{.ID}}">{{template "installments" .}}<button class="btn" type="submit">{{.Label}}</button></form>{{end}}{{end}}
{{if .NoMethod}}<p class="muted">{{.L.pay_none}}</p>{{end}}</div>{{end}}
<div class="noprint">{{if .HasPDF}}<a class="btn alt" href="{{.PDF}}">{{.L.pdf}}</a>{{end}}{{if .Receipt}}<a class="btn alt" href="{{.Receipt}}">{{.L.receipt_view}}</a>{{end}}{{if .Back}}<a class="btn alt" href="{{.Back}}">{{.L.back}}</a>{{end}}{{if .Withdraw}}<a class="btn alt" href="{{.Withdraw}}">{{.L.withdraw_link}}</a>{{end}}</div>
{{template "foot" .}}{{end}}`

// The agreements form: each text the customer has not accepted yet (the page needs no script: the browser keeps the form from being sent with a box empty, and the server
// checks again), the two consents of a consumer, the payment method and the button that says what pressing it means.
// The instalments of a saved card: a plain radio group in the pay form (the page runs no script), the single payment chosen. Only the stored-card method has one; the server
// ignores the field for any other method.
const installmentsTmpl = `{{define "installments"}}{{if .Installments}}<fieldset class="inst" data-installments><legend>{{.InstLegend}}</legend>{{range $i, $o := .Installments}}<label class="check"><input type="radio" name="installments" value="{{$o.Count}}"{{if eq $i 0}} checked{{end}}><span>{{$o.Label}}</span></label>{{end}}<p class="muted">{{.InstHint}}</p></fieldset>{{end}}{{end}}`

const agreeTmpl = `{{define "agree"}}<form method="post" action="{{.Pay}}" class="agree">
<h3>{{.L.contract_title}}</h3>
{{if .Contracts.Error}}<p class="err" role="alert">{{.Contracts.Error}}</p>{{end}}
{{range .Contracts.Items}}<details class="contract"{{if not .Accepted}} open{{end}}><summary>{{.Label}} · {{$.L.contract_version}} {{.Number}}</summary><div class="text">{{.HTML}}</div></details>
{{if .Accepted}}<p class="muted">{{$.L.contract_accepted_on}} {{.AcceptedOn}}</p>{{else}}<input type="hidden" name="v_{{.Kind}}" value="{{.VersionID}}"><label class="check"><input type="checkbox" name="accept_{{.Kind}}" value="1" required><span>{{.CheckboxText}}</span></label>{{end}}
{{end}}{{if .Contracts.Consents}}<label class="check"><input type="checkbox" name="consent_start" value="1" required><span>{{.L.consent_start}}</span></label>
<label class="check"><input type="checkbox" name="consent_waiver" value="1" required><span>{{.L.consent_waiver}}</span></label>{{end}}
{{if gt (len .Methods) 1}}<fieldset><legend>{{.L.pay_method}}</legend>{{range $i, $m := .Methods}}<label class="check"><input type="radio" name="method" value="{{$m.ID}}"{{if eq $i 0}} checked{{end}}><span>{{$m.Label}}</span></label>{{end}}</fieldset>{{else}}{{range .Methods}}<input type="hidden" name="method" value="{{.ID}}">{{end}}{{end}}
{{range .Methods}}{{template "installments" .}}{{end}}
<p class="muted">{{.L.contract_pay_note}}</p>
<button class="btn" type="submit">{{.L.contract_pay}}</button></form>{{end}}`

// The agreements the customer is asked to accept again on the portal (a material version was published since they accepted): no payment happens here, so the button says
// only that they accept.
const portalAgreeTmpl = `{{define "portalAgree"}}<form method="post" action="{{.AgreeAction}}" class="agree">
<p class="hint">{{.L.agreements_changed}}</p>
{{if .Ask.Error}}<p class="err" role="alert">{{.Ask.Error}}</p>{{end}}
{{range .Ask.Items}}<details class="contract"{{if not .Accepted}} open{{end}}><summary>{{.Label}} · {{$.L.contract_version}} {{.Number}}</summary><div class="text">{{.HTML}}</div></details>
{{if .Accepted}}<p class="muted">{{$.L.contract_accepted_on}} {{.AcceptedOn}}</p>{{else}}<input type="hidden" name="v_{{.Kind}}" value="{{.VersionID}}"><label class="check"><input type="checkbox" name="accept_{{.Kind}}" value="1" required><span>{{.CheckboxText}}</span></label>{{end}}
{{end}}{{if .Ask.Consents}}<label class="check"><input type="checkbox" name="consent_start" value="1" required><span>{{.L.consent_start}}</span></label>
<label class="check"><input type="checkbox" name="consent_waiver" value="1" required><span>{{.L.consent_waiver}}</span></label>{{end}}
<button class="btn" type="submit">{{.L.agreements_accept}}</button></form>{{end}}`

// The withdrawal function (Directive 2011/83/EU art. 11a): the statement, then the page that shows what will be sent with the confirmation button.
const withdrawTmpl = `{{define "withdraw"}}{{template "head" .}}
<h1>{{if .Review}}{{.L.withdraw_review}}{{else}}{{.L.withdraw_title}}{{end}}</h1>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
{{if .Review}}<dl class="kv"><dt>{{.L.withdraw_name}}</dt><dd>{{.Name}}</dd><dt>{{.L.withdraw_contract}}</dt><dd>{{.ContractLabel}}</dd>{{if .Words}}<dt>{{.L.withdraw_words}}</dt><dd>{{.Words}}</dd>{{end}}</dl>
<p class="muted">{{.Email}}</p>
<form method="post" action="{{.Action}}" class="wd"><input type="hidden" name="name" value="{{.Name}}"><input type="hidden" name="contract" value="{{.Contract}}"><input type="hidden" name="words" value="{{.Words}}">
<button class="btn" type="submit">{{.L.withdraw_confirm}}</button><a class="btn alt" href="{{.Back}}">{{.L.withdraw_change}}</a></form>
{{else}}<p>{{.L.withdraw_intro}}</p>
<form method="post" action="{{.Action}}" class="wd">
<label class="lbl" for="w-name">{{.L.withdraw_name}}</label><input class="fld" id="w-name" name="name" value="{{.Name}}" required maxlength="200" autocomplete="name">
{{if gt (len .Invoices) 1}}<fieldset><legend>{{.L.withdraw_contract}}</legend>{{range .Invoices}}<label class="check"><input type="radio" name="contract" value="{{.ID}}"{{if eq .ID $.Contract}} checked{{end}}><span>{{.Label}}</span></label>{{end}}</fieldset>
{{else}}{{range .Invoices}}<p class="lbl">{{$.L.withdraw_contract}}</p><p>{{.Label}}</p><input type="hidden" name="contract" value="{{.ID}}">{{end}}{{end}}
<p class="muted">{{.Email}}</p>
<label class="lbl" for="w-words">{{.L.withdraw_words}}</label><textarea class="fld" id="w-words" name="words" maxlength="1000" rows="4">{{.Words}}</textarea>
<button class="btn" type="submit">{{.L.withdraw_continue}}</button><a class="btn alt" href="{{.Back}}">{{.L.instr_back}}</a></form>{{end}}
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
<div class="wrap" tabindex="0"><table><thead><tr><th>{{.L.description}}</th><th class="n">{{.L.amount}} ({{.Currency}})</th></tr></thead><tbody>
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
{{if .Ask}}<h2 class="sec">{{.L.agreements_title}}</h2>{{template "portalAgree" .}}{{end}}
{{if .Subscriptions}}<h2 class="sec">{{.L.sub_title}}</h2>
{{range .Subscriptions}}<div class="sub"><div class="subhead"><span class="name">{{.Plan}}</span><span class="badge {{.State}}">{{.StateLabel}}</span></div>
{{if .Price}}<div class="subprice">{{.Price}}</div>{{end}}{{if .Facts}}<ul class="subfacts">{{range .Facts}}<li>{{.}}</li>{{end}}</ul>{{end}}</div>
{{end}}<p class="muted">{{.L.sub_contact}}</p>{{end}}
<h2 class="sec">{{.L.portal_invoices}}</h2>
{{if .Invoices}}<div class="wrap" tabindex="0"><table class="nw"><thead><tr><th>{{.L.number}}</th><th>{{.L.date}}</th><th>{{.L.status}}</th><th class="n">{{.L.total}}</th><th><span class="sr">{{.L.action}}</span></th></tr></thead><tbody>
{{range .Invoices}}<tr><td>{{.Number}}</td><td>{{.Date}}</td><td><span class="badge {{.State}}">{{.StateLabel}}</span></td><td class="n">{{.Total}} {{.Currency}}</td><td><a href="{{.Href}}">{{if .Payable}}{{$.L.pay_title}}{{else}}{{$.L.view}}{{end}}</a></td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.portal_none_inv}}</p>{{end}}
<h2 class="sec">{{.L.portal_payments}}</h2>
{{if .Payments}}<div class="wrap" tabindex="0"><table class="nw"><thead><tr><th>{{.L.date}}</th><th>{{.L.type}}</th><th>{{.L.number}}</th><th>{{.L.status}}</th><th>{{.L.provider}}</th><th class="n">{{.L.amount}}</th></tr></thead><tbody>
{{range .Payments}}<tr><td>{{.Date}}</td><td>{{.Kind}}</td><td>{{.Number}}</td><td>{{.Status}}</td><td>{{.Provider}}</td><td class="n{{if .Negative}} neg{{end}}">{{.Amount}} {{.Currency}}</td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.portal_none_pay}}</p>{{end}}
{{if .Agreements}}<h2 class="sec">{{.L.agreements_list}}</h2>
<div class="wrap" tabindex="0"><table class="nw"><thead><tr><th>{{.L.contract_title}}</th><th>{{.L.contract_version}}</th><th>{{.L.contract_accepted_on}}</th><th><span class="sr">{{.L.action}}</span></th></tr></thead><tbody>
{{range .Agreements}}<tr><td>{{.Label}}</td><td>{{.Number}}</td><td>{{.Date}}</td><td><a href="{{.Href}}">{{$.L.agreements_view}}</a>{{if .PdfHref}} · <a href="{{.PdfHref}}">{{$.L.agreements_pdf}}</a>{{end}}</td></tr>
{{end}}</tbody></table></div>{{end}}
{{if .Withdraw}}<div class="noprint"><a class="btn alt" href="{{.Withdraw}}">{{.L.withdraw_link}}</a></div>{{end}}
{{if .ShowCredits}}<h2 class="sec">{{.L.cr_title}}</h2>
<div class="stat"><span class="muted">{{.L.cr_balance}}</span> <span class="big">{{.Balance}}</span> <span class="muted">{{.L.cr_unit}}</span></div>
{{if .Lots}}<div class="wrap" tabindex="0"><table class="nw"><thead><tr><th>{{.L.cr_left}}</th><th>{{.L.cr_expires}}</th></tr></thead><tbody>
{{range .Lots}}<tr><td>{{.Remaining}} / {{.Granted}}</td><td>{{if .Expires}}{{.Expires}}{{else}}{{$.L.cr_never}}{{end}}</td></tr>
{{end}}</tbody></table></div>{{else}}<p class="muted">{{.L.cr_none}}</p>{{end}}
{{if .Packs}}<h2 class="sec">{{.L.cr_buy_title}}</h2>
<ul class="packs">{{range .Packs}}<li class="pack"><div><div class="name">{{.Name}}</div><div class="muted">{{.Credits}} {{$.L.cr_unit}} · {{.Valid}}</div></div><div class="price">{{.Price}} {{.Currency}}</div>
{{if $.CanBuy}}<form method="post" action="{{$.Buy}}" autocomplete="off"><input type="hidden" name="pack_id" value="{{.ID}}"><input class="code" type="text" name="code" maxlength="40" autocomplete="off" autocapitalize="characters" autocorrect="off" spellcheck="false" enterkeyhint="done" data-1p-ignore data-lpignore="true" data-bwignore data-form-type="other" placeholder="{{$.L.promo_label}}" aria-label="{{$.L.promo_label}}"><button class="btn" type="submit">{{$.L.cr_buy}}</button></form>{{end}}</li>
{{end}}</ul>{{end}}{{end}}
{{template "foot" .}}{{end}}`

// parsePageTemplates parses the templates; the tests parse a second set to run it with stricter options than the one the handlers use.
func parsePageTemplates() *template.Template {
	return template.Must(template.New("pages").Parse(pageHead + invoiceTmpl + installmentsTmpl + agreeTmpl + portalAgreeTmpl + withdrawTmpl + adhocTmpl + receiptTmpl + instructionsTmpl + messageTmpl + portalTmpl + referralTmpl + privacyTmpl))
}

var pageTemplates = parsePageTemplates()
