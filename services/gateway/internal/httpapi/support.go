package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The help desk. A signed-in person opens tickets for their account and answers on them (the tickets of every account they belong to); the operator works the
// whole queue: answers publicly or leaves a note the person never sees, and sets status, priority and assignee. Nothing the browser sends decides whose ticket it
// is: the person's id comes from the session, and plans-svc checks the account's membership.

func (s *Server) supportRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/support/tickets", AccessSession, ratelimit.ClassRead, s.supportMyList},
		{"POST", "/app/v1/support/tickets", AccessSession, ratelimit.ClassWrite, s.supportMyOpen},
		{"GET", "/app/v1/support/tickets/{id}", AccessSession, ratelimit.ClassRead, s.supportMyGet},
		{"POST", "/app/v1/support/tickets/{id}/replies", AccessSession, ratelimit.ClassWrite, s.supportMyReply},
		{"PUT", "/app/v1/support/tickets/{id}/status", AccessSession, ratelimit.ClassWrite, s.supportMyStatus},
		{"GET", "/app/v1/support/summary", AccessSession, ratelimit.ClassRead, s.supportMySummary},
		{"POST", "/app/v1/support/tickets/{id}/messages/{message_id}/attachments", AccessSession, ratelimit.ClassWrite, s.supportMyAttach},
		{"GET", "/app/v1/support/tickets/{id}/attachments/{attachment_id}", AccessSession, ratelimit.ClassRead, s.supportMyDownload},

		{"GET", "/admin/v1/support/tickets", AccessOperator, "", s.supportList},
		{"GET", "/admin/v1/support/tickets/{id}", AccessOperator, "", s.supportGet},
		{"PATCH", "/admin/v1/support/tickets/{id}", AccessOperator, "", s.supportUpdate},
		{"POST", "/admin/v1/support/tickets/{id}/replies", AccessOperator, "", s.supportReply},
		{"GET", "/admin/v1/support/summary", AccessOperator, "", s.supportSummary},
		{"GET", "/admin/v1/support/operators", AccessOperator, "", s.supportOperators},
		{"POST", "/admin/v1/support/tickets/{id}/messages/{message_id}/attachments", AccessOperator, "", s.supportAttach},
		{"GET", "/admin/v1/support/tickets/{id}/attachments/{attachment_id}", AccessOperator, "", s.supportDownload},
	}
}

