package domain

import (
	"testing"
	"time"
)

func TestNoticeSendAt(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, c := range []struct {
		name       string
		start      time.Time
		send, appl int
		want       time.Time
		held       bool
	}{
		{"none: at once", now.Add(200 * day), 0, 30, now, false},
		{"far future: held until send days before", now.Add(200 * day), 40, 30, now.Add(160 * day), true},
		{"near: the moment is past, so at once", now.Add(35 * day), 40, 30, now, false},
		{"exactly now: at once", now.Add(40 * day), 40, 30, now, false},
		{"at the floor", now.Add(100 * day), 30, 30, now.Add(70 * day), true},
		{"below the applicable notice clamps to it", now.Add(100 * day), 20, 60, now.Add(40 * day), true},
		{"yearly: eleven months ahead", now.Add(335 * day), 30, 30, now.Add(305 * day), true},
	} {
		got, held := NoticeSendAt(now, c.start, c.send, c.appl)
		if !got.Equal(c.want) || held != c.held {
			t.Errorf("%s: got %v held=%v, want %v held=%v", c.name, got, held, c.want, c.held)
		}
		if held && c.start.Sub(got) < time.Duration(c.appl)*day {
			t.Errorf("%s: told later than the notice", c.name)
		}
	}
}

func TestValidateNoticeSendDays(t *testing.T) {
	for _, c := range []struct {
		send, notice int
		ok           bool
	}{{0, 30, true}, {30, 30, true}, {365, 30, true}, {29, 30, false}, {366, 30, false}, {-1, 30, false}} {
		if err := ValidateNoticeSendDays(c.send, c.notice); (err == nil) != c.ok {
			t.Errorf("%d/%d: %v", c.send, c.notice, err)
		}
	}
}
