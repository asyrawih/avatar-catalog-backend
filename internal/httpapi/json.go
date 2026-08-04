package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
)

// maxBodyBytes membatasi ukuran body yang mau dibaca. Batch terbesar (100 id)
// jauh di bawah angka ini.
const maxBodyBytes = 1 << 20

// errorEnvelope adalah bentuk seragam untuk semua kegagalan.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details []any  `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("gagal menulis respons", "err", err)
	}
}

// writeError menulis err sebagai amplop error. Error yang bukan *apierr.Error
// dianggap bug internal dan tidak pernah bocor ke klien.
func writeError(w http.ResponseWriter, err error) {
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		slog.Error("error tak tertangani", "err", err)
		apiErr = apierr.New(http.StatusInternalServerError, "internal_error", "Terjadi kesalahan di sisi server")
	}

	if apiErr.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(apiErr.RetryAfter))
	}
	writeJSON(w, apiErr.Status, errorEnvelope{Error: errorBody{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Details: apiErr.Details,
	}})
}

// decodeJSON membaca body JSON ke dst. Field tak dikenal ditolak supaya salah
// ketik di sisi klien ketahuan sejak awal, bukan diam-diam terabaikan.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, apierr.TooLarge("body_too_large", "Body permintaan terlalu besar"))
			return false
		}
		writeError(w, apierr.BadRequest("invalid_json", "Body bukan JSON yang valid: "+err.Error()))
		return false
	}
	return true
}

// queryInt membaca parameter query bertipe angka.
func queryInt(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apierr.BadRequest("invalid_query", "Parameter "+key+" harus berupa angka")
	}
	return n, nil
}

// queryInt64 membaca parameter query bertipe angka 64-bit, mis. userId.
func queryInt64(r *http.Request, key string) (int64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, apierr.BadRequest("invalid_query", "Parameter "+key+" harus berupa angka")
	}
	return n, nil
}

// queryBool membaca parameter query tri-state: nil berarti tidak dikirim.
func queryBool(r *http.Request, key string) (*bool, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil, nil
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, apierr.BadRequest("invalid_query", "Parameter "+key+" harus true atau false")
	}
	return &parsed, nil
}

// jsonID adalah id yang boleh dikirim sebagai string JSON maupun angka.
//
// templateId adalah Roblox asset id, dan klien Luau yang menyimpannya sebagai
// number akan mengirim angka telanjang lewat JSONEncode. Menerima keduanya
// mencegah kegagalan decode yang menyamar sebagai "body tidak valid".
type jsonID string

// UnmarshalJSON menerima "123", 123, dan null.
func (id *jsonID) UnmarshalJSON(raw []byte) error {
	text := string(raw)
	if text == "null" {
		*id = ""
		return nil
	}

	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		*id = jsonID(s)
		return nil
	}

	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return errors.New("harus berupa string atau angka")
	}
	*id = jsonID(n.String())
	return nil
}

// String mengembalikan id sebagai teks.
func (id jsonID) String() string { return string(id) }

// nullableString membantu membedakan "tidak dikirim" dari "dikosongkan" saat
// menulis respons.
func nullableString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
