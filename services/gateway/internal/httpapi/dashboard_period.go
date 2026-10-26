package httpapi

import (
	"math"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

type dashPeriod struct {
	Preset           string
	From, To         time.Time // the first and the last day, midnights in the tenant's zone
	Days             int
	Granularity      string
	Compare          bool
	PrevFrom, PrevTo time.Time
}

func (p dashPeriod) rangeJSON() dashRange {
	r := dashRange{Period: p.Preset, From: dayStr(p.From), To: dayStr(p.To), Days: p.Days, Granularity: p.Granularity, Compare: p.Compare}
	if p.Compare {
		r.PrevFrom, r.PrevTo = dayStr(p.PrevFrom), dayStr(p.PrevTo)
	}
	return r
}

func dayStr(t time.Time) string { return t.Format(time.DateOnly) }

// parseDashPeriod reads ?period=7d|30d|90d|ytd|custom (default 30d), ?from=&to= for custom, and ?compare=0 to switch the comparison off. Days are the tenant's.
func parseDashPeriod(q url.Values, now time.Time, loc *time.Location) (dashPeriod, error) {
	today := startOfDay(now.In(loc), loc)
	p := dashPeriod{Preset: q.Get("period"), To: today, Compare: q.Get("compare") != "0"}
	if p.Preset == "" {
		p.Preset = "30d"
	}
	switch p.Preset {
	case "7d", "30d", "90d":
		n, _ := strconv.Atoi(p.Preset[:len(p.Preset)-1])
		p.From = today.AddDate(0, 0, -(n - 1))
	case "ytd":
		p.From = time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, loc)
	case "custom":
		from, err1 := time.ParseInLocation(time.DateOnly, q.Get("from"), loc)
		to, err2 := time.ParseInLocation(time.DateOnly, q.Get("to"), loc)
		if err1 != nil || err2 != nil {
			return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.bad_period", "a custom period needs from and to as days like 2026-10-21")
		}
		if to.After(today) {
			to = today
		}
		if from.After(to) {
			return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.bad_period", "from must not be after to")
		}
		p.From, p.To = from, to
	default:
		return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.bad_period", "period is 7d, 30d, 90d, ytd or custom")
	}
	p.Days = int(math.Round(p.To.Sub(p.From).Hours()/24)) + 1
	if p.Days > dashMaxDays {
		return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.range_too_long", "a period is at most two years")
	}
	p.Granularity = "day"
	if p.Days > dashDayThreshold {
		p.Granularity = "month"
	}
	p.PrevTo = p.From.AddDate(0, 0, -1)
	p.PrevFrom = p.PrevTo.AddDate(0, 0, -(p.Days - 1))
	return p, nil
}

// dashBuckets lists the buckets of a series: every day, or the first day of every month the period touches.
func dashBuckets(from, to time.Time, gran string) []string {
	var out []string
	if gran == "month" {
		for d := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location()); !d.After(to); d = d.AddDate(0, 1, 0) {
			out = append(out, dayStr(d))
		}
		return out
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, dayStr(d))
	}
	return out
}
