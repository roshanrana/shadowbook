//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/httpapi"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

var fixedNow = time.Date(2028, time.February, 29, 12, 0, 0, 0, time.UTC)

func newServer(t *testing.T) (*httptest.Server, *store.Store, []uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("SHADOWBOOK_LEDGER_DSN")
	if dsn == "" {
		t.Skip("SHADOWBOOK_LEDGER_DSN unset")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
		t.Fatal(err)
	}

	accounts := []uuid.UUID{
		uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
	for _, id := range accounts {
		if err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: "CHK-01", Currency: "USD",
			OpenedOn: bizdate.Date(2018, time.June, 1),
		}); err != nil {
			t.Fatal(err)
		}
	}

	reg := prometheus.NewRegistry()
	m := obs.NewMetrics(reg)
	srv := httptest.NewServer(httpapi.New(httpapi.Config{
		Store: st, Metrics: m, Registry: reg,
		Checker: obs.NewChecker(st, m, time.Second),
		Clock:   func() time.Time { return fixedNow },
	}).Handler())
	t.Cleanup(srv.Close)
	return srv, st, accounts
}

func postJSON(t *testing.T, srv *httptest.Server, path, key string, body any, headers ...string) (int, map[string]any) {
	t.Helper()
	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Principal", "sim")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func posting(a, b uuid.UUID, amount int64) map[string]any {
	return map[string]any{
		"kind": "transfer", "currency": "USD",
		"business_date": "2028-02-29", "value_date": "2028-02-29",
		"posted_at":           "2028-02-29T16:59:59.999Z",
		"reverses_posting_id": nil,
		"entries": []map[string]any{
			{"account_id": a.String(), "amount_minor": -amount},
			{"account_id": b.String(), "amount_minor": amount},
		},
	}
}

func TestPostPostingCreatesThenReplays(t *testing.T) {
	srv, _, acc := newServer(t)

	status, body := postJSON(t, srv, "/v1/postings", "k1", posting(acc[0], acc[1], 125000))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", body["entries"])
	}

	// A replay is 200, not 201, and returns the same posting.
	status2, body2 := postJSON(t, srv, "/v1/postings", "k1", posting(acc[0], acc[1], 125000))
	if status2 != http.StatusOK {
		t.Fatalf("replay status = %d", status2)
	}
	if body2["posting_id"] != body["posting_id"] {
		t.Fatalf("replay returned a different posting id")
	}
}

// The error taxonomy of LLD §3.4, each case exercised through the wire.
func TestErrorTaxonomy(t *testing.T) {
	srv, _, acc := newServer(t)
	if status, _ := postJSON(t, srv, "/v1/postings", "seed", posting(acc[0], acc[1], 100)); status != http.StatusCreated {
		t.Fatalf("seed posting failed: %d", status)
	}

	for _, tc := range []struct {
		name       string
		key        string
		body       any
		headers    []string
		wantStatus int
		wantCode   string
	}{
		{
			name: "missing idempotency key", key: "", body: posting(acc[0], acc[1], 1),
			wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeMissingIdempotencyKey,
		},
		{
			name: "unknown principal", key: "k", body: posting(acc[0], acc[1], 1),
			headers:    []string{"X-Principal", "intruder"},
			wantStatus: http.StatusForbidden, wantCode: httpapi.CodeUnknownPrincipal,
		},
		{
			name: "idempotency body mismatch", key: "seed", body: posting(acc[0], acc[1], 999),
			wantStatus: http.StatusConflict, wantCode: httpapi.CodeIdempotencyBodyMismatch,
		},
		{
			name: "entries not balanced", key: "nb",
			body: map[string]any{
				"kind": "transfer", "currency": "USD",
				"business_date": "2028-02-29", "value_date": "2028-02-29",
				"posted_at": "2028-02-29T10:00:00Z", "reverses_posting_id": nil,
				"entries": []map[string]any{
					{"account_id": acc[0].String(), "amount_minor": -100},
					{"account_id": acc[1].String(), "amount_minor": 999},
				},
			},
			wantStatus: http.StatusUnprocessableEntity, wantCode: httpapi.CodeEntriesNotBalanced,
		},
		{
			name: "single entry", key: "one",
			body: map[string]any{
				"kind": "transfer", "currency": "USD",
				"business_date": "2028-02-29", "value_date": "2028-02-29",
				"posted_at": "2028-02-29T10:00:00Z", "reverses_posting_id": nil,
				"entries": []map[string]any{{"account_id": acc[0].String(), "amount_minor": 0}},
			},
			wantStatus: http.StatusUnprocessableEntity, wantCode: httpapi.CodeEntriesNotBalanced,
		},
		{
			name: "unknown currency", key: "cur",
			body: map[string]any{
				"kind": "transfer", "currency": "XXX",
				"business_date": "2028-02-29", "value_date": "2028-02-29",
				"posted_at": "2028-02-29T10:00:00Z", "reverses_posting_id": nil,
				"entries": []map[string]any{
					{"account_id": acc[0].String(), "amount_minor": -1},
					{"account_id": acc[1].String(), "amount_minor": 1},
				},
			},
			wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidRequest,
		},
		{
			name: "unknown kind", key: "kind",
			body: map[string]any{
				"kind": "wizardry", "currency": "USD",
				"business_date": "2028-02-29", "value_date": "2028-02-29",
				"posted_at": "2028-02-29T10:00:00Z", "reverses_posting_id": nil,
				"entries": []map[string]any{
					{"account_id": acc[0].String(), "amount_minor": -1},
					{"account_id": acc[1].String(), "amount_minor": 1},
				},
			},
			wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidRequest,
		},
		{
			name: "malformed date", key: "date",
			body: map[string]any{
				"kind": "transfer", "currency": "USD",
				"business_date": "29/02/2028", "value_date": "2028-02-29",
				"posted_at": "2028-02-29T10:00:00Z", "reverses_posting_id": nil,
				"entries": []map[string]any{
					{"account_id": acc[0].String(), "amount_minor": -1},
					{"account_id": acc[1].String(), "amount_minor": 1},
				},
			},
			wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := postJSON(t, srv, "/v1/postings", tc.key, tc.body, tc.headers...)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %v)", status, tc.wantStatus, body)
			}
			errObj, _ := body["error"].(map[string]any)
			if errObj["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", errObj["code"], tc.wantCode)
			}
			if errObj["request_id"] == "" {
				t.Fatal("no request id on an error response")
			}
		})
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	srv, _, acc := newServer(t)
	body := posting(acc[0], acc[1], 1)
	body["surprise"] = true
	status, _ := postJSON(t, srv, "/v1/postings", "k", body)
	if status != http.StatusBadRequest {
		t.Fatalf("an unknown field was accepted: %d", status)
	}
}

