package idempotency_test

import (
	"testing"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/idempotency"
)

func TestSimpanLaluBaca(t *testing.T) {
	store := idempotency.NewMemoryStore(time.Hour)
	store.Put("outfits.create", "k1", idempotency.Record{Status: 201, Body: []byte(`{"a":1}`)})

	rec, ok := store.Get("outfits.create", "k1")
	if !ok {
		t.Fatal("rekaman tidak ditemukan")
	}
	if rec.Status != 201 || string(rec.Body) != `{"a":1}` {
		t.Errorf("rekaman = %+v", rec)
	}
}

// Scope memisahkan kunci: dua endpoint boleh memakai Idempotency-Key yang sama
// tanpa saling menimpa.
func TestScopeMemisahkanKunci(t *testing.T) {
	store := idempotency.NewMemoryStore(time.Hour)
	store.Put("a", "sama", idempotency.Record{Status: 201})

	if _, ok := store.Get("b", "sama"); ok {
		t.Error("kunci dari scope lain ikut terbaca")
	}
}

func TestRekamanKedaluwarsaTidakTerbaca(t *testing.T) {
	store := idempotency.NewMemoryStore(20 * time.Millisecond)
	store.Put("s", "k", idempotency.Record{Status: 201})

	time.Sleep(30 * time.Millisecond)

	if _, ok := store.Get("s", "k"); ok {
		t.Error("rekaman kedaluwarsa masih terbaca")
	}
}
