// Package webhook is the generic e-invoice adapter: it POSTs the document to a URL the tenant owns and lets that endpoint talk to whatever integrator
// it likes (Uyumsoft, Foriba, Paraşüt, a Peppol access point…). No Quotify code is needed to wire one.
//
// # Settings
//
//	{"url": "https://einvoice.example.com/quotify", "secret": "whsec_…"}
//
// The URL must be https and public (private addresses are refused). The secret signs every request; it is stored sealed and never returned by the API.
//
// # Requests
//
// Every request is a POST to the URL and carries the signature header of pkg/webhooksig, "Quotify-Signature: t=<unix>,v1=<hex>", where v1 is
// HMAC-SHA256(secret, "<t>.<body>") over the exact request body (see webhooksig.Verify). Reject a request whose signature does not verify or whose
// timestamp is more than five minutes off. The headers
//
//	Quotify-Action:         issue | status | cancel | pdf | ubl
//	Quotify-Document-Uuid:  the document's UUID, identical to the UBL's cbc:UUID; the idempotency key
//	Quotify-Document-Kind:  invoice | credit_note
//	Quotify-Document-Type:  e-fatura | e-arsiv | peppol
//	Quotify-Document-Number: Quotify's own number (INV-2026-000123)
//
// (the prefix is the deployment's product name; "Quotify" by default) are repeated as the "action" query parameter on the URL (?action=issue).
//
// # issue
//
// The body is the UBL 2.1 document (Content-Type: application/xml; UBL-TR 1.2 for e-fatura and e-arsiv, EN 16931 / Peppol BIS 3 for peppol), unsealed:
// sealing, sending and numbering are the endpoint's job. The endpoint must be idempotent on the UUID: a second request with the same UUID answers with the
// first one's result, it never creates a second legal document. Answer 200 with JSON:
//
//	{"state": "issued" | "pending" | "rejected",
//	 "legal_number": "ABC2026000000001",   // required when issued: the number the authority knows the document under, never reused
//	 "external_id": "…",                   // your id for the document; echoed back in later actions
//	 "pdf_base64": "…",                    // optional: a legal PDF
//	 "message": "…"}                       // optional, shown to the operator, put no secrets in it
//
// "pending" means accepted but not yet confirmed; Quotify asks again with action=status. "rejected" is final (the operator must fix the data and retry).
//
// # status, cancel, pdf, ubl
//
// The body is JSON, Content-Type: application/json:
//
//	{"action": "status", "uuid": "…", "external_id": "…", "type": "e-arsiv", "reason": "…"}   // reason only on cancel
//
// and the answers are
//
//	status: {"state": "issued|pending|rejected|cancelled", "legal_number": "…", "message": "…"}
//	cancel: {"state": "cancelled"}                      // 501 when cancelling is not legal for the document
//	pdf:    {"pdf_base64": "…"}
//	ubl:    {"ubl_base64": "…"}                         // the sealed document the authority holds
//
// # Status codes
//
// 2xx with a JSON body is an answer. 404 means the document is unknown, 501 that the action is not supported. Any other 4xx except 408 and 429 is a
// refusal that retrying will not change (Quotify marks the document failed and shows the "message" of the body, if any). 408, 429, 5xx, a timeout or a
// connection error are retried by Quotify with backoff (1 min, 5 min, 30 min, 2 h, 12 h), so the endpoint must tolerate repeats.
package webhook
