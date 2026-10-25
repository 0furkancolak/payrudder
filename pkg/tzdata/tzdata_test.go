package tzdata_test

import (
	"testing"
	"time"

	_ "github.com/0furkancolak/payrudder/pkg/tzdata"
)

func TestZonesLoadWithoutHostDatabase(t *testing.T) {
	t.Setenv("ZONEINFO", "/nonexistent")
	for _, z := range []string{"UTC", "Europe/Istanbul", "Asia/Kolkata", "America/Argentina/Buenos_Aires", "Pacific/Chatham", "Asia/Kathmandu", "Africa/Casablanca", "Australia/Lord_Howe"} {
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("%s: %v", z, err)
		}
	}
	if _, err := time.LoadLocation("Mars/Olympus"); err == nil {
		t.Error("unknown zone accepted")
	}
}
