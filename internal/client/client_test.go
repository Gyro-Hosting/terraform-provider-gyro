package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/client"
	"github.com/Gyro-Hosting/terraform-provider-gyro/internal/mockapi"
)

const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFaA/eWywO0ibNUDeE3EjFbGU0y4og3sMQAiwQYSi6Hv test"

func setup(t *testing.T) (*mockapi.API, *client.Client, *client.Client) {
	t.Helper()
	m := mockapi.New()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	a := client.New(srv.URL+"/api/v1/client", "wck_testA", "test")
	b := client.New(srv.URL+"/api/v1/client", "wck_testB", "test")
	a.Sleep, b.Sleep = func(time.Duration) {}, func(time.Duration) {}
	return m, a, b
}

func TestAuthErrors(t *testing.T) {
	_, a, _ := setup(t)
	a.Token = ""
	_, err := a.Servers(context.Background(), "")
	if !client.HasCode(err, "missing_token") {
		t.Fatalf("want missing_token, got %v", err)
	}
	a.Token = "wck_nope"
	if _, err = a.Servers(context.Background(), ""); !client.HasCode(err, "invalid_token") {
		t.Fatalf("want invalid_token, got %v", err)
	}
}

func TestSSHKeysAndCrossTenant(t *testing.T) {
	_, a, b := setup(t)
	ctx := context.Background()
	k, err := a.CreateSSHKey(ctx, "laptop", pub)
	if err != nil || k.ID == 0 || k.Fingerprint == "" {
		t.Fatalf("create: %v %+v", err, k)
	}
	if _, err := a.CreateSSHKey(ctx, "dup", pub); !client.HasCode(err, "ssh_key_exists") {
		t.Fatalf("want ssh_key_exists, got %v", err)
	}
	if _, err := b.SSHKey(ctx, k.ID); !client.IsNotFound(err) {
		t.Fatalf("B must not see A's key: %v", err)
	}
	if err := b.DeleteSSHKey(ctx, k.ID); !client.IsNotFound(err) {
		t.Fatalf("B must not delete A's key: %v", err)
	}
	keys, _ := b.SSHKeys(ctx)
	if len(keys) != 0 {
		t.Fatalf("B lists %d keys", len(keys))
	}
	if err := a.DeleteSSHKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCreateServerMoneyRules(t *testing.T) {
	m, a, b := setup(t)
	ctx := context.Background()
	q, err := a.CreateServer(ctx, client.CreateServerRequest{ProductID: 723, Image: "debian-12", DryRun: true}, "")
	if err != nil || !q.DryRun || q.Quote == nil || q.Quote.Total.Amount <= 0 {
		t.Fatalf("dry run: %v %+v", err, q)
	}
	if m.Balance[1] != 5000 {
		t.Fatal("dry run charged balance")
	}
	if _, err := a.CreateServer(ctx, client.CreateServerRequest{ProductID: 723, Image: "debian-12"}, "k-1-aaaaaaaa"); !client.HasCode(err, "confirm_required") {
		t.Fatalf("want confirm_required, got %v", err)
	}
	if _, err := a.CreateServer(ctx, client.CreateServerRequest{ProductID: 723, Image: "debian-12", Confirm: true}, ""); !client.HasCode(err, "idempotency_key_required") {
		t.Fatalf("want idempotency_key_required, got %v", err)
	}
	if _, err := b.CreateServer(ctx, client.CreateServerRequest{ProductID: 723, Image: "debian-12", Confirm: true}, client.NewIdempotencyKey()); !client.HasCode(err, "insufficient_balance") {
		t.Fatalf("want insufficient_balance, got %v", err)
	}
	key := client.NewIdempotencyKey()
	r1, err := a.CreateServer(ctx, client.CreateServerRequest{ProductID: 723, Image: "debian-12", Confirm: true}, key)
	if err != nil || r1.Server == nil {
		t.Fatalf("create: %v", err)
	}
	r2, err := a.CreateServer(ctx, client.CreateServerRequest{ProductID: 723, Image: "debian-12", Confirm: true}, key)
	if err != nil || r2.Server.ID != r1.Server.ID {
		t.Fatalf("replay must return the same server: %v", err)
	}
	if list, _ := a.Servers(ctx, ""); len(list) != 1 {
		t.Fatalf("idempotent retry created %d servers", len(list))
	}
	if _, err := a.CreateServer(ctx, client.CreateServerRequest{ProductID: 656, Image: "debian-12", Confirm: true}, key); !client.HasCode(err, "idempotency_key_reuse") {
		t.Fatalf("want idempotency_key_reuse, got %v", err)
	}
	if _, err := b.Server(ctx, r1.Server.ID); !client.IsNotFound(err) {
		t.Fatalf("B must get 404 on A's server: %v", err)
	}
	if _, err := b.Action(ctx, r1.Server.ID, "reboot", nil); !client.IsNotFound(err) {
		t.Fatalf("B must get 404 on A's server action: %v", err)
	}
	if err := b.DeleteServer(ctx, r1.Server.ID); !client.IsNotFound(err) {
		t.Fatalf("B must get 404 on A's server delete: %v", err)
	}
	s, err := a.WaitReady(ctx, r1.Server.ID, "running", time.Millisecond)
	if err != nil || s.Live == nil || s.Live.PowerState != "running" {
		t.Fatalf("wait: %v", err)
	}
}

func TestRetryOn429AndIdempotentRetryOn5xx(t *testing.T) {
	var n int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&n, 1)
		if c == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`))
			return
		}
		if c == 2 {
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"error":{"code":"server_error","message":"x"}}`))
			return
		}
		if r.Method == "POST" && r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("retried POST without idempotency key")
		}
		_, _ = w.Write([]byte(`{"data":{"server":{"id":7,"status":"inprocess"}}}`))
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	c := client.New(srv.URL, "wck_x", "test")
	var slept []time.Duration
	c.Sleep = func(d time.Duration) { slept = append(slept, d) }
	r, err := c.CreateServer(context.Background(), client.CreateServerRequest{ProductID: 1, Image: "x", Confirm: true}, "idem-key-123456")
	if err != nil || r.Server.ID != 7 || n != 3 {
		t.Fatalf("err=%v calls=%d", err, n)
	}
	if len(slept) != 2 || slept[0] != time.Second {
		t.Fatalf("backoff: %v", slept)
	}
}

func TestNoRetryForNonIdempotentWrite(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"error":{"code":"server_error","message":"x"}}`))
	}))
	defer srv.Close()
	c := client.New(srv.URL, "wck_x", "test")
	c.Sleep = func(time.Duration) {}
	if _, err := c.Action(context.Background(), 1, "reboot", nil); err == nil || n != 1 {
		t.Fatalf("non-idempotent POST must not be retried on 5xx (calls=%d)", n)
	}
}