func TestBalancesAndHolds(t *testing.T) {
	srv, _, acc := newServer(t)
	if status, _ := postJSON(t, srv, "/v1/postings", "b1", posting(acc[1], acc[0], 500000)); status != http.StatusCreated {
		t.Fatal("seed posting failed")
	}

	get := func(path string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("X-Principal", "sim")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	status, bal := get(fmt.Sprintf("/v1/accounts/%s/balances?as_of=2028-02-29", acc[0]))
	if status != http.StatusOK {
		t.Fatalf("balances status = %d", status)
	}
	if bal["ledger_minor"].(float64) != 500000 {
		t.Fatalf("ledger = %v, want 500000", bal["ledger_minor"])
	}
	if bal["available_minor"] != bal["ledger_minor"] {
		t.Fatal("available differs from ledger with no holds outstanding")
	}
	if bal["scale"].(float64) != 2 {
		t.Fatalf("scale = %v, want 2", bal["scale"])
	}

	// Place a hold: available drops, ledger does not. That distinction is what
	// Q7 diverges on.
	status, hold := postJSON(t, srv, fmt.Sprintf("/v1/accounts/%s/holds", acc[0]), "h1", map[string]any{
		"amount_minor": 200000, "currency": "USD",
		"placed_at": "2028-02-29T10:00:00Z", "as_of": "2028-02-29",
	})
	if status != http.StatusCreated {
		t.Fatalf("hold status = %d, body %v", status, hold)
	}

	_, bal = get(fmt.Sprintf("/v1/accounts/%s/balances?as_of=2028-02-29", acc[0]))
	if bal["ledger_minor"].(float64) != 500000 {
		t.Fatal("a hold changed the LEDGER balance; it must not")
	}
	if bal["available_minor"].(float64) != 300000 {
		t.Fatalf("available = %v, want 300000", bal["available_minor"])
	}
	if bal["pending_minor"].(float64) != 200000 {
		t.Fatalf("pending = %v, want 200000", bal["pending_minor"])
	}

	// A hold larger than available is refused.
	status, body := postJSON(t, srv, fmt.Sprintf("/v1/accounts/%s/holds", acc[0]), "h2", map[string]any{
		"amount_minor": 9_000_000, "currency": "USD",
		"placed_at": "2028-02-29T10:00:00Z", "as_of": "2028-02-29",
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("oversized hold status = %d", status)
	}
	if body["error"].(map[string]any)["code"] != httpapi.CodeInsufficientAvailable {
		t.Fatalf("code = %v", body["error"])
	}

	// Release, then release again: the second is a conflict, not a 500.
	holdID := hold["hold_id"].(string)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/holds/"+holdID+"?release_kind=cancelled", nil)
	req.Header.Set("X-Principal", "sim")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("release status = %d", resp.StatusCode)
	}
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("double release status = %d, want 409", resp2.StatusCode)
	}
}

func TestEntriesEndpointIsWhatTheReconcilerReads(t *testing.T) {
	srv, _, acc := newServer(t)
	postJSON(t, srv, "/v1/postings", "e1", posting(acc[0], acc[1], 700))

	req, _ := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/v1/accounts/%s/entries?business_date=2028-02-29", srv.URL, acc[0]), nil)
	req.Header.Set("X-Principal", "sim")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	entries := out["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	if entries[0].(map[string]any)["amount_minor"].(float64) != -700 {
		t.Fatalf("amount = %v", entries[0])
	}
}

func TestHealthReadyAndMetrics(t *testing.T) {
	srv, _, _ := newServer(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
	}
}

func TestNotFoundAndBadIdentifiers(t *testing.T) {
	srv, _, _ := newServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/accounts/not-a-uuid/balances?as_of=2028-02-29", nil)
	req.Header.Set("X-Principal", "sim")
	resp, _ := srv.Client().Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad uuid = %d", resp.StatusCode)
	}

	missing := uuid.New()
	req2, _ := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/v1/accounts/%s/balances?as_of=2028-02-29", srv.URL, missing), nil)
	req2.Header.Set("X-Principal", "sim")
	resp2, _ := srv.Client().Do(req2)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("missing account = %d, want 404", resp2.StatusCode)
	}

	req3, _ := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/v1/accounts/%s/balances", srv.URL, missing), nil)
	req3.Header.Set("X-Principal", "sim")
	resp3, _ := srv.Client().Do(req3)
	_ = resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing as_of = %d, want 400", resp3.StatusCode)
	}
}
