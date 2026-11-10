package notifications

import (
	"errors"
	"github.com/google/uuid"
	"sync"
)

type hub struct {
	mu    sync.Mutex
	users map[uuid.UUID]map[chan struct{}]struct{}
	total int
}

func newHub() *hub { return &hub{users: make(map[uuid.UUID]map[chan struct{}]struct{})} }
func (h *hub) subscribe(user uuid.UUID) (chan struct{}, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= 5000 || len(h.users[user]) >= 2 {
		return nil, nil, errors.New("notification stream capacity reached")
	}
	wake := make(chan struct{}, 1)
	if h.users[user] == nil {
		h.users[user] = make(map[chan struct{}]struct{})
	}
	h.users[user][wake] = struct{}{}
	h.total++
	return wake, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.users[user], wake)
		if len(h.users[user]) == 0 {
			delete(h.users, user)
		}
		h.total--
	}, nil
}
func (h *hub) signal(payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	notify := func(channels map[chan struct{}]struct{}) {
		for wake := range channels {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
	if payload == "" {
		for _, channels := range h.users {
			notify(channels)
		}
		return
	}
	id, err := uuid.Parse(payload)
	if err == nil {
		notify(h.users[id])
	}
}
