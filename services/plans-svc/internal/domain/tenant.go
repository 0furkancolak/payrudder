package domain

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/money"
)

// MaxGrace is the largest grace period a tenant may choose.
const MaxGrace = 7 * 24 * time.Hour

// Tier names as stored.
const (
	TierFree       = "free"
	TierStandard   = "standard"
	TierEnterprise = "enterprise"
)

// TierToDB maps the API enum to the stored name.
func TierToDB(t plansv1.TenantTier) (string, bool) {
	switch t {
	case plansv1.TenantTier_TENANT_TIER_FREE:
		return TierFree, true
	case plansv1.TenantTier_TENANT_TIER_STANDARD:
		return TierStandard, true
	case plansv1.TenantTier_TENANT_TIER_ENTERPRISE:
		return TierEnterprise, true
	default:
		return "", false
	}
}

// TierFromDB maps a stored name to the API enum.
func TierFromDB(s string) plansv1.TenantTier {
	switch s {
	case TierFree:
		return plansv1.TenantTier_TENANT_TIER_FREE
	case TierStandard:
		return plansv1.TenantTier_TENANT_TIER_STANDARD
	case TierEnterprise:
		return plansv1.TenantTier_TENANT_TIER_ENTERPRISE
	default:
		return plansv1.TenantTier_TENANT_TIER_UNSPECIFIED
	}
}

// EnvironmentFromDB maps "sandbox"/"live" to the API enum.
func EnvironmentFromDB(s string) plansv1.Environment {
	switch s {
	case "sandbox":
		return plansv1.Environment_ENVIRONMENT_SANDBOX
	case "live":
		return plansv1.Environment_ENVIRONMENT_LIVE
	default:
		return plansv1.Environment_ENVIRONMENT_UNSPECIFIED
	}
}

// Quotas per tier (docs/06-multi-tenancy.md#quotas-and-fairness). 0 = unlimited.
type Quotas struct {
	MaxCustomers  int64
	MaxMeterCodes int64
}

// QuotasFor returns the quotas of a stored tier name.
func QuotasFor(tier string) Quotas {
	switch tier {
	case TierStandard:
		return Quotas{MaxCustomers: 100_000, MaxMeterCodes: 100}
	case TierEnterprise:
		return Quotas{MaxCustomers: 0, MaxMeterCodes: 500}
	default:
		return Quotas{MaxCustomers: 1_000, MaxMeterCodes: 10}
	}
}

// AccountSettings are the validated CreateAccount inputs.
type AccountSettings struct {
	Name     string
	Currency string
	Timezone string
	Grace    time.Duration
	Tier     string
}

// ValidateCreateAccount checks that currency, time zone and grace were chosen explicitly (no defaults, ADR 0011).
func ValidateCreateAccount(req *plansv1.CreateAccountRequest) (AccountSettings, error) {
	var v Violations
	s := AccountSettings{Name: strings.TrimSpace(req.GetName()), Currency: req.GetCurrency(), Timezone: req.GetTimezone()}
	if s.Name == "" || len(s.Name) > 200 {
		v.Add("name", "required, at most 200 characters")
	}
	if s.Currency == "" {
		v.Add("currency", "required: an active ISO 4217 code such as USD, EUR or TRY")
	} else if _, ok := money.LookupCurrency(s.Currency); !ok {
		v.Add("currency", "unsupported: use an active ISO 4217 code such as USD, EUR or TRY")
	}
	if err := ValidateTimezone(s.Timezone); err != nil {
		v.Add("timezone", err.Error())
	}
	if req.GetGracePeriod() == nil {
		v.Add("grace_period", "required, between 0s and 168h")
	} else if g, err := ValidateGrace(req.GetGracePeriod().AsDuration()); err != nil {
		v.Add("grace_period", err.Error())
	} else {
		s.Grace = g
	}
	tier := req.GetTier()
	if tier == plansv1.TenantTier_TENANT_TIER_UNSPECIFIED {
		tier = plansv1.TenantTier_TENANT_TIER_FREE
	}
	if name, ok := TierToDB(tier); ok {
		s.Tier = name
	} else {
		v.Add("tier", "unknown tier")
	}
	return s, v.Err()
}

// ValidateTimezone requires a loadable IANA name. "Local" and empty are rejected; "UTC" is allowed.
func ValidateTimezone(tz string) error {
	if tz == "" {
		return fmt.Errorf("required: an IANA time zone such as Europe/Istanbul or UTC")
	}
	if tz == "Local" {
		return fmt.Errorf("must be an IANA time zone name")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("unknown IANA time zone %q", tz)
	}
	return nil
}

// ValidateGrace checks 0 ≤ grace ≤ 7 days with whole-second precision.
func ValidateGrace(d time.Duration) (time.Duration, error) {
	if d < 0 || d > MaxGrace {
		return 0, fmt.Errorf("must be between 0s and 168h")
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("must be whole seconds")
	}
	return d, nil
}

// SandboxPaymentMethods are the mock-psp scenario tokens (docs/03-services/workers.md#payments-worker).
var SandboxPaymentMethods = []string{
	"pm_sandbox_success",
	"pm_sandbox_declined",
	"pm_sandbox_insufficient_funds_once",
	"pm_sandbox_error_then_success",
	"pm_sandbox_timeout_committed",
	"pm_sandbox_timeout_not_committed",
}

// ValidatePaymentMethod enforces scenario tokens in sandbox and keeps them out of live. Empty is allowed.
func ValidatePaymentMethod(environment, pm string) error {
	if pm == "" {
		return nil
	}
	isScenario := strings.HasPrefix(pm, "pm_sandbox_")
	switch environment {
	case "sandbox":
		for _, s := range SandboxPaymentMethods {
			if pm == s {
				return nil
			}
		}
		return fmt.Errorf("sandbox tenants accept only %s", strings.Join(SandboxPaymentMethods, ", "))
	default:
		if isScenario {
			return fmt.Errorf("pm_sandbox_* tokens are only valid in sandbox")
		}
		if len(pm) > 255 {
			return fmt.Errorf("at most 255 characters")
		}
		return nil
	}
}

// ValidateCustomer checks CreateCustomer fields.
func ValidateCustomer(environment string, req *plansv1.CreateCustomerRequest) error {
	var v Violations
	if n := len(req.GetExternalId()); n == 0 || n > 128 {
		v.Add("external_id", "required, 1..128 characters")
	}
	if strings.TrimSpace(req.GetName()) == "" || len(req.GetName()) > 200 {
		v.Add("name", "required, at most 200 characters")
	}
	if addr, err := mail.ParseAddress(req.GetEmail()); err != nil || addr.Address != req.GetEmail() {
		v.Add("email", "must be a plain e-mail address")
	}
	if err := ValidatePaymentMethod(environment, req.GetPaymentMethod()); err != nil {
		v.Add("payment_method", err.Error())
	}
	if c := req.GetCountry(); c != "" && !countryRe.MatchString(c) {
		v.Add("country", "must be an ISO 3166-1 alpha-2 code in capitals, e.g. TR")
	}
	v.Address("address", req.GetAddress())
	if c, ac := req.GetCountry(), req.GetAddress().GetCountry(); c != "" && ac != "" && c != ac {
		v.Add("address.country", "must equal country when both are given")
	}
	if c := req.GetBillingCurrency(); c != "" {
		if _, ok := money.LookupCurrency(c); !ok {
			v.Add("billing_currency", "unsupported currency: use an active ISO 4217 code such as USD, EUR or TRY")
		}
	}
	return v.Err()
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
