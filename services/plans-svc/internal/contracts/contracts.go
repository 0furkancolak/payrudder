// Package contracts is ContractService: the agreements a seller presents to its own customers (distance sales, pre-information, terms, privacy), the immutable
// versions of each, the append-only evidence that a customer saw and accepted a specific text, and the customer's withdrawal requests
// (docs/plans/21-contracts.md). It is tenant-scoped: every call runs in the tenant's transaction on the app role.
//
// Payrudder supplies templates and evidence, not legal guarantees. Nothing in this package says a text is sufficient in any country.
package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/contracts/snapshotpdf"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// AcceptedTopic is the Kafka topic of the ContractAccepted fact (docs/04-messaging.md), written to the outbox and relayed like subscription.price-change.v1.
const AcceptedTopic = "contract.accepted.v1"

// WithdrawalReceivedTopic is the Kafka topic of the ContractWithdrawalReceived fact: the acknowledgement mail of the withdrawal function of the hosted pages.
const WithdrawalReceivedTopic = "contract.withdrawal-received.v1"

// refundStale is how long a refund request stands as "requesting" before another caller may take it over: the caller that made it died before it recorded an answer.
const refundStale = 5 * time.Minute

// farFuture is the end of an acceptance log's time range when the caller names none.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// Service implements plansv1.ContractServiceServer.
type Service struct {
	plansv1.UnimplementedContractServiceServer
	db  *store.DB
	now func() time.Time
}

// New returns the service on the app pool (or its router when cells are on).
func New(db *store.DB) *Service { return &Service{db: db, now: time.Now} }

func (s *Service) tx(ctx context.Context, what string, fn func(q *sqlc.Queries) error) error {
	return convert.Err(s.db.Tx(ctx, fn), what)
}

func stamp(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

// micro is the precision Postgres keeps; the time a record and its snapshot hash are made from is cut to it, so that the page rendered from the stored row is the page
// that was hashed.
func micro(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func bodiesOf(raw json.RawMessage) map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func versionOf(v sqlc.ContractVersion, currentNumber int32) *plansv1.ContractVersion {
	bodies := bodiesOf(v.Bodies)
	var used []string
	for _, b := range bodies {
		u, _ := domain.ContractVariablesIn(b)
		for _, name := range u {
			if !slices.Contains(used, name) {
				used = append(used, name)
			}
		}
	}
	slices.Sort(used)
	return &plansv1.ContractVersion{Id: v.ID.String(), Kind: v.Kind, Audience: v.Audience, Number: v.Number, Material: v.Material, Bodies: bodies, Sha256: v.Sha256, Note: v.Note,
		Author: v.Author, CreateTime: stamp(v.CreatedAt), Variables: used, Current: v.Number == currentNumber, HasTemplateNotice: domain.AnyTemplateNotice(bodies)}
}

func currentNumbers(ctx context.Context, q *sqlc.Queries) (map[[2]string]int32, error) {
	rows, err := q.ListContractCurrent(ctx)
	if err != nil {
		return nil, err
	}
	out := map[[2]string]int32{}
	for _, r := range rows {
		out[[2]string{r.Kind, r.Audience}] = r.Number
	}
	return out, nil
}

func seriesOf(rows []sqlc.ListContractCurrentRow) []domain.ContractSeries {
	out := make([]domain.ContractSeries, len(rows))
	for i, r := range rows {
		out[i] = domain.ContractSeries{ID: r.ID.String(), Kind: r.Kind, Audience: r.Audience, Number: int(r.Number), MaterialNumber: int(r.MaterialNumber), Bodies: bodiesOf(r.Bodies), SHA256: r.Sha256}
	}
	return out
}

// PublishContractVersion adds the next version of (kind, audience).
func (s *Service) PublishContractVersion(ctx context.Context, req *plansv1.PublishContractVersionRequest) (*plansv1.PublishContractVersionResponse, error) {
	audience := req.GetAudience()
	if audience == "" {
		audience = domain.AudienceConsumer
	}
	switch {
	case !domain.ValidContractKind(req.GetKind()):
		return nil, domain.Invalid("kind", "one of "+strings.Join(domain.ContractKinds, ", "))
	case audience != domain.AudienceConsumer && audience != domain.AudienceBusiness:
		return nil, domain.Invalid("audience", "consumer or business")
	case len(req.GetNote()) > domain.MaxContractNote:
		return nil, domain.Invalid("note", "at most 500 characters")
	}
	if _, err := domain.ValidateContractBodies(req.GetBodies()); err != nil {
		return nil, err
	}
	tid, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.ContractVersion
	err = convert.Err(s.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		n, err := q.NextContractVersionNumber(ctx, sqlc.NextContractVersionNumberParams{Kind: req.GetKind(), Audience: audience})
		if err != nil {
			return err
		}
		bodies, err := json.Marshal(req.GetBodies())
		if err != nil {
			return err
		}
		v, err := q.InsertContractVersion(ctx, sqlc.InsertContractVersionParams{ID: idgen.NewUUIDv7(), Kind: req.GetKind(), Audience: audience, Number: n,
			Material: req.GetMaterial() || n == 1, Bodies: bodies, Sha256: domain.ContractHash(req.GetKind(), audience, req.GetBodies()), Note: req.GetNote(), Author: req.GetAuthor()})
		if err != nil {
			return err
		}
		out = versionOf(v, v.Number)
		if err := audit.Record(ctx, tx, audit.Event{TenantID: tid, Action: "contract.version_published", TargetType: "contract_version", TargetID: v.ID.String(),
			Summary: "published " + v.Kind + " (" + v.Audience + ") version " + strconv.Itoa(int(v.Number)),
			Detail:  map[string]any{"kind": v.Kind, "audience": v.Audience, "number": v.Number, "material": v.Material, "locales": sortedLocales(req.GetBodies())}}); err != nil { // the hash is in contract_versions; the log would mask it as an opaque run
			return err
		}
		data, err := json.Marshal(map[string]any{"version_id": v.ID.String(), "kind": v.Kind, "audience": v.Audience, "number": v.Number, "material": v.Material, "sha256": v.Sha256,
			"locales": sortedLocales(req.GetBodies()), "published_at": v.CreatedAt.UTC().Format(time.RFC3339)})
		if err != nil {
			return err
		}
		_, err = webhookscmd.Emit(ctx, q, v.ID, webhooksmodel.ContractVersionPublished, data, v.CreatedAt)
		return err
	}), "contract version")
	if err != nil {
		return nil, err
	}
	return &plansv1.PublishContractVersionResponse{Version: out}, nil
}

