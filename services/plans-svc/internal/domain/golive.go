package domain

import (
	"slices"
	"strings"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/apikey"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// Tenant status names as stored (phase 45).
const (
	StatusInactive  = "inactive"
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// StatusFromDB maps a stored status to the API enum.
func StatusFromDB(s string) plansv1.TenantStatus {
	switch s {
	case StatusInactive:
		return plansv1.TenantStatus_TENANT_STATUS_INACTIVE
	case StatusActive:
		return plansv1.TenantStatus_TENANT_STATUS_ACTIVE
	case StatusSuspended:
		return plansv1.TenantStatus_TENANT_STATUS_SUSPENDED
	default:
		return plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED
	}
}

// StatusToDB maps the API enum to the stored name.
func StatusToDB(s plansv1.TenantStatus) (string, bool) {
	switch s {
	case plansv1.TenantStatus_TENANT_STATUS_INACTIVE:
		return StatusInactive, true
	case plansv1.TenantStatus_TENANT_STATUS_ACTIVE:
		return StatusActive, true
	case plansv1.TenantStatus_TENANT_STATUS_SUSPENDED:
		return StatusSuspended, true
	default:
		return "", false
	}
}

// KeyGrant reads what a request says a new key may do: a role and permissions, or, from an older client, a scope. Neither given is the role admin. ok is
// false for an unknown role or permission, a custom role without permissions, or a permission outside a fixed role.
func KeyGrant(role string, permissions []string, legacy plansv1.ApiKey_Scope) (outRole string, perms []string, ok bool) {
	if role == "" && len(permissions) == 0 {
		switch legacy {
		case plansv1.ApiKey_SCOPE_INGEST:
			role = apikey.RoleIngestOnly
		case plansv1.ApiKey_SCOPE_READ:
			role = apikey.RoleReadOnly
		case plansv1.ApiKey_SCOPE_UNSPECIFIED, plansv1.ApiKey_SCOPE_FULL:
			role = apikey.RoleAdmin
		default:
			return "", nil, false
		}
	}
	return apikey.Resolve(role, permissions)
}

// KeyScope is the summary of a role for readers of the deprecated scope field: ingest for ingest_only, read for read_only, full for the rest.
func KeyScope(role string) plansv1.ApiKey_Scope {
	switch role {
	case apikey.RoleIngestOnly:
		return plansv1.ApiKey_SCOPE_INGEST
	case apikey.RoleReadOnly:
		return plansv1.ApiKey_SCOPE_READ
	}
	return plansv1.ApiKey_SCOPE_FULL
}

// KeyPermissions is the permission set of a stored key: the role admin always holds every permission that exists, so one added later reaches the admin keys
// already issued.
func KeyPermissions(role string, stored []string) []string {
	if role == apikey.RoleAdmin {
		return slices.Clone(apikey.Permissions)
	}
	return stored
}

// taxIDCountries are the countries whose invoices need the seller's tax id: Turkey and the EU member states.
var taxIDCountries = map[string]bool{
	"TR": true, "AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true, "EE": true, "FI": true, "FR": true, "DE": true, "GR": true,
	"HU": true, "IE": true, "IT": true, "LV": true, "LT": true, "LU": true, "MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SK": true, "SI": true,
	"ES": true, "SE": true,
}

// TaxIDRequired reports whether the seller's invoices in a country need the seller's tax id.
func TaxIDRequired(country string) bool {
	return taxIDCountries[strings.ToUpper(strings.TrimSpace(country))]
}

// NormalizeBillingProfile returns the profile as it is stored: parts trimmed, the country in capitals, the tax id without spaces or dashes and in capitals
// (so "de 123 456 789" is the VAT id DE123456789). The inputs are not changed.
func NormalizeBillingProfile(address *plansv1.Address, legal *plansv1.LegalInfo) (*plansv1.Address, *plansv1.LegalInfo) {
	trim := strings.TrimSpace
	a := &plansv1.Address{Line1: trim(address.GetLine1()), Line2: trim(address.GetLine2()), City: trim(address.GetCity()), Region: trim(address.GetRegion()),
		PostalCode: trim(address.GetPostalCode()), Country: strings.ToUpper(trim(address.GetCountry()))}
	taxID := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", ".", "").Replace(trim(legal.GetTaxId())))
	l := &plansv1.LegalInfo{LegalName: trim(legal.GetLegalName()), TaxId: taxID, TaxOffice: trim(legal.GetTaxOffice()), MersisNo: trim(legal.GetMersisNo()),
		TradeRegistryNo: trim(legal.GetTradeRegistryNo())}
	return a, l
}

// ValidateBillingProfile checks a profile an owner entered (already normalised): what the go-live checklist needs, plus the address lines an invoice prints. The
// refusals carry keys the console translates, one per missing part.
func ValidateBillingProfile(address *plansv1.Address, legal *plansv1.LegalInfo) error {
	switch {
	case legal.GetLegalName() == "":
		return errkey.New(codes.InvalidArgument, "identity.billing_profile.legal_name_required", "legal_name is required")
	case address.GetCountry() == "":
		return errkey.New(codes.InvalidArgument, "identity.billing_profile.country_required", "address.country is required")
	case address.GetLine1() == "" || address.GetCity() == "":
		return errkey.New(codes.InvalidArgument, "identity.billing_profile.address_required", "address.line1 and address.city are required")
	case TaxIDRequired(address.GetCountry()) && legal.GetTaxId() == "":
		return errkey.New(codes.InvalidArgument, "identity.billing_profile.tax_id_required", "legal.tax_id is required for invoices in "+address.GetCountry())
	}
	if err := ValidateAddress("address", address); err != nil {
		return err
	}
	return ValidateLegal(legal)
}

// LegalProfileComplete is the go-live rule for the seller's data: a legal name and a country, and a tax id where the country's invoices need one.
func LegalProfileComplete(address *plansv1.Address, legal *plansv1.LegalInfo) bool {
	if strings.TrimSpace(legal.GetLegalName()) == "" {
		return false
	}
	country := strings.ToUpper(strings.TrimSpace(address.GetCountry()))
	if country == "" {
		return false
	}
	return !taxIDCountries[country] || strings.TrimSpace(legal.GetTaxId()) != ""
}
