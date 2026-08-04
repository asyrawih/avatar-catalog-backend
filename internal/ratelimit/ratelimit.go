// Package ratelimit menyediakan pembatas jendela-tetap sederhana.
//
// Dipakai untuk endpoint laporan sync run: worker yang kena rate limit di sisi
// Roblox cenderung retry beruntun, dan tanpa pembatas laporan gagal separuh
// akan membanjiri backend.
package ratelimit

import (
	"sync"
	"time"
)

// FixedWindow membatasi jumlah kejadian per kunci dalam satu jendela waktu.
type FixedWindow struct {
	mu      sync.Mutex
	counts  map[string]window
	limit   int
	windowD time.Duration
	now     func() time.Time
}

type window struct {
	startedAt time.Time
	count     int
}

// NewFixedWindow membuat pembatas dengan limit kejadian per durasi d.
func NewFixedWindow(limit int, d time.Duration) *FixedWindow {
	return &FixedWindow{
		counts:  make(map[string]window),
		limit:   limit,
		windowD: d,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Allow mencatat satu kejadian untuk key. Jika ditolak, nilai kedua adalah
// sisa detik sebelum jendela berikutnya dibuka.
func (f *FixedWindow) Allow(key string) (bool, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	w, ok := f.counts[key]
	if !ok || now.Sub(w.startedAt) >= f.windowD {
		f.counts[key] = window{startedAt: now, count: 1}
		return true, 0
	}

	if w.count >= f.limit {
		retry := max(int((f.windowD - now.Sub(w.startedAt)).Seconds()), 1)
		return false, retry
	}

	w.count++
	f.counts[key] = w
	return true, 0
}
