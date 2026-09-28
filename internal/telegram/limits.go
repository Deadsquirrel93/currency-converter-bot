package telegram

import (
	"sync"
	"time"
)

const (
	// updatesPerMinute limits how many updates one user can make the bot
	// process; every /rate may hit the Bank of Russia, so flooding must not
	// turn into a flood of upstream requests.
	updatesPerMinute = 30
	// blockedNoticeInterval limits replies (and log lines) for users outside
	// the whitelist, so spamming the bot cannot fill the chat or the disk.
	blockedNoticeInterval = 10 * time.Minute
	// maxTrackedUsers bounds limiter memory; expired windows are swept first.
	maxTrackedUsers = 10000
)

// userLimiter allows each user a fixed number of events per window.
type userLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	users  map[int64]*userWindow
}

type userWindow struct {
	start  time.Time
	count  int
	warned bool
}

func newUserLimiter(limit int, window time.Duration) *userLimiter {
	return &userLimiter{limit: limit, window: window, users: map[int64]*userWindow{}}
}

// allow reports whether the event may proceed and, if not, whether this is
// the first rejection in the current window (so the user can be told once).
func (l *userLimiter) allow(userID int64, now time.Time) (allowed, firstRejection bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.users[userID]
	if w == nil || now.Sub(w.start) >= l.window {
		if w == nil && len(l.users) >= maxTrackedUsers {
			l.sweep(now)
		}
		w = &userWindow{start: now}
		l.users[userID] = w
	}
	w.count++
	if w.count <= l.limit {
		return true, false
	}
	first := !w.warned
	w.warned = true
	return false, first
}

func (l *userLimiter) sweep(now time.Time) {
	for userID, w := range l.users {
		if now.Sub(w.start) >= l.window {
			delete(l.users, userID)
		}
	}
	if len(l.users) < maxTrackedUsers {
		return
	}
	// Still full of active windows: start over rather than grow unbounded.
	clear(l.users)
}
