package domain

import (
	"strings"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
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

// API key scopes as stored.
const (
	ScopeFull   = "full"
	ScopeIngest = "ingest"
	ScopeRead   = "read"
)

// ScopeToDB maps the API enum to the stored name; unset is full.
func ScopeToDB(s plansv1.ApiKey_Scope) (string, bool) {
	switch s {
	case plansv1.ApiKey_SCOPE_UNSPECIFIED, plansv1.ApiKey_SCOPE_FULL:
		return ScopeFull, true
	case plansv1.ApiKey_SCOPE_INGEST:
		return ScopeIngest, true
	case plansv1.ApiKey_SCOPE_READ:
		return ScopeRead, true
	default:
		return "", false
	}
}

// ScopeFromDB maps a stored scope to the API enum.
func ScopeFromDB(s string) plansv1.ApiKey_Scope {
	switch s {
	case ScopeIngest:
		return plansv1.ApiKey_SCOPE_INGEST
	case ScopeRead:
		return plansv1.ApiKey_SCOPE_READ
	default:
		return plansv1.ApiKey_SCOPE_FULL
	}
}

// taxIDCountries are the countries whose invoices need the seller's tax id: Turkey and the EU member states.
var taxIDCountries = map[string]bool{
	"TR": true, "AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true, "EE": true, "FI": true, "FR": true, "DE": true, "GR": true,
	"HU": true, "IE": true, "IT": true, "LV": true, "LT": true, "LU": true, "MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SK": true, "SI": true,
	"ES": true, "SE": true,
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
