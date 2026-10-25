package workerx

import "sync"

// KeyedMutex serialises work per key. An entry is counted by the callers that hold or wait for it and leaves the map with the
// last of them, so two callers of one key never end up with two different mutexes. The zero value is ready to use.
type KeyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu   sync.Mutex
	refs int // holders and waiters, guarded by KeyedMutex.mu
}

// Lock blocks until the caller is the only one working on key and returns the function that ends its turn.
func (k *KeyedMutex) Lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.refs++
	k.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
