package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	apiErr := asAPIError(err)

	if apiErr.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(apiErr.RetryAfter))
	}
	writeJSON(w, apiErr.Status, errorEnvelope{Error: *newErrorBody(err)})
}

// newErrorBody menyusun bagian dalam amplop error, tanpa menulisnya. Dipakai
// balasan yang memuat banyak kegagalan sekaligus — mis. hasil per-outfit di
// POST /v1/outfits:batch — supaya bentuk error di sana sama persis dengan
// error tunggal di endpoint lain.
func newErrorBody(err error) *errorBody {
	apiErr := asAPIError(err)
	return &errorBody{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Details: apiErr.Details,
	}
}

// asAPIError menerjemahkan error apa pun menjadi *apierr.Error. Yang bukan
// error API dicatat sebagai bug dan diganti pesan generik — isinya bisa memuat
// detail internal yang tidak boleh sampai ke klien.
func asAPIError(err error) *apierr.Error {
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		slog.Error("error tak tertangani", "err", err)
		return apierr.New(http.StatusInternalServerError, "internal_error", "Terjadi kesalahan di sisi server")
	}
	return apiErr
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

// queryTime membaca parameter query bertipe waktu RFC 3339; kosong berarti
// zero time — pemanggil yang menentukan nilai default-nya.
func queryTime(r *http.Request, key string) (time.Time, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, apierr.BadRequest("invalid_query", "Parameter "+key+" harus berupa waktu RFC 3339")
	}
	return t, nil
}

// queryList membaca parameter query yang boleh diulang maupun dipisah koma,
// mis. ?outfitId=a,b atau ?outfitId=a&outfitId=b. Nilai kosong dibuang supaya
// "a,,b" tidak berubah jadi pencarian id kosong.
func queryList(r *http.Request, key string) []string {
	raw := r.URL.Query()[key]
	if len(raw) == 0 {
		return nil
	}

	out := make([]string, 0, len(raw))
	for _, value := range raw {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
