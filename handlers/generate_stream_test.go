package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ayush00git/stanza/models"
)

// An EventSource cannot see a status code: if the server answers a stream request with a
// hang or a bare 500, the browser retries forever and the user watches a spinner. The
// guards must therefore reject before any SSE header is written, with a JSON body the
// client's error handler can read.
func TestGenerateRunStreamHandlerGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/runs/:id/generate/stream", GenerateRunStreamHandler)

	// A run with structures would start a Claude call, so only the rejecting paths are
	// exercised here. The happy path is a two-minute network call and is not unit-tested.
	DefaultRunStore.Put(&models.Run{ID: "run_no_structures", Status: "structure_acquired"})

	cases := []struct {
		name, id string
		want     int
	}{
		{"unknown run", "run_missing", http.StatusNotFound},
		{"no mutant structure yet", "run_no_structures", http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/runs/"+tc.id+"/generate/stream?n=4", nil)
			r.ServeHTTP(w, req)

			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body %q)", w.Code, tc.want, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				t.Errorf("rejected request opened an event stream (Content-Type %q); "+
					"EventSource would retry against it forever", ct)
			}
			if !strings.Contains(w.Body.String(), `"error"`) {
				t.Errorf("body %q carries no error field for the client to surface", w.Body.String())
			}
		})
	}
}