func sortedLocales(bodies map[string]string) []string {
	out := make([]string, 0, len(bodies))
	for l := range bodies {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

func (s *Service) ListContractVersions(ctx context.Context, req *plansv1.ListContractVersionsRequest) (*plansv1.ListContractVersionsResponse, error) {
	if k := req.GetKind(); k != "" && !domain.ValidContractKind(k) {
		return nil, domain.Invalid("kind", "one of "+strings.Join(domain.ContractKinds, ", "))
	}
	var out []*plansv1.ContractVersion
	err := s.tx(ctx, "contract version", func(q *sqlc.Queries) error {
		cur, err := currentNumbers(ctx, q)
		if err != nil {
			return err
		}
		rows, err := q.ListContractVersions(ctx, sqlc.ListContractVersionsParams{Kind: req.GetKind(), Audience: req.GetAudience()})
		if err != nil {
			return err
		}
		for _, v := range rows {
			out = append(out, versionOf(v, cur[[2]string{v.Kind, v.Audience}]))
		}
		return nil
	})
	return &plansv1.ListContractVersionsResponse{Versions: out}, err
}

func (s *Service) GetContractVersion(ctx context.Context, req *plansv1.GetContractVersionRequest) (*plansv1.GetContractVersionResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, versionNotFound()
	}
	var out *plansv1.ContractVersion
	err = s.tx(ctx, "contract version", func(q *sqlc.Queries) error {
		v, err := q.GetContractVersion(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return versionNotFound()
		}
		if err != nil {
			return err
		}
		cur, err := currentNumbers(ctx, q)
		if err != nil {
			return err
		}
		out = versionOf(v, cur[[2]string{v.Kind, v.Audience}])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.GetContractVersionResponse{Version: out}, nil
}

func versionNotFound() error {
	return errkey.New(codes.NotFound, "plans.contract.version_not_found", "no such contract version")
}

// PreviewContract renders a text the way a customer sees it, with sample values marked as variable.
func (s *Service) PreviewContract(_ context.Context, req *plansv1.PreviewContractRequest) (*plansv1.PreviewContractResponse, error) {
	if len(req.GetBody()) > domain.MaxContractBody {
		return nil, domain.Invalid("body", "too long")
	}
	used, unknown := domain.ContractVariablesIn(req.GetBody())
	return &plansv1.PreviewContractResponse{Html: domain.RenderContractHTML(req.GetBody(), domain.ContractSample, true), Variables: used, UnknownVariables: unknown}, nil
}

// GetContractStarter returns the starter text of a kind in a language.
func (s *Service) GetContractStarter(_ context.Context, req *plansv1.GetContractStarterRequest) (*plansv1.GetContractStarterResponse, error) {
	if !domain.ValidContractKind(req.GetKind()) {
		return nil, domain.Invalid("kind", "one of "+strings.Join(domain.ContractKinds, ", "))
	}
	body, loc, ok := Starter(req.GetKind(), req.GetLocale())
	if !ok {
		return nil, errkey.New(codes.NotFound, "plans.contract.no_starter", "there is a starter text in Turkish and English only")
	}
	return &plansv1.GetContractStarterResponse{Body: body, Locale: loc}, nil
}

func settingsOf(st sqlc.ContractSetting, published []string) *plansv1.ContractSettings {
	return &plansv1.ContractSettings{RequiredKinds: orEmpty(st.RequiredKinds), ImmediateStart: st.ImmediateStart, RetentionMonths: st.RetentionMonths, PublishedKinds: published, UpdateTime: stamp(st.UpdatedAt),
		WithdrawalRefund: st.WithdrawalRefund, RenewalNoticeDays: st.RenewalNoticeDays, RenewalCancelTexts: bodiesOf(st.RenewalCancelTexts), WithdrawalFunction: st.WithdrawalFunction}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// loadSettings reads the tenant's settings; a tenant that never saved any has none enforced and the default retention.
func loadSettings(ctx context.Context, q *sqlc.Queries) (sqlc.ContractSetting, error) {
	st, err := q.GetContractSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.ContractSetting{RequiredKinds: []string{}, RetentionMonths: domain.DefaultRetention, WithdrawalRefund: domain.WithdrawalRefundFull,
			RenewalNoticeDays: domain.DefaultRenewalNoticeDays, RenewalCancelTexts: json.RawMessage(`{}`), WithdrawalFunction: true}, nil
	}
	return st, err
}

func publishedKinds(rows []sqlc.ListContractCurrentRow) []string {
	var out []string
	for _, k := range domain.ContractKinds {
		for _, r := range rows {
			if r.Kind == k && r.Audience == domain.AudienceConsumer {
				out = append(out, k)
				break
			}
		}
	}
	return orEmpty(out)
}

func (s *Service) GetContractSettings(ctx context.Context, _ *plansv1.GetContractSettingsRequest) (*plansv1.GetContractSettingsResponse, error) {
	var out *plansv1.ContractSettings
	err := s.tx(ctx, "contract settings", func(q *sqlc.Queries) error {
		st, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		cur, err := q.ListContractCurrent(ctx)
		if err != nil {
			return err
		}
		out = settingsOf(st, publishedKinds(cur))
		if st.UpdatedAt.IsZero() {
			out.UpdateTime = nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.GetContractSettingsResponse{Settings: out}, nil
}

// SetContractSettings changes what is enforced. A kind can be required only once it has a published version, so that no payment is ever refused for a missing text.
func (s *Service) SetContractSettings(ctx context.Context, req *plansv1.SetContractSettingsRequest) (*plansv1.SetContractSettingsResponse, error) {
	var required []string
	for _, k := range req.GetRequiredKinds() {
		if !domain.ValidContractKind(k) {
			return nil, domain.Invalid("required_kinds", "one of "+strings.Join(domain.ContractKinds, ", "))
		}
		if !slices.Contains(required, k) {
			required = append(required, k)
		}
	}
	slices.SortFunc(required, func(a, b string) int {
		return slices.Index(domain.ContractKinds, a) - slices.Index(domain.ContractKinds, b)
	})
	if m := req.GetRetentionMonths(); m != 0 && (m < domain.MinRetentionMths || m > domain.MaxRetentionMths) {
		return nil, domain.Invalid("retention_months", "between 12 and 120")
	}
	if r := req.GetWithdrawalRefund(); r != "" && r != domain.WithdrawalRefundFull && r != domain.WithdrawalRefundNone {
		return nil, domain.Invalid("withdrawal_refund", "full or none")
	}
	if req.RenewalNoticeDays != nil && (req.GetRenewalNoticeDays() < 0 || req.GetRenewalNoticeDays() > domain.MaxRenewalNoticeDays) {
		return nil, domain.Invalid("renewal_notice_days", "between 0 and 60")
	}
	if err := domain.ValidateCancelTexts(req.GetRenewalCancelTexts()); err != nil {
		return nil, err
	}
	tid, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.ContractSettings
	err = convert.Err(s.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.ListContractCurrent(ctx)
		if err != nil {
			return err
		}
		published := publishedKinds(cur)
		for _, k := range required {
			if !slices.Contains(published, k) {
				return errkey.Newf(codes.FailedPrecondition, "plans.contract.kind_unpublished", "%s cannot be required before a version of it is published", k)
			}
		}
		old, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		months := old.RetentionMonths
		if req.GetRetentionMonths() != 0 {
			months = req.GetRetentionMonths()
		}
		refund, days, function := old.WithdrawalRefund, old.RenewalNoticeDays, old.WithdrawalFunction
		if req.GetWithdrawalRefund() != "" {
			refund = req.GetWithdrawalRefund()
		}
		if req.RenewalNoticeDays != nil {
			days = req.GetRenewalNoticeDays()
		}
		if req.WithdrawalFunction != nil {
			function = req.GetWithdrawalFunction()
		}
		texts := bodiesOf(old.RenewalCancelTexts)
		for loc, t := range req.GetRenewalCancelTexts() {
			if strings.TrimSpace(t) == "" {
				delete(texts, loc)
			} else {
				texts[loc] = t
			}
		}
		textsJSON, err := json.Marshal(texts)
		if err != nil {
			return err
		}
		st, err := q.UpsertContractSettings(ctx, sqlc.UpsertContractSettingsParams{RequiredKinds: orEmpty(required), ImmediateStart: req.GetImmediateStart(), RetentionMonths: months,
			WithdrawalRefund: refund, RenewalNoticeDays: days, RenewalCancelTexts: textsJSON, WithdrawalFunction: function})
		if err != nil {
			return err
		}
		out = settingsOf(st, published)
		// The texts of the cancel notices are the seller's own wording and stay out of the entry; their locales do not.
		return audit.Record(ctx, tx, audit.Event{TenantID: tid, Action: "contract.settings_changed", TargetType: "contract_settings", Before: old, After: st,
			Summary: "changed the contract settings", Detail: map[string]any{"required_kinds": required, "immediate_start": st.ImmediateStart, "retention_months": st.RetentionMonths,
				"withdrawal_refund": st.WithdrawalRefund, "renewal_notice_days": st.RenewalNoticeDays, "withdrawal_function": st.WithdrawalFunction,
				"renewal_cancel_text_locales": sortedLocales(texts)}})
	}), "contract settings")
	if err != nil {
		return nil, err
	}
	return &plansv1.SetContractSettingsResponse{Settings: out}, nil
}

// ---- requirements and the gate ----------------------------------------------------------------------------------------------------------------------------

// who is the person the requirements are about: a customer, or a guest known by the invoice they pay.
type who struct {
	customer *sqlc.Customer
	ref      string
	typ      string
}

func (s *Service) resolve(ctx context.Context, q *sqlc.Queries, externalID, id, ref, customerType string) (who, error) {
	var w who
	switch {
	case externalID != "":
		c, err := q.GetCustomerByExternalID(ctx, externalID)
		if err != nil {
			return w, convert.Err(err, "customer")
		}
		w.customer = &c
	case id != "":
		cid, err := domain.ParseID("customer_id", id)
		if err != nil {
			return w, err
		}
		c, err := q.GetCustomerByID(ctx, cid)
		if err != nil {
			return w, convert.Err(err, "customer")
		}
		w.customer = &c
	case ref == "":
		return w, domain.Invalid("customer_external_id", "a customer, or a context_ref for a guest")
	}
	w.ref = ref
	w.typ = customerType
	if w.typ == "" && w.customer != nil {
		w.typ = w.customer.CustomerType
	}
	return w, nil
}

// need is one required kind as the customer stands with it.
type need struct {
	series   domain.ContractSeries
	accepted bool
	at       time.Time
}

// state is what the tenant requires of a person and how far they are.
type state struct {
	settings sqlc.ContractSetting
	audience string
	needs    []need
	missing  []string
}

func (st *state) pending() []string {
	var out []string
	for _, n := range st.needs {
		if !n.accepted {
			out = append(out, n.series.Kind)
		}
	}
	return append(out, st.missing...)
}

func (st *state) enforced() bool { return len(st.settings.RequiredKinds) > 0 }

// evaluate reads what the tenant requires of a person. kinds, when it has any, are required instead of the tenant's setting (a payment link names its own); they must be kinds that exist.
func evaluate(ctx context.Context, q *sqlc.Queries, w who, kinds []string) (*state, error) {
	settings, err := loadSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(kinds) > 0 {
		for _, k := range kinds {
			if !domain.ValidContractKind(k) {
				return nil, domain.Invalid("required_kinds", "one of "+strings.Join(domain.ContractKinds, ", "))
			}
		}
		settings.RequiredKinds = kinds
	}
	st := &state{settings: settings, audience: domain.AudienceOf(w.typ)}
	if len(settings.RequiredKinds) == 0 {
		return st, nil
	}
	cur, err := q.ListContractCurrent(ctx)
	if err != nil {
		return nil, err
	}
	series := seriesOf(cur)
	accepted := map[[2]string]domain.AcceptedNumbers{}
	if w.customer != nil {
		rows, err := q.CustomerAcceptedNumbers(ctx, &w.customer.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			accepted[[2]string{r.Kind, r.Audience}] = domain.AcceptedNumbers{Number: int(r.Number), ConsentNumber: int(r.ConsentNumber), At: r.AcceptedAt}
		}
	} else {
		rows, err := q.ReferenceAcceptedNumbers(ctx, w.ref)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			accepted[[2]string{r.Kind, r.Audience}] = domain.AcceptedNumbers{Number: int(r.Number), ConsentNumber: int(r.ConsentNumber), At: r.AcceptedAt}
		}
	}
	needConsent := settings.ImmediateStart && st.audience == domain.AudienceConsumer
	for _, kind := range domain.ContractKinds {
		if !slices.Contains(settings.RequiredKinds, kind) {
			continue
		}
		sr, ok := domain.ChooseSeries(series, kind, st.audience)
		if !ok {
			st.missing = append(st.missing, kind)
			continue
		}
		a := accepted[[2]string{sr.Kind, sr.Audience}]
		st.needs = append(st.needs, need{series: sr, accepted: a.Covers(sr, needConsent), at: a.At})
	}
	return st, nil
}

// Check is the gate of the paths that start something for a customer without a page to ask on (a subscription made through the API): it refuses with
// plans.contract.not_accepted while a kind the seller requires is not accepted by the customer, in the version that counts. A seller that requires nothing never
// refuses. Renewals and dunning charges never call it: nobody is there to accept.
func Check(ctx context.Context, q *sqlc.Queries, c sqlc.Customer) error {
	st, err := evaluate(ctx, q, who{customer: &c, typ: c.CustomerType}, nil)
	if err != nil {
		return err
	}
	if !st.enforced() {
		return nil
	}
	if p := st.pending(); len(p) > 0 {
		return errkey.Newf(codes.FailedPrecondition, "plans.contract.not_accepted", "the customer has not accepted: %s (POST /v1/customers/{id}/contract-acceptances)", strings.Join(p, ", "))
	}
	return nil
}

// context resolves the variables of the texts for a person and an order.
func (s *Service) values(ctx context.Context, q *sqlc.Queries, w who, order *plansv1.ContractOrder, guestName string, at time.Time) (domain.ContractContext, error) {
	seller, err := q.ContractSeller(ctx)
	if err != nil {
		return domain.ContractContext{}, err
	}
	c := domain.ContractContext{SellerName: seller.Name, OrderDescription: order.GetDescription(), OrderTotal: order.GetTotal(), OrderCurrency: order.GetCurrency(), At: at}
	if legal := convert.LegalFromJSON(seller.Legal); legal != nil {
		if legal.GetLegalName() != "" {
			c.SellerName = legal.GetLegalName()
		}
		c.SellerTaxID, c.SellerTaxOffice, c.SellerMersis = legal.GetTaxId(), legal.GetTaxOffice(), legal.GetMersisNo()
	}
	c.SellerAddress = addressText(convert.AddressFromJSON(seller.Address))
	if w.customer != nil {
		c.CustomerName, c.CustomerEmail = w.customer.Name, w.customer.Email
		c.CustomerAddress = addressText(convert.AddressFromJSON(w.customer.Address))
	} else {
		c.CustomerName = guestName
	}
	return c, nil
}

func addressText(a *plansv1.Address) string {
	if a == nil {
		return ""
	}
	var parts []string
	for _, p := range []string{a.GetLine1(), a.GetLine2(), strings.TrimSpace(a.GetPostalCode() + " " + a.GetCity()), a.GetRegion(), a.GetCountry()} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	return strings.Join(parts, ", ")
}

// GetContractRequirements says what the person must accept, with the text to show.
func (s *Service) GetContractRequirements(ctx context.Context, req *plansv1.GetContractRequirementsRequest) (*plansv1.GetContractRequirementsResponse, error) {
	var out *plansv1.GetContractRequirementsResponse
	err := s.tx(ctx, "contract", func(q *sqlc.Queries) error {
		w, err := s.resolve(ctx, q, req.GetCustomerExternalId(), req.GetCustomerId(), req.GetContextRef(), req.GetCustomerType())
		if err != nil {
			return err
		}
		st, err := evaluate(ctx, q, w, req.GetRequiredKinds())
		if err != nil {
			return err
		}
		out = &plansv1.GetContractRequirementsResponse{Enforced: st.enforced(), Audience: st.audience, Satisfied: len(st.pending()) == 0, MissingKinds: orEmpty(st.missing),
			ImmediateStart: st.enforced() && st.settings.ImmediateStart && st.audience == domain.AudienceConsumer}
		if len(st.needs) == 0 {
			return nil
		}
		cc, err := s.values(ctx, q, w, req.GetOrder(), req.GetGuestName(), micro(s.now()))
		if err != nil {
			return err
		}
		vals := cc.Values()
		for _, n := range st.needs {
			body, served := domain.PickContractBody(n.series.Bodies, req.GetLocale())
			r := &plansv1.ContractRequirement{Kind: n.series.Kind, VersionId: n.series.ID, VersionNumber: int32(n.series.Number), Audience: n.series.Audience, Locale: served, //nolint:gosec // a version number
				BodyHtml: domain.RenderContractHTML(body, vals, false), BodySha256: domain.TextHash(body), Accepted: n.accepted, HasTemplateNotice: domain.HasTemplateNotice(body)}
			if n.accepted && !n.at.IsZero() {
				r.AcceptTime = stamp(n.at)
			}
			out.Requirements = append(out.Requirements, r)
		}
		return nil
	})
	return out, err
}

// ---- acceptances ------------------------------------------------------------------------------------------------------------------------------------------

func acceptanceOf(a sqlc.ContractAcceptance, externalID string) *plansv1.ContractAcceptance {
	out := &plansv1.ContractAcceptance{Id: a.ID.String(), GroupId: a.GroupID.String(), VersionId: a.VersionID.String(), Kind: a.Kind, Audience: a.Audience, VersionNumber: a.VersionNumber,
		VersionSha256: a.VersionSha256, LocaleRequested: a.LocaleRequested, LocaleServed: a.LocaleServed, BodySha256: a.BodySha256, ContextKind: a.ContextKind, ContextRef: a.ContextRef,
		Source: a.Source, CheckboxText: a.CheckboxText, ImmediateStartConsent: a.ImmediateStartConsent, ImmediateStartText: a.ImmediateStartText,
		WithdrawalWaiverAcknowledged: a.WithdrawalWaiverAcknowledged, WithdrawalWaiverText: a.WithdrawalWaiverText, SnapshotSha256: a.SnapshotSha256, Ip: a.Ip, UserAgent: a.UserAgent,
		AcceptTime: stamp(a.AcceptedAt), CustomerExternalId: externalID, Observed: map[string]bool{}, Variables: map[string]string{}}
	if a.CustomerID != nil {
		out.CustomerId = a.CustomerID.String()
	}
	_ = json.Unmarshal(a.Observed, &out.Observed)
	_ = json.Unmarshal(a.Variables, &out.Variables)
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// RecordContractAcceptance writes the evidence that a person accepted versions together: one row per version, one group, one snapshot page whose hash every row holds.
func (s *Service) RecordContractAcceptance(ctx context.Context, req *plansv1.RecordContractAcceptanceRequest) (*plansv1.RecordContractAcceptanceResponse, error) {
	source := req.GetSource()
	contextKind := map[string]string{"hosted": "hosted_invoice", "api": "api"}[source]
	if source == "hosted" && req.GetContextKind() == "portal" {
		contextKind = "portal" // asked for on the customer portal (a new material version between two payments), not on an invoice
	}
	switch {
	case contextKind == "":
		return nil, domain.Invalid("source", "hosted or api")
	case req.GetContextKind() != "" && req.GetContextKind() != contextKind:
		return nil, domain.Invalid("context_kind", contextKind+" for source "+source)
	case len(req.GetItems()) == 0:
		return nil, domain.Invalid("items", "at least one version to accept")
	case len(req.GetItems()) > len(domain.ContractKinds):
		return nil, domain.Invalid("items", "at most one version of each kind")
	case len(req.GetContextRef()) > 200:
		return nil, domain.Invalid("context_ref", "at most 200 characters")
	case req.GetWithdrawalWaiverAcknowledged() && !req.GetImmediateStartConsent():
		return nil, domain.Invalid("withdrawal_waiver_acknowledged", "the waiver goes with the request to start at once")
	case len(req.GetImmediateStartText()) > domain.MaxCheckboxText || len(req.GetWithdrawalWaiverText()) > domain.MaxCheckboxText:
		return nil, domain.Invalid("immediate_start_text", "at most 1000 characters")
	}
	at := micro(s.now())
	observed := map[string]bool{"ip": source == "hosted", "user_agent": source == "hosted", "time": true, "text": source == "hosted"}
	if source == "api" && req.GetAcceptTime() != nil {
		if err := req.GetAcceptTime().CheckValid(); err != nil || req.GetAcceptTime().AsTime().After(at.Add(5*time.Minute)) {
			return nil, domain.Invalid("accept_time", "a valid time that is not in the future")
		}
		at, observed["time"] = micro(req.GetAcceptTime().AsTime()), false
	}
	ip, ua := truncate(req.GetIp(), 64), truncate(req.GetUserAgent(), 500)
	var out *plansv1.RecordContractAcceptanceResponse
	err := s.tx(ctx, "contract acceptance", func(q *sqlc.Queries) error {
		w, err := s.resolve(ctx, q, req.GetCustomerExternalId(), req.GetCustomerId(), req.GetContextRef(), req.GetCustomerType())
		if err != nil {
			return err
		}
		cur, err := q.ListContractCurrent(ctx)
		if err != nil {
			return err
		}
		settings, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		type picked struct {
			v         sqlc.ContractVersion
			bodies    map[string]string
			body      string
			served    string
			checkbox  string
			sortIndex int
		}
		var items []picked
		seen := map[string]bool{}
		for i, it := range req.GetItems() {
			vid, err := uuid.Parse(it.GetVersionId())
			if err != nil {
				return versionNotFound()
			}
			v, err := q.GetContractVersion(ctx, vid)
			if errors.Is(err, pgx.ErrNoRows) {
				return versionNotFound()
			}
			if err != nil {
				return err
			}
			if seen[v.Kind] {
				return domain.Invalid("items", "at most one version of each kind")
			}
			seen[v.Kind] = true
			isCurrent := false
			for _, c := range cur {
				if c.Kind == v.Kind && c.Audience == v.Audience && c.Number == v.Number {
					isCurrent = true
				}
			}
			if !isCurrent {
				return errkey.New(codes.FailedPrecondition, "plans.contract.version_stale", "a newer version of this text was published: show the current one and ask again")
			}
			text := strings.TrimSpace(it.GetCheckboxText())
			if text == "" || len(text) > domain.MaxCheckboxText {
				return domain.Invalid("items["+strconv.Itoa(i)+"].checkbox_text", "the words next to the box, up to 1000 characters")
			}
			bodies := bodiesOf(v.Bodies)
			body, served := domain.PickContractBody(bodies, req.GetLocale())
			items = append(items, picked{v: v, bodies: bodies, body: body, served: served, checkbox: text, sortIndex: slices.Index(domain.ContractKinds, v.Kind)})
		}
		slices.SortFunc(items, func(a, b picked) int { return a.sortIndex - b.sortIndex })
		consumer := false
		distance := false
		for _, it := range items {
			consumer = consumer || it.v.Audience == domain.AudienceConsumer
			distance = distance || it.v.Kind == domain.ContractDistanceSales
		}
		if settings.ImmediateStart && consumer && distance && (!req.GetImmediateStartConsent() || !req.GetWithdrawalWaiverAcknowledged() ||
			strings.TrimSpace(req.GetImmediateStartText()) == "" || strings.TrimSpace(req.GetWithdrawalWaiverText()) == "") {
			return errkey.New(codes.FailedPrecondition, "plans.contract.consent_required", "this seller starts the service at once: the request to start now and the acknowledgement of losing the right of withdrawal are required, each with its words")
		}
		cc, err := s.values(ctx, q, w, req.GetOrder(), req.GetGuestName(), at)
		if err != nil {
			return err
		}
		vals := cc.Values()
		snap := domain.Snapshot{Seller: vals["seller_name"], Customer: vals["customer_name"], Email: vals["customer_email"], AcceptedAt: at, Locale: req.GetLocale(), Values: vals,
			Source: source, Observed: observed}
		if req.GetImmediateStartConsent() {
			snap.ImmediateStart = req.GetImmediateStartText()
		}
		if req.GetWithdrawalWaiverAcknowledged() {
			snap.WaiverAck = req.GetWithdrawalWaiverText()
		}
		if loc := items[0].served; loc != "" {
			snap.Locale = loc
		}
		for _, it := range items {
			snap.Items = append(snap.Items, domain.SnapshotItem{Kind: it.v.Kind, Number: int(it.v.Number), LocaleServed: it.served, Body: it.body, BodySHA256: domain.TextHash(it.body), VersionSHA256: it.v.Sha256, CheckboxText: it.checkbox})
		}
		page := domain.RenderSnapshot(snap)
		pageHash := domain.TextHash(page)
		observedJSON, _ := json.Marshal(observed)
		varsJSON, _ := json.Marshal(vals)
		group := idgen.NewUUIDv7()
		out = &plansv1.RecordContractAcceptanceResponse{GroupId: group.String()}
		var customerID *uuid.UUID
		if w.customer != nil {
			customerID = &w.customer.ID
		}
		var first uuid.UUID
		for _, it := range items {
			row, err := q.InsertContractAcceptance(ctx, sqlc.InsertContractAcceptanceParams{ID: idgen.NewUUIDv7(), GroupID: group, CustomerID: customerID, VersionID: it.v.ID, Kind: it.v.Kind,
				Audience: it.v.Audience, VersionNumber: it.v.Number, VersionSha256: it.v.Sha256, LocaleRequested: req.GetLocale(), LocaleServed: it.served, BodySha256: domain.TextHash(it.body),
				ContextKind: contextKind, ContextRef: req.GetContextRef(), Source: source, Observed: observedJSON, CheckboxText: it.checkbox, ImmediateStartConsent: req.GetImmediateStartConsent(),
				ImmediateStartText: snap.ImmediateStart, WithdrawalWaiverAcknowledged: req.GetWithdrawalWaiverAcknowledged(), WithdrawalWaiverText: snap.WaiverAck, Variables: varsJSON,
				SnapshotSha256: pageHash, Ip: ip, UserAgent: ua, AcceptedAt: at})
			if err != nil {
				return err
			}
			if first == uuid.Nil {
				first = row.ID
			}
			out.Acceptances = append(out.Acceptances, acceptanceOf(row, deref(customerExternalID(w))))
		}
		if err := q.InsertContractSnapshot(ctx, sqlc.InsertContractSnapshotParams{GroupID: group, Html: page, Sha256: pageHash}); err != nil {
			return err
		}
		return s.announce(ctx, q, w, group, first, pageHash, at, out.Acceptances, source)
	})
	return out, err
}

func customerExternalID(w who) *string {
	if w.customer == nil {
		return nil
	}
	return &w.customer.ExternalID
}

// announce queues the webhook event and, for a customer, the fact the notifier mails: both in the transaction of the acceptance.
func (s *Service) announce(ctx context.Context, q *sqlc.Queries, w who, group, first uuid.UUID, pageHash string, at time.Time, rows []*plansv1.ContractAcceptance, source string) error {
	items := make([]map[string]any, len(rows))
	for i, r := range rows {
		items[i] = map[string]any{"acceptance_id": r.GetId(), "kind": r.GetKind(), "audience": r.GetAudience(), "version_id": r.GetVersionId(), "version": r.GetVersionNumber(), "sha256": r.GetVersionSha256(),
			"locale": r.GetLocaleServed()}
	}
	data := map[string]any{"group_id": group.String(), "source": source, "accepted_at": at.Format(time.RFC3339), "snapshot_sha256": pageHash, "items": items,
		"immediate_start_consent": rows[0].GetImmediateStartConsent(), "withdrawal_waiver_acknowledged": rows[0].GetWithdrawalWaiverAcknowledged(),
		"context": map[string]any{"kind": rows[0].GetContextKind(), "ref": rows[0].GetContextRef()}}
	if w.customer != nil {
		data["customer_id"], data["customer_external_id"] = w.customer.ID.String(), w.customer.ExternalID
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := webhookscmd.Emit(ctx, q, group, webhooksmodel.ContractAccepted, raw, at); err != nil {
		return err
	}
	if w.customer == nil || w.customer.Email == "" {
		return nil
	}
	ti, err := tenantOf(ctx)
	if err != nil {
		return err
	}
	payload, err := proto.Marshal(&eventsv1.ContractAccepted{EventId: group.String(), TenantId: ti.String(), CustomerId: w.customer.ID.String(), AcceptanceId: first.String(), SnapshotSha256: pageHash,
		AcceptedAt: stamp(at)})
	if err != nil {
		return err
	}
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: AcceptedTopic, PartitionKey: ti.String() + "/" + w.customer.ID.String(), Payload: payload}); err != nil {
		return err
	}
	return q.NotifyOutbox(ctx)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func tenantOf(ctx context.Context) (uuid.UUID, error) {
	ti, err := tenant.Require(ctx)
	return ti.ID, err
}

// ListContractAcceptances is a customer's acceptance log, newest first, a page at a time, narrowed by kind, source, version and the time of acceptance.
func (s *Service) ListContractAcceptances(ctx context.Context, req *plansv1.ListContractAcceptancesRequest) (*plansv1.ListContractAcceptancesResponse, error) {
	size, ok := page.Size(req.GetPageSize(), 100, 200)
	if !ok {
		return nil, domain.Invalid("page_size", "1..200")
	}
	p := sqlc.ListCustomerContractAcceptancesParams{Kind: req.GetKind(), Source: req.GetSource(), PageSize: size + 1, FromTime: time.Time{}, ToTime: farFuture}
	if p.Kind != "" && !domain.ValidContractKind(p.Kind) {
		return nil, domain.Invalid("kind", "one of "+strings.Join(domain.ContractKinds, ", "))
	}
	if p.Source != "" && !slices.Contains([]string{"api", "hosted"}, p.Source) {
		return nil, domain.Invalid("source", "api or hosted")
	}
	if v := req.GetVersionId(); v != "" {
		id, err := domain.ParseID("version_id", v)
		if err != nil {
			return nil, err
		}
		p.VersionID = id
	}
	for field, bound := range map[string]struct {
		in  *timestamppb.Timestamp
		out *time.Time
	}{"from_time": {req.GetFromTime(), &p.FromTime}, "to_time": {req.GetToTime(), &p.ToTime}} {
		if bound.in == nil {
			continue
		}
		if err := bound.in.CheckValid(); err != nil {
			return nil, domain.Invalid(field, "a valid time")
		}
		*bound.out = bound.in.AsTime()
	}
	cur := page.Start()
	if tok := req.GetPageToken(); tok != "" {
		if cur, ok = page.ParseCursor(tok); !ok {
			return nil, domain.Invalid("page_token", "not one this API gave")
		}
	}
	p.AfterTime, p.AfterID = cur.At, cur.ID
	var out []*plansv1.ContractAcceptance
	err := s.tx(ctx, "contract acceptance", func(q *sqlc.Queries) error {
		w, err := s.resolve(ctx, q, req.GetCustomerExternalId(), req.GetCustomerId(), "", "")
		if err != nil {
			return err
		}
		if w.customer == nil {
			return domain.Invalid("customer_external_id", "required")
		}
		p.CustomerID = &w.customer.ID
		rows, err := q.ListCustomerContractAcceptances(ctx, p)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, acceptanceOf(sqlc.ContractAcceptance{TenantID: r.TenantID, ID: r.ID, GroupID: r.GroupID, CustomerID: r.CustomerID, VersionID: r.VersionID, Kind: r.Kind,
				Audience: r.Audience, VersionNumber: r.VersionNumber, VersionSha256: r.VersionSha256, LocaleRequested: r.LocaleRequested, LocaleServed: r.LocaleServed, BodySha256: r.BodySha256,
				ContextKind: r.ContextKind, ContextRef: r.ContextRef, Source: r.Source, Observed: r.Observed, CheckboxText: r.CheckboxText, ImmediateStartConsent: r.ImmediateStartConsent,
				ImmediateStartText: r.ImmediateStartText, WithdrawalWaiverAcknowledged: r.WithdrawalWaiverAcknowledged, WithdrawalWaiverText: r.WithdrawalWaiverText, Variables: r.Variables,
				SnapshotSha256: r.SnapshotSha256, Ip: r.Ip, UserAgent: r.UserAgent, AcceptedAt: r.AcceptedAt}, deref(r.CustomerExternalID)))
		}
		return nil
	})
	resp := &plansv1.ListContractAcceptancesResponse{}
	if err != nil {
		return resp, err
	}
	var more bool
	if resp.Acceptances, more = page.Trim(out, int(size)); more {
		last := resp.Acceptances[len(resp.Acceptances)-1]
		id, _ := uuid.Parse(last.Id) // an id we made
		resp.NextPageToken = page.Cursor{At: last.AcceptTime.AsTime(), ID: id}.Token()
	}
	return resp, nil
}

// GetContractSnapshot returns the page the acceptance's group was shown to and accepted, as it was written then, with its sha256 (and, when asked, the same page as a PDF). The page is checked against the hash the
// acceptance holds before it is returned: a page that no longer matches is an error, never evidence.
func (s *Service) GetContractSnapshot(ctx context.Context, req *plansv1.GetContractSnapshotRequest) (*plansv1.GetContractSnapshotResponse, error) {
	id, err := uuid.Parse(req.GetAcceptanceId())
	if err != nil {
		return nil, acceptanceNotFound()
	}
	var out *plansv1.GetContractSnapshotResponse
	err = s.tx(ctx, "contract acceptance", func(q *sqlc.Queries) error {
		a, err := q.GetContractAcceptance(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return acceptanceNotFound()
		}
		if err != nil {
			return err
		}
		snap, err := q.GetContractSnapshotOfGroup(ctx, a.GroupID)
		if err != nil {
			return err
		}
		if domain.TextHash(snap.Html) != a.SnapshotSha256 || snap.Sha256 != a.SnapshotSha256 {
			return status.Error(codes.DataLoss, "the stored page does not match its hash")
		}
		out = &plansv1.GetContractSnapshotResponse{Html: snap.Html, Sha256: snap.Sha256, GroupId: a.GroupID.String()}
		if req.GetIncludePdf() {
			switch pdf, err := snapshotpdf.Render(snap.Html, a.AcceptedAt); {
			case errors.Is(err, snapshotpdf.ErrUnsupported):
				out.PdfUnavailable = true
			case err != nil:
				return err
			default:
				out.Pdf = pdf
			}
		}
		return nil
	})
	return out, err
}

func acceptanceNotFound() error {
	return errkey.New(codes.NotFound, "plans.contract.acceptance_not_found", "no such acceptance")
}

// ---- withdrawals ------------------------------------------------------------------------------------------------------------------------------------------

func withdrawalOf(w sqlc.ContractWithdrawal, externalID string) *plansv1.ContractWithdrawal {
	out := &plansv1.ContractWithdrawal{Id: w.ID.String(), CustomerId: w.CustomerID.String(), CustomerExternalId: externalID, Statement: w.Statement, Source: w.Source, Status: w.Status,
		ReceiveTime: stamp(w.ReceivedAt), DecidedBy: w.DecidedBy, DecisionNote: w.DecisionNote, RefundState: w.RefundState, RefundNote: w.RefundNote}
	if w.CreditNoteID != nil {
		out.CreditNoteId = w.CreditNoteID.String()
	}
	if w.RefundAt != nil {
		out.RefundTime = stamp(*w.RefundAt)
	}
	if w.SubscriptionID != nil {
		out.SubscriptionId = w.SubscriptionID.String()
	}
	if w.InvoiceID != nil {
		out.InvoiceId = w.InvoiceID.String()
	}
	if w.DecidedAt != nil {
		out.DecideTime = stamp(*w.DecidedAt)
	}
	return out
}

func optUUID(field, raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := domain.ParseID(field, raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// CreateContractWithdrawal records the statement of a customer who withdraws. Deciding it and refunding stay with the seller (docs/plans/21-contracts.md).
func (s *Service) CreateContractWithdrawal(ctx context.Context, req *plansv1.CreateContractWithdrawalRequest) (*plansv1.CreateContractWithdrawalResponse, error) {
	source := req.GetSource()
	if source == "" {
		source = "api"
	}
	if !slices.Contains([]string{"api", "console", "hosted"}, source) {
		return nil, domain.Invalid("source", "api, console or hosted")
	}
	if len(req.GetStatement()) > 2000 {
		return nil, domain.Invalid("statement", "at most 2000 characters")
	}
	sub, err := optUUID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	inv, err := optUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	at := micro(s.now())
	if t := req.GetReceiveTime(); t != nil {
		if err := t.CheckValid(); err != nil || t.AsTime().After(at.Add(5*time.Minute)) {
			return nil, domain.Invalid("receive_time", "a valid time that is not in the future")
		}
		at = micro(t.AsTime())
	}
	var out *plansv1.ContractWithdrawal
	err = s.tx(ctx, "withdrawal", func(q *sqlc.Queries) error {
		who, err := s.resolve(ctx, q, req.GetCustomerExternalId(), req.GetCustomerId(), "", "")
		if err != nil {
			return err
		}
		if who.customer == nil {
			return domain.Invalid("customer_external_id", "required")
		}
		c := *who.customer
		w, err := q.InsertContractWithdrawal(ctx, sqlc.InsertContractWithdrawalParams{ID: idgen.NewUUIDv7(), CustomerID: c.ID, SubscriptionID: sub, InvoiceID: inv, Statement: req.GetStatement(),
			Source: source, ReceivedAt: at})
		if err != nil {
			return err
		}
		out = withdrawalOf(w, c.ExternalID)
		raw, err := json.Marshal(map[string]any{"withdrawal_id": w.ID.String(), "customer_id": c.ID.String(), "customer_external_id": c.ExternalID, "subscription_id": out.GetSubscriptionId(),
			"invoice_id": out.GetInvoiceId(), "source": source, "received_at": at.Format(time.RFC3339)})
		if err != nil {
			return err
		}
		if _, err = webhookscmd.Emit(ctx, q, w.ID, webhooksmodel.ContractWithdrawalRequested, raw, at); err != nil {
			return err
		}
		if source != "hosted" || c.Email == "" {
			return nil
		}
		return s.acknowledge(ctx, q, c, w)
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateContractWithdrawalResponse{Withdrawal: out}, nil
}

func (s *Service) ListContractWithdrawals(ctx context.Context, req *plansv1.ListContractWithdrawalsRequest) (*plansv1.ListContractWithdrawalsResponse, error) {
	if st := req.GetStatus(); st != "" && !slices.Contains([]string{"received", "accepted", "rejected", "refunded"}, st) {
		return nil, domain.Invalid("status", "received, accepted, rejected or refunded")
	}
	var out []*plansv1.ContractWithdrawal
	err := s.tx(ctx, "withdrawal", func(q *sqlc.Queries) error {
		p := sqlc.ListContractWithdrawalsParams{Status: req.GetStatus()}
		if req.GetCustomerExternalId() != "" {
			c, err := q.GetCustomerByExternalID(ctx, req.GetCustomerExternalId())
			if err != nil {
				return convert.Err(err, "customer")
			}
			p.CustomerID = c.ID
		}
		rows, err := q.ListContractWithdrawals(ctx, p)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, withdrawalOf(sqlc.ContractWithdrawal{TenantID: r.TenantID, ID: r.ID, CustomerID: r.CustomerID, SubscriptionID: r.SubscriptionID, InvoiceID: r.InvoiceID,
				Statement: r.Statement, Source: r.Source, Status: r.Status, ReceivedAt: r.ReceivedAt, DecidedAt: r.DecidedAt, DecidedBy: r.DecidedBy, DecisionNote: r.DecisionNote,
				CreditNoteID: r.CreditNoteID, RefundState: r.RefundState, RefundNote: r.RefundNote, RefundAt: r.RefundAt}, r.CustomerExternalID))
		}
		return nil
	})
	return &plansv1.ListContractWithdrawalsResponse{Withdrawals: out}, err
}

// withdrawalMoves are the decisions that can follow a status: received is decided once (accepted or rejected); an accepted one is later marked refunded.
var withdrawalMoves = map[string][]string{"received": {"accepted", "rejected"}, "accepted": {"refunded"}}

func (s *Service) SetContractWithdrawalStatus(ctx context.Context, req *plansv1.SetContractWithdrawalStatusRequest) (*plansv1.SetContractWithdrawalStatusResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, withdrawalNotFound()
	}
	if len(req.GetNote()) > 1000 {
		return nil, domain.Invalid("note", "at most 1000 characters")
	}
	var out *plansv1.ContractWithdrawal
	err = s.tx(ctx, "withdrawal", func(q *sqlc.Queries) error {
		w, err := q.GetContractWithdrawalForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return withdrawalNotFound()
		}
		if err != nil {
			return err
		}
		if !slices.Contains(withdrawalMoves[w.Status], req.GetStatus()) {
			return errkey.Newf(codes.FailedPrecondition, "plans.contract.withdrawal_decided", "a withdrawal that is %s cannot become %q", w.Status, req.GetStatus())
		}
		decided, err := q.DecideContractWithdrawal(ctx, sqlc.DecideContractWithdrawalParams{ID: id, Status: req.GetStatus(), DecidedAt: ptr(micro(s.now())), DecidedBy: truncate(req.GetDecidedBy(), 200),
			DecisionNote: req.GetNote()})
		if err != nil {
			return err
		}
		c, err := q.GetCustomerByID(ctx, decided.CustomerID)
		if err != nil {
			return err
		}
		out = withdrawalOf(decided, c.ExternalID)
		raw, err := json.Marshal(map[string]any{"withdrawal_id": decided.ID.String(), "customer_id": c.ID.String(), "customer_external_id": c.ExternalID, "subscription_id": out.GetSubscriptionId(),
			"invoice_id": out.GetInvoiceId(), "status": decided.Status, "decided_by": decided.DecidedBy, "decided_at": decided.DecidedAt.UTC().Format(time.RFC3339), "note": decided.DecisionNote})
		if err != nil {
			return err
		}
		_, err = webhookscmd.Emit(ctx, q, idgen.NewUUIDv7(), webhooksmodel.ContractWithdrawalDecided, raw, *decided.DecidedAt)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.SetContractWithdrawalStatusResponse{Withdrawal: out}, nil
}

// acknowledge queues the fact the notifier turns into the acknowledgement of receipt of a withdrawal made on a hosted page: a durable medium, without delay (Directive
// 2011/83/EU art. 11a(3), Mesafeli Sözleşmeler Yönetmeliği art. 11(2)). It is written in the transaction that records the withdrawal.
func (s *Service) acknowledge(ctx context.Context, q *sqlc.Queries, c sqlc.Customer, w sqlc.ContractWithdrawal) error {
	ti, err := tenantOf(ctx)
	if err != nil {
		return err
	}
	ev := &eventsv1.ContractWithdrawalReceived{EventId: w.ID.String(), TenantId: ti.String(), CustomerId: c.ID.String(), Statement: w.Statement, ReceivedAt: stamp(w.ReceivedAt)}
	if w.InvoiceID != nil {
		ev.InvoiceId = w.InvoiceID.String()
	}
	payload, err := proto.Marshal(ev)
	if err != nil {
		return err
	}
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: WithdrawalReceivedTopic, PartitionKey: ti.String() + "/" + c.ID.String(), Payload: payload}); err != nil {
		return err
	}
	return q.NotifyOutbox(ctx)
}

func refundNotPossible(why string) error {
	return errkey.New(codes.FailedPrecondition, "plans.contract.refund_not_possible", why)
}

// ClaimContractWithdrawalRefund gives the caller the right to ask billing for the refund of an accepted withdrawal: one caller at a time, and never twice for one withdrawal. A
// claim that stood for refundStale without an answer is taken over (reclaimed): its owner died, and the caller must look for a credit note it may have made.
func (s *Service) ClaimContractWithdrawalRefund(ctx context.Context, req *plansv1.ClaimContractWithdrawalRefundRequest) (*plansv1.ClaimContractWithdrawalRefundResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, withdrawalNotFound()
	}
	out := &plansv1.ClaimContractWithdrawalRefundResponse{}
	err = s.tx(ctx, "withdrawal", func(q *sqlc.Queries) error {
		w, err := q.GetContractWithdrawalForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return withdrawalNotFound()
		}
		if err != nil {
			return err
		}
		now := micro(s.now())
		switch {
		case w.Status != "accepted":
			return refundNotPossible("only an accepted withdrawal is refunded")
		case w.InvoiceID == nil:
			return refundNotPossible("the withdrawal names no invoice: refund it by hand")
		case w.RefundState == "requested" || w.RefundState == "outside" || w.RefundState == "refunded":
			return refundNotPossible("a refund was asked for already")
		case w.RefundState == "refund_failed":
			return refundNotPossible("the credit note was made and the provider did not return the money: retry the credit note's refund, a second credit note would credit the invoice twice")
		case w.RefundState == "requesting" && w.RefundAt != nil && now.Sub(*w.RefundAt) < refundStale:
			return refundNotPossible("a refund is being asked for")
		}
		out.Reclaimed = w.RefundState == "requesting"
		row, err := q.SetContractWithdrawalRefund(ctx, sqlc.SetContractWithdrawalRefundParams{ID: id, State: "requesting", Now: &now})
		if err != nil {
			return err
		}
		c, err := q.GetCustomerByID(ctx, row.CustomerID)
		if err != nil {
			return err
		}
		out.Withdrawal = withdrawalOf(row, c.ExternalID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RecordContractWithdrawalRefund writes what billing answered to the claim: requested (a credit note exists), outside (the money cannot go back through Payrudder), failed (ask
// again) or none (there was nothing to refund).
func (s *Service) RecordContractWithdrawalRefund(ctx context.Context, req *plansv1.RecordContractWithdrawalRefundRequest) (*plansv1.RecordContractWithdrawalRefundResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, withdrawalNotFound()
	}
	if !slices.Contains([]string{"none", "requested", "outside", "failed"}, req.GetState()) {
		return nil, domain.Invalid("state", "none, requested, outside or failed")
	}
	cn, err := optUUID("credit_note_id", req.GetCreditNoteId())
	if err != nil {
		return nil, err
	}
	if (req.GetState() == "requested") != (cn != nil) {
		return nil, domain.Invalid("credit_note_id", "named when the refund is requested, and only then")
	}
	var out *plansv1.ContractWithdrawal
	err = s.tx(ctx, "withdrawal", func(q *sqlc.Queries) error {
		w, err := q.GetContractWithdrawalForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return withdrawalNotFound()
		}
		if err != nil {
			return err
		}
		if w.RefundState != "requesting" {
			return refundNotPossible("no refund request is open")
		}
		now := micro(s.now())
		row, err := q.SetContractWithdrawalRefund(ctx, sqlc.SetContractWithdrawalRefundParams{ID: id, State: req.GetState(), CreditNoteID: cn, Note: truncate(req.GetNote(), 1000), Now: &now})
		if err != nil {
			return err
		}
		c, err := q.GetCustomerByID(ctx, row.CustomerID)
		if err != nil {
			return err
		}
		out = withdrawalOf(row, c.ExternalID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &plansv1.RecordContractWithdrawalRefundResponse{Withdrawal: out}, nil
}

func ptr[T any](v T) *T { return &v }

func withdrawalNotFound() error {
	return errkey.New(codes.NotFound, "plans.contract.withdrawal_not_found", "no such withdrawal")
}
