package support

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Files on messages. The bytes are in assets-svc under the first tenant of the ticket's account; this module is the record and the rules: who may attach to which
// message, how many files a ticket holds, how many bytes an account may keep, and which files a person may fetch (never those of an operator's internal note). The
// gateway moves the bytes: Authorize (may it, and where), upload, Add (record it); and for a download, Get and then a link from assets-svc.

func attachmentNotFound() error {
	return errkey.New(codes.NotFound, "support.attachment.not_found", "attachment not found")
}

func attachmentTargetOf(t sqlc.SupportStorageTenantRow) *plansv1.SupportAttachmentTarget {
	return &plansv1.SupportAttachmentTarget{TenantId: t.ID.String(), Environment: t.Environment, Timezone: t.Timezone, Tier: t.Tier}
}

// attachmentsOf groups a ticket's attachments by message; the files of internal notes only for the operator.
func attachmentsOf(ctx context.Context, q *sqlc.Queries, ticketID uuid.UUID, withInternal bool) (map[uuid.UUID][]*plansv1.SupportAttachment, error) {
	rows, err := q.ListSupportAttachments(ctx, sqlc.ListSupportAttachmentsParams{TicketID: ticketID, WithInternal: withInternal})
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]*plansv1.SupportAttachment{}
	for _, a := range rows {
		out[a.MessageID] = append(out[a.MessageID], &plansv1.SupportAttachment{Id: a.ID.String(), Filename: a.Filename, ContentType: a.ContentType, SizeBytes: a.SizeBytes, CreateTime: timestamppb.New(a.CreatedAt)})
	}
	return out, nil
}

// callerOf says who is attaching: a person (user_id given) or, when it is empty, the operator, which the context must prove.
func callerOf(ctx context.Context, userID string) (*uuid.UUID, error) {
	if userID == "" {
		return nil, tenant.RequireOperator(ctx)
	}
	u, err := uuid.Parse(userID)
	if err != nil {
		return nil, notFound()
	}
	return &u, nil
}

// attachable checks that the caller may add a file to the message and returns the ticket's account: the person must belong to the account and have written the
// message (not another member's, not the operator's), the operator may attach to the operator's own messages (notes included), and a closed ticket takes none.
func attachable(ctx context.Context, q *sqlc.Queries, user *uuid.UUID, ticketID, messageID uuid.UUID) (uuid.UUID, error) {
	m, err := q.SupportMessageForAttachment(ctx, sqlc.SupportMessageForAttachmentParams{ID: messageID, TicketID: ticketID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, notFound()
		}
		return uuid.Nil, err
	}
	if user != nil {
		if err := member(ctx, q, *user, m.AccountID); err != nil {
			return uuid.Nil, err
		}
		if m.AuthorKind != "customer" || m.AuthorUserID == nil || *m.AuthorUserID != *user {
			return uuid.Nil, notFound()
		}
	} else if m.AuthorKind != "operator" {
		return uuid.Nil, notFound()
	}
	if m.Status == "closed" {
		return uuid.Nil, errkey.New(codes.FailedPrecondition, "support.ticket.closed", "this ticket is closed; open a new one")
	}
	return m.AccountID, nil
}

// withinLimits refuses a file that would make the ticket hold more than its share or the account keep more than its share.
func withinLimits(ctx context.Context, q *sqlc.Queries, ticketID, account uuid.UUID, size int64) error {
	n, err := q.SupportAttachmentTotals(ctx, sqlc.SupportAttachmentTotalsParams{TicketID: ticketID, AccountID: account})
	if err != nil {
		return err
	}
	if n.OnTicket >= domain.SupportMaxAttachments {
		return errkey.Newf(codes.ResourceExhausted, "support.attachment.too_many", "a ticket can hold at most %d files", domain.SupportMaxAttachments)
	}
	if n.AccountBytes+size > domain.SupportMaxAccountAttachmentBytes {
		return errkey.Newf(codes.ResourceExhausted, "support.attachment.account_full", "an organisation can keep at most %d MiB of support files", domain.SupportMaxAccountAttachmentBytes>>20)
	}
	return nil
}

func (s *Service) AuthorizeSupportAttachment(ctx context.Context, req *plansv1.AuthorizeSupportAttachmentRequest) (*plansv1.AuthorizeSupportAttachmentResponse, error) {
	user, err := callerOf(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	ticketID, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	messageID, err := parse(req.GetMessageId())
	if err != nil {
		return nil, err
	}
	q := sqlc.New(s.pool)
	account, err := attachable(ctx, q, user, ticketID, messageID)
	if err != nil {
		return nil, err
	}
	// The size is not known yet: the check here is for room at all, the real one is made again when the file is recorded.
	if err := withinLimits(ctx, q, ticketID, account, 0); err != nil {
		return nil, err
	}
	t, err := q.SupportStorageTenant(ctx, account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errkey.New(codes.FailedPrecondition, "support.attachment.no_app", "files are stored with an app: create one first")
		}
		return nil, err
	}
	return &plansv1.AuthorizeSupportAttachmentResponse{Target: attachmentTargetOf(t)}, nil
}

