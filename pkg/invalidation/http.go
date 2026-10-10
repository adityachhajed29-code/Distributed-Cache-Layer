package invalidation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/malwaredllc/minicache/pkg/storage"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var keyPattern = regexp.MustCompile(`^[a-z0-9:_-]{1,250}$`)

// Handler serves JSON granules, with base64-encoded binary values.
func (e *Engine) Handler(w http.ResponseWriter, r *http.Request) {
	key := strings.ToLower(strings.TrimPrefix(r.URL.Path, "/v1/cache/"))
	if !keyPattern.MatchString(key) {
		http.Error(w, "invalid key", 400)
		return
	}
	var g Granule
	var err error
	switch r.Method {
	case http.MethodGet:
		g, err = e.Get(r.Context(), key)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 710000)
		var input struct {
			Val   []byte `json:"val"`
			TTLMS int64  `json:"ttl_ms"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&input); err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				http.Error(w, "payload too large", 413)
			} else {
				http.Error(w, "invalid JSON", 400)
			}
			return
		}
		var trailing interface{}
		if decoder.Decode(&trailing) != io.EOF {
			http.Error(w, "expected one JSON object", 400)
			return
		}
		if len(input.Val) > 512*1024 {
			http.Error(w, "payload too large", 413)
			return
		}
		if input.TTLMS < 0 || input.TTLMS > 86400000*365 {
			http.Error(w, "TTL outside 0..365 days", 400)
			return
		}
		g, err = e.Write(r.Context(), storage.Record{Key: key, Val: input.Val}, time.Duration(input.TTLMS)*time.Millisecond)
	case http.MethodDelete:
		g, err = e.Write(r.Context(), storage.Record{Key: key, Deleted: true}, 0)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", 405)
		return
	}
	if err != nil {
		code := 503
		if errors.Is(err, sql.ErrNoRows) {
			code = 404
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet && e.policy == "write-behind" {
		w.WriteHeader(http.StatusAccepted)
	}
	json.NewEncoder(w).Encode(g)
}
