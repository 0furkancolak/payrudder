package payrudder

import (
	"errors"
	"iter"
)

// errStuckPagination stops an iterator whose server keeps answering the page token it was given.
var errStuckPagination = errors.New("payrudder: next_page_token did not advance")

// paginate turns a page fetcher into an iterator over all items. The token of the first call is empty; the walk ends
// when a page carries no next_page_token. An error is yielded once and ends the walk.
func paginate[T any](fetch func(token string) (items []T, next string, err error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		token := ""
		for {
			items, next, err := fetch(token)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, it := range items {
				if !yield(it, nil) {
					return
				}
			}
			if next == "" {
				return
			}
			if next == token {
				var zero T
				yield(zero, errStuckPagination)
				return
			}
			token = next
		}
	}
}