func (s *Server) supportMyList(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Support.ListMySupportTickets(r.Context(), &plansv1.ListMySupportTicketsRequest{UserId: sess.UserID, Status: r.URL.Query().Get("status")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportMyOpen(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.OpenMySupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId = sess.UserID
	resp, err := s.d.Support.OpenMySupportTicket(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) supportMyGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Support.GetMySupportTicket(r.Context(), &plansv1.GetMySupportTicketRequest{UserId: sess.UserID, TicketId: r.PathValue("id")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportMyReply(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.ReplyMySupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.TicketId = sess.UserID, r.PathValue("id")
	resp, err := s.d.Support.ReplyMySupportTicket(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) supportMyStatus(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	req := &plansv1.SetMySupportTicketStatusRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId, req.TicketId = sess.UserID, r.PathValue("id")
	resp, err := s.d.Support.SetMySupportTicketStatus(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	breached, _ := strconv.ParseBool(q.Get("sla_breached"))
	resp, err := s.d.Support.ListSupportTickets(r.Context(), &plansv1.ListSupportTicketsRequest{Status: q.Get("status"), Priority: q.Get("priority"), Assignee: q.Get("assignee"), Search: q.Get("search"), SlaBreached: breached})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportGet(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.GetSupportTicket(r.Context(), &plansv1.GetSupportTicketRequest{TicketId: r.PathValue("id")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportUpdate(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateSupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	// The signed-in operator's address is who the ticket's history names (the machine token: "Payrudder support").
	req.TicketId, req.AuthorName = r.PathValue("id"), operatorFrom(r.Context())
	resp, err := s.d.Support.UpdateSupportTicket(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// supportReply answers as the signed-in operator (their address is the author shown to the person; the machine token answers as "Payrudder support").
func (s *Server) supportReply(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ReplySupportTicketRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TicketId, req.AuthorName = r.PathValue("id"), operatorFrom(r.Context())
	resp, err := s.d.Support.ReplySupportTicket(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

// supportAttachmentLimit is the body of an attachment upload: the file's 5 MiB (assets-svc's rule for the purpose) and the form's framing.
const supportAttachmentLimit = 5<<20 + formOverhead

func (s *Server) supportMySummary(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.GetMySupportSummary(r.Context(), &plansv1.GetMySupportSummaryRequest{UserId: sessionFrom(r.Context()).UserID})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) supportSummary(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Support.GetSupportSummary(r.Context(), &plansv1.GetSupportSummaryRequest{})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// supportOperators lists the addresses a ticket can be assigned to: the operators of the deployment (PAYRUDDER_OPERATOR_EMAILS).
func (s *Server) supportOperators(w http.ResponseWriter, _ *http.Request) {
	noStore(w)
	writeJSONValue(w, http.StatusOK, map[string]any{"operators": append([]string{}, s.d.OperatorEmails...)})
}

func (s *Server) supportMyAttach(w http.ResponseWriter, r *http.Request) {
	s.supportAttachTo(w, r, sessionFrom(r.Context()).UserID)
}

func (s *Server) supportAttach(w http.ResponseWriter, r *http.Request) { s.supportAttachTo(w, r, "") }

func (s *Server) supportMyDownload(w http.ResponseWriter, r *http.Request) {
	s.supportDownloadFor(w, r, sessionFrom(r.Context()).UserID)
}

func (s *Server) supportDownload(w http.ResponseWriter, r *http.Request) {
	s.supportDownloadFor(w, r, "")
}

// storedUnder is ctx as the tenant that holds the file in assets-svc: the help desk keeps a ticket's files under the first tenant of its account.
func storedUnder(ctx context.Context, t *plansv1.SupportAttachmentTarget) context.Context {
	id, _ := uuid.Parse(t.GetTenantId())
	return tenant.NewContext(ctx, tenant.Info{ID: id, Environment: t.GetEnvironment(), Timezone: t.GetTimezone(), Tier: t.GetTier()})
}

// supportAttachTo is the upload of one file (form field "file") to a message: the help desk says whether it may and where it goes, assets-svc stores it under
// that tenant with the purpose support_attachment (which checks the type, the size and the content), and the help desk records it. A file whose record fails is
// deleted again. user is the person's id, empty for the operator.
func (s *Server) supportAttachTo(w http.ResponseWriter, r *http.Request, user string) {
	ticketID, messageID := r.PathValue("id"), r.PathValue("message_id")
	auth, err := s.d.Support.AuthorizeSupportAttachment(r.Context(), &plansv1.AuthorizeSupportAttachmentRequest{UserId: user, TicketId: ticketID, MessageId: messageID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, invalid("content-type", "multipart/form-data with a boundary is required"))
		return
	}
	var res uploadResult
	ctx := storedUnder(r.Context(), auth.GetTarget())
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			writeError(w, r, invalid("file", "no file part"))
			return
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, r, errBodyTooLarge(mbe.Limit))
				return
			}
			writeError(w, r, invalid("body", "malformed multipart body"))
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		res, _ = s.uploadOne(ctx, part, assetsv1.Purpose_PURPOSE_SUPPORT_ATTACHMENT, &uploadBudget{})
		break
	}
	if res.Status != "created" {
		writeError(w, r, attachmentRefusal(res.ErrorCode))
		return
	}
	var asset assetsv1.Asset
	if err := protojson.Unmarshal(res.Asset, &asset); err != nil {
		writeError(w, r, err)
		return
	}
	added, err := s.d.Support.AddSupportAttachment(r.Context(), &plansv1.AddSupportAttachmentRequest{UserId: user, TicketId: ticketID, MessageId: messageID,
		TenantId: auth.GetTarget().GetTenantId(), AssetId: asset.GetId(), Filename: asset.GetFilename(), ContentType: asset.GetContentType(), SizeBytes: asset.GetSizeBytes()})
	if err != nil {
		// Nothing points at the file: take it back out of the storage (a failure here leaves an orphan that assets-svc's own sweep removes).
		if _, derr := s.d.Assets.DeleteAsset(ctx, &assetsv1.DeleteAssetRequest{AssetId: asset.GetId()}); derr != nil {
			s.d.Log.WarnContext(r.Context(), "support: an attachment that was refused could not be removed again", "asset_id", asset.GetId(), "error", derr)
		}
		writeError(w, r, err)
		return
	}
	respond(w, r, http.StatusCreated, added, nil)
}

// attachmentRefusal turns what assets-svc said about a refused file into the help desk's own error key.
func attachmentRefusal(code string) error {
	switch code {
	case "UNSUPPORTED_CONTENT_TYPE", "CONTENT_TYPE_MISMATCH":
		return errkey.New(codes.InvalidArgument, "support.attachment.bad_type", "only png, jpeg, pdf and plain text files can be attached")
	case "FILE_TOO_LARGE", "REQUEST_TOO_LARGE":
		return errkey.New(codes.InvalidArgument, "support.attachment.too_large", "a file can be at most 5 MiB")
	case "STORAGE_QUOTA_EXCEEDED":
		return errkey.New(codes.ResourceExhausted, "support.attachment.storage_full", "the storage of the app that holds support files is full")
	}
	return errkey.New(codes.InvalidArgument, "support.attachment.rejected", "the file cannot be attached")
}

// supportDownloadFor answers with a short-lived link to the file. The person gets one only for a file on a public message of a ticket of theirs.
func (s *Server) supportDownloadFor(w http.ResponseWriter, r *http.Request, user string) {
	resp, err := s.d.Support.GetSupportAttachment(r.Context(), &plansv1.GetSupportAttachmentRequest{UserId: user, TicketId: r.PathValue("id"), AttachmentId: r.PathValue("attachment_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	dl, err := s.d.Assets.GetDownloadUrl(storedUnder(r.Context(), resp.GetTarget()), &assetsv1.GetDownloadUrlRequest{AssetId: resp.GetTarget().GetAssetId()})
	noStore(w)
	respondSignedURL(w, r, dl.GetUrl(), dl.GetExpireTime(), err)
}
