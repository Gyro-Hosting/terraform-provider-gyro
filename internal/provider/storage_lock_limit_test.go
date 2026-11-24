package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
)

// v0.41.1 (Gyro Public API 2.46.1): the plan warns when days would pass the service end date.
func TestLockLimitWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/cloud/storage/12/object-lock"):
			_, _ = w.Write([]byte(`{"data":{"service_id":12,"max_retention_days":29,"service_end":"2026-12-24T21:22:00+03:00","buckets":[]}}`))
		case strings.HasSuffix(r.URL.Path, "/cloud/storage/13/object-lock"):
			_, _ = w.Write([]byte(`{"data":{"service_id":13,"max_retention_days":3650,"buckets":[]}}`)) // older API: no service_end
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"storage_not_found","message":"not found"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := client.New(srv.URL+"/api/v1/client", "wck_test", "test")
	ctx := context.Background()
	if w := lockLimitWarning(ctx, c, 12, 30); !strings.Contains(w, "at most 29 days") || !strings.Contains(w, "2026-12-24T21:22:00+03:00") || !strings.Contains(w, "triennially") {
		t.Fatalf("warning expected, got %q", w)
	}
	if w := lockLimitWarning(ctx, c, 12, 29); w != "" {
		t.Fatalf("no warning expected at the limit, got %q", w)
	}
	if w := lockLimitWarning(ctx, c, 13, 400); w != "" {
		t.Fatalf("no warning without service_end, got %q", w)
	}
	if w := lockLimitWarning(ctx, c, 99, 400); w != "" {
		t.Fatalf("no warning on API error, got %q", w)
	}
}