func (s *Service) AddSupportAttachment(ctx context.Context, req *plansv1.AddSupportAttachmentRequest) (*plansv1.AddSupportAttachmentResponse, error) {
	user, err := callerOf(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	ticketID, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	messageID, err := parse(req.GetMessageId())
	if err != nil {
		return nil, err
	}
	tenantID, terr := uuid.Parse(req.GetTenantId())
	assetID, aerr := uuid.Parse(req.GetAssetId())
	name := strings.TrimSpace(req.GetFilename())
	if terr != nil || aerr != nil || name == "" || len(name) > 255 || req.GetSizeBytes() < 1 || req.GetSizeBytes() > domain.SupportMaxAttachmentBytes || !slices.Contains(domain.SupportAttachmentTypes, req.GetContentType()) {
		return nil, errkey.New(codes.InvalidArgument, "support.attachment.rejected", "the file cannot be attached")
	}
	out := &plansv1.SupportAttachment{Id: uuid.NewString(), Filename: name, ContentType: req.GetContentType(), SizeBytes: req.GetSizeBytes()}
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		// The ticket's lock makes the counts and the insert one step: two files sent at once cannot both take the last place.
		if _, err := lockedFor(ctx, q, ticketID, user); err != nil {
			return err
		}
		account, err := attachable(ctx, q, user, ticketID, messageID)
		if err != nil {
			return err
		}
		// The gateway stores under the tenant it was told by Authorize; it must still be one of the account's.
		if _, err := q.SupportTenantInAccount(ctx, sqlc.SupportTenantInAccountParams{ID: tenantID, AccountID: account}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errkey.New(codes.InvalidArgument, "support.attachment.rejected", "the file cannot be attached")
			}
			return err
		}
		if err := withinLimits(ctx, q, ticketID, account, req.GetSizeBytes()); err != nil {
			return err
		}
		if err := q.InsertSupportAttachment(ctx, sqlc.InsertSupportAttachmentParams{ID: uuid.MustParse(out.Id), TicketID: ticketID, MessageID: messageID, TenantID: tenantID, AssetID: assetID,
			Filename: name, ContentType: out.ContentType, SizeBytes: out.SizeBytes}); err != nil {
			return err
		}
		out.CreateTime = timestamppb.New(s.now())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.AddSupportAttachmentResponse{Attachment: out}, nil
}

func (s *Service) GetSupportAttachment(ctx context.Context, req *plansv1.GetSupportAttachmentRequest) (*plansv1.GetSupportAttachmentResponse, error) {
	user, err := callerOf(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	ticketID, err := parse(req.GetTicketId())
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetAttachmentId())
	if err != nil {
		return nil, attachmentNotFound()
	}
	q := sqlc.New(s.pool)
	a, err := q.GetSupportAttachment(ctx, sqlc.GetSupportAttachmentParams{ID: id, TicketID: ticketID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, attachmentNotFound()
		}
		return nil, err
	}
	if user != nil {
		// The same answer for a file that is not there, one of an internal note, and one of another account.
		if err := member(ctx, q, *user, a.AccountID); err != nil || a.Internal {
			return nil, attachmentNotFound()
		}
	}
	return &plansv1.GetSupportAttachmentResponse{Target: &plansv1.SupportAttachmentTarget{TenantId: a.TenantID.String(), Environment: a.Environment, Timezone: a.Timezone, Tier: a.Tier,
		AssetId: a.AssetID.String(), Filename: a.Filename}}, nil
}

// --- the counts of the sidebar

func (s *Service) GetMySupportSummary(ctx context.Context, req *plansv1.GetMySupportSummaryRequest) (*plansv1.GetMySupportSummaryResponse, error) {
	user, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notFound()
	}
	n, err := sqlc.New(s.pool).SupportUnreadCount(ctx, user)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMySupportSummaryResponse{Unread: n}, nil
}

func (s *Service) GetSupportSummary(ctx context.Context, _ *plansv1.GetSupportSummaryRequest) (*plansv1.GetSupportSummaryResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	c, err := sqlc.New(s.pool).SupportOperatorCounts(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetSupportSummaryResponse{Waiting: c.Waiting, SlaBreached: c.SlaBreached}, nil
}
