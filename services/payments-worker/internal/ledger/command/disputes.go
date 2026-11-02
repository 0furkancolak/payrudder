package command

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// MaxNoteRunes is the longest note a seller can write on a case (the table checks the same).
const MaxNoteRunes = 2000

func disputeNotFound() *refuse.Error {
	return refuse.New(http.StatusNotFound, "payments.dispute.not_found", "no such dispute")
}

// AddNote writes the seller's note on a case. A note is text that says something: blank is refused, and so is one past MaxNoteRunes.
func AddNote(ctx context.Context, d Deps, tenant uuid.UUID, id int64, note string) (store.DisputeEvent, error) {
	note = strings.TrimSpace(note)
	switch {
	case note == "":
		return store.DisputeEvent{}, refuse.New(http.StatusBadRequest, "payments.dispute.note_required", "write what you want to remember about the case")
	case !utf8.ValidString(note) || utf8.RuneCountInString(note) > MaxNoteRunes:
		return store.DisputeEvent{}, refuse.New(http.StatusBadRequest, "payments.dispute.note_too_long", "a note is at most 2000 characters")
	}
	ev, _, err := d.Disputes.Annotate(ctx, tenant, id, "note", note, store.ActorOf(ctx))
	if errors.Is(err, store.ErrNoDispute) {
		return ev, disputeNotFound()
	}
	return ev, err
}

// SetHandled marks a case as handled by the seller, or lifts the mark, and answers the case as it stands. The mark is the seller's own: it changes nothing at the provider,
// in the ledger, or in what billing was told. Setting what is set is not an error and writes nothing.
func SetHandled(ctx context.Context, d Deps, tenant uuid.UUID, id int64, handled bool) (store.Dispute, error) {
	action := "unhandled"
	if handled {
		action = "handled"
	}
	if _, _, err := d.Disputes.Annotate(ctx, tenant, id, action, "", store.ActorOf(ctx)); errors.Is(err, store.ErrNoDispute) {
		return store.Dispute{}, disputeNotFound()
	} else if err != nil {
		return store.Dispute{}, err
	}
	return One(ctx, d, tenant, id)
}

// One is the case as it stands.
func One(ctx context.Context, d Deps, tenant uuid.UUID, id int64) (store.Dispute, error) {
	c, err := d.Disputes.One(ctx, tenant, id)
	if errors.Is(err, store.ErrNoDispute) {
		return c, disputeNotFound()
	}
	return c, err
}
