package negcache

import (
	"strconv"
	"testing"
	"time"
)

func TestCacheExpiresAndStaysBounded(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	c := New(30*time.Second, 3)
	c.SetClock(func() time.Time { return now })
	c.Add("a")
	if !c.Has("a") || c.Has("b") {
		t.Fatal("an added credential must be found, another must not")
	}
	now = now.Add(29 * time.Second)
	if !c.Has("a") {
		t.Error("forgotten before its time")
	}
	now = now.Add(2 * time.Second)
	if c.Has("a") {
		t.Error("believed after its time")
	}
	if c.Len() != 0 {
		t.Errorf("an expired entry stays in memory after it was looked at: %d", c.Len())
	}
	// The least recently used is the one that goes.
	for _, k := range []string{"1", "2", "3"} {
		c.Add(k)
	}
	c.Has("1") // 1 is now the most recent
	c.Add("4")
	if c.Len() != 3 || !c.Has("1") || c.Has("2") || !c.Has("3") || !c.Has("4") {
		t.Errorf("after the fourth: len %d, 1=%v 2=%v 3=%v 4=%v", c.Len(), c.Has("1"), c.Has("2"), c.Has("3"), c.Has("4"))
	}
	for i := range 1000 {
		c.Add(strconv.Itoa(i))
	}
	if c.Len() > 3 {
		t.Errorf("len = %d", c.Len())
	}
}
