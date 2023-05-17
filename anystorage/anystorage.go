package anystorage

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrExpired = errors.New("expired")
var ErrNotFound = errors.New("not found")
var ErrAlreadyHave = errors.New("already have func with that id")

type Storage[Tk comparable, Tv any] struct {
	strg map[Tk]item[Tv]
	mux  sync.Mutex
}

type item[Tv any] struct {
	subj    Tv
	expires int64
}

// on zero item_annihilation_check_ticktime no expiration checker will be inited
func NewStorage[Tkey comparable, Tvalue any](ctx context.Context, item_annihilation_check_ticktime time.Duration) *Storage[Tkey, Tvalue] {
	s := &Storage[Tkey, Tvalue]{strg: make(map[Tkey]item[Tvalue])}
	if item_annihilation_check_ticktime != 0 {
		// annihilator:
		go func() {
			ticker := time.NewTicker(item_annihilation_check_ticktime)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.mux.Lock()
					t := time.Now().Unix()
					for k, itm := range s.strg { // TODO: потенциально долгий гемор из-за мьютекса
						if itm.expires < t && itm.expires != 0 {
							delete(s.strg, k)
						}
					}
					s.mux.Unlock()
				}
			}
		}()
	}

	return s
}

// expires must be unix seconds, value won't be deleted on expiration when expires is zero
func (s *Storage[Tk, Tv]) Add(key Tk, value Tv, expires int64) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	if _, ok := s.strg[key]; ok {
		return ErrAlreadyHave
	}
	s.strg[key] = item[Tv]{subj: value, expires: expires}
	return nil
}

func (s *Storage[Tk, _]) Remove(key Tk) {
	s.mux.Lock()
	defer s.mux.Unlock()
	delete(s.strg, key)
}

// deletes func from storage on found
func (s *Storage[Tk, Tv]) Get(key Tk) (Tv, error) {
	s.mux.Lock()
	defer s.mux.Unlock()
	if itm, ok := s.strg[key]; ok {
		delete(s.strg, key)
		if (itm.expires > time.Now().Unix() && itm.expires != 0) || itm.expires == 0 {
			return itm.subj, nil
		}
		return *new(Tv), ErrExpired
	} else {
		return *new(Tv), ErrNotFound
	}
}

// don't deletes func from storage on found, but still deletes on expiration
func (s *Storage[Tk, Tv]) GetWithoutRemoving(key Tk) (Tv, error) {
	s.mux.Lock()
	defer s.mux.Unlock()
	if itm, ok := s.strg[key]; ok {
		if (itm.expires > time.Now().Unix() && itm.expires != 0) || itm.expires == 0 {
			return itm.subj, nil
		}
		delete(s.strg, key)
		return *new(Tv), ErrExpired
	} else {
		return *new(Tv), ErrNotFound
	}
}
