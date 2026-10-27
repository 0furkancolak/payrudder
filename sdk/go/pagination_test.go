package payrudder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// customerPages serves n customers, size per page, with next_page_token = the index of the next page.
func customerPages(total, size int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := 0
		if tok := r.URL.Query().Get("page_token"); tok != "" {
			start, _ = strconv.Atoi(tok)
		}
		end := min(start+size, total)
		fmt.Fprint(w, `{"customers":[`)
		for i := start; i < end; i++ {
			if i > start {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"external_id":"cus_%d"}`, i)
		}
		fmt.Fprint(w, `]`)
		if end < total {
			fmt.Fprintf(w, `,"next_page_token":"%d"`, end)
		}
		fmt.Fprint(w, `}`)
	}
}

func TestListAllWalksEveryPage(t *testing.T) {
	var pages int
	h := customerPages(25, 10)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		h(w, r)
	}))
	var got []string
	for cust, err := range c.Customers.ListCustomersAll(context.Background(), &ListCustomersParams{PageSize: Ptr(10)}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, cust.ExternalID)
	}
	if len(got) != 25 || got[0] != "cus_0" || got[24] != "cus_24" || pages != 3 {
		t.Fatalf("got %d items in %d pages", len(got), pages)
	}
}

func TestListAllStopsEarly(t *testing.T) {
	var pages int
	h := customerPages(100, 10)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		h(w, r)
	}))
	n := 0
	for _, err := range c.Customers.ListCustomersAll(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		if n == 12 {
			break
		}
	}
	if n != 12 || pages != 2 {
		t.Fatalf("items %d pages %d", n, pages)
	}
}

func TestListAllYieldsTheErrorOnce(t *testing.T) {
	h := customerPages(100, 10)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "10" {
			w.WriteHeader(404)
			return
		}
		h(w, r)
	}))
	var items, errs int
	for _, err := range c.Customers.ListCustomersAll(context.Background(), nil) {
		if err != nil {
			errs++
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("%v", err)
			}
			continue
		}
		items++
	}
	if items != 10 || errs != 1 {
		t.Fatalf("items %d errors %d", items, errs)
	}
}

func TestListAllDoesNotModifyTheCallersParams(t *testing.T) {
	c, _ := newTestClient(t, customerPages(30, 10))
	p := &ListCustomersParams{PageSize: Ptr(10)}
	for _, err := range c.Customers.ListCustomersAll(context.Background(), p) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if p.PageToken != "" {
		t.Fatalf("token %q", p.PageToken)
	}
}

func TestListAllStopsOnAStuckToken(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"customers":[{"external_id":"x"}],"next_page_token":"same"}`)
	}))
	var items int
	var last error
	for _, err := range c.Customers.ListCustomersAll(context.Background(), nil) {
		if err != nil {
			last = err
			break
		}
		items++
	}
	if last == nil || items != 2 {
		t.Fatalf("items %d err %v", items, last)
	}
}

func TestListAllWithAPathParameter(t *testing.T) {
	var path string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		fmt.Fprint(w, `{"entries":[{"id":"e1"}]}`)
	}))
	n := 0
	for _, err := range c.Customers.ListCreditLedgerAll(context.Background(), "cus_1", nil) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 1 || path != "/v1/customers/cus_1/credits/ledger" {
		t.Fatalf("n %d path %s", n, path)
	}
}
