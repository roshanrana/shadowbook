// Package httpapi is the ledger's HTTP surface.
//
// Four business routes on the standard library mux -- no framework, no router
// dependency (D-013). vegeta drives HTTP, which is why this is not gRPC, and a
// reviewer can reproduce a posting with curl, which matters for a portfolio
// repo.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/ledger/balance"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

// Server wires the routes.
type Server struct {
	st         *store.Store
	posting    *posting.Service
	balance    *balance.Service
	metrics    *obs.Metrics
	checker    *obs.Checker
	registry   *prometheus.Registry
	log        *slog.Logger
	principals map[string]bool
	reqCounter atomic.Uint64
	// clock is injected: nothing in the ledger reads wall-clock time directly
	// (LLD §8.3).
	clock func() time.Time
}

// Config builds a Server.
type Config struct {
	Store      *store.Store
	Metrics    *obs.Metrics
	Checker    *obs.Checker
	Registry   *prometheus.Registry
	Logger     *slog.Logger
	Principals []string
	Clock      func() time.Time
}

// New builds the server.
func New(c Config) *Server {
	ps := map[string]bool{}
	for _, p := range c.Principals {
		ps[p] = true
	}
	if len(ps) == 0 {
		ps["sim"] = true
	}
	clock := c.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		st: c.Store, posting: posting.New(c.Store), balance: balance.New(c.Store),
		metrics: c.Metrics, checker: c.Checker, registry: c.Registry,
		log: log, principals: ps, clock: clock,
	}
}

// Handler returns the mux. Go 1.22+ patterns carry the method, so no router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/postings", s.handlePostPosting)
	mux.HandleFunc("POST /v1/accounts/{account_id}/holds", s.handlePlaceHold)
	mux.HandleFunc("DELETE /v1/holds/{hold_id}", s.handleReleaseHold)
	mux.HandleFunc("GET /v1/accounts/{account_id}/balances", s.handleBalances)
	mux.HandleFunc("GET /v1/accounts/{account_id}/entries", s.handleEntries)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	if s.registry != nil {
		mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	}
	return mux
}

func (s *Server) nextRequestID() string {
	return fmt.Sprintf("req-%d", s.reqCounter.Add(1))
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.Error("write response", "error", err)
	}
}

func (s *Server) writeErr(w http.ResponseWriter, reqID string, err error) {
	code, status := classify(err)
	if status >= 500 {
		// Full detail to the log, never to the client.
		s.log.Error("request failed", "request_id", reqID, "code", code, "error", err)
	} else {
		s.log.Info("request rejected", "request_id", reqID, "code", code, "error", err)
	}
	msg := err.Error()
	if status >= 500 {
		msg = "internal error"
	}
	s.writeJSON(w, status, errorEnvelope{errorBody{Code: code, Message: msg, RequestID: reqID}})
}

// principal validates the header. Not security -- idempotency keys are scoped
// per principal, so the principal is load-bearing for correctness (LLD §8.1).
func (s *Server) principal(r *http.Request) (string, error) {
	p := r.Header.Get("X-Principal")
	if p == "" || !s.principals[p] {
		return "", fmt.Errorf("principal %q is not in the allow-list", p)
	}
	return p, nil
}

// ---------------------------------------------------------------- postings

type postingRequestBody struct {
	Kind              string                 `json:"kind"`
	Currency          string                 `json:"currency"`
	BusinessDate      string                 `json:"business_date"`
	ValueDate         string                 `json:"value_date"`
	PostedAt          string                 `json:"posted_at"`
	ReversesPostingID *string                `json:"reverses_posting_id"`
	Entries           []posting.EntryRequest `json:"entries"`
}

type postingResponseBody struct {
	PostingID    string        `json:"posting_id"`
	BusinessDate string        `json:"business_date"`
	Entries      []entryOutput `json:"entries"`
}

type entryOutput struct {
	EntryID     int64  `json:"entry_id"`
	AccountID   string `json:"account_id"`
	AmountMinor int64  `json:"amount_minor"`
}

func (s *Server) handlePostPosting(w http.ResponseWriter, r *http.Request) {
	reqID := s.nextRequestID()
	start := s.clock()

	p, err := s.principal(r)
	if err != nil {
		s.writeJSON(w, http.StatusForbidden,
			errorEnvelope{errorBody{Code: CodeUnknownPrincipal, Message: err.Error(), RequestID: reqID}})
		return
	}
	key := r.Header.Get("Idempotency-Key")

	var body postingRequestBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: %w", errInvalidBody, err))
		return
	}

	req, err := toPostingRequest(p, key, body)
	if err != nil {
		s.writeErr(w, reqID, err)
		return
	}

	res, err := s.posting.Post(r.Context(), req)
	if err != nil {
		s.metrics.PostingsTotal.WithLabelValues(req.Kind, "error").Inc()
		s.writeErr(w, reqID, err)
		return
	}

	result := "created"
	status := http.StatusCreated
	if res.Replayed {
		result, status = "replayed", http.StatusOK
	}
	s.metrics.PostingsTotal.WithLabelValues(req.Kind, result).Inc()
	s.metrics.PostingDuration.Observe(s.clock().Sub(start).Seconds())

	out := postingResponseBody{
		PostingID:    res.PostingID.String(),
		BusinessDate: req.BusinessDate.String(),
	}
	for i, e := range req.Entries {
		var id int64
		if i < len(res.EntryIDs) {
			id = res.EntryIDs[i]
		}
		out.Entries = append(out.Entries, entryOutput{
			EntryID: id, AccountID: e.AccountID.String(), AmountMinor: e.AmountMinor,
		})
	}
	s.writeJSON(w, status, out)
}

var errInvalidBody = errors.New("invalid request body")

func toPostingRequest(principal, key string, b postingRequestBody) (posting.Request, error) {
	bd, err := bizdate.Parse(b.BusinessDate)
	if err != nil {
		return posting.Request{}, fmt.Errorf("%w: business_date: %w", errInvalidBody, err)
	}
	vd, err := bizdate.Parse(b.ValueDate)
	if err != nil {
		return posting.Request{}, fmt.Errorf("%w: value_date: %w", errInvalidBody, err)
	}
	at, err := time.Parse(time.RFC3339Nano, b.PostedAt)
	if err != nil {
		return posting.Request{}, fmt.Errorf("%w: posted_at: %w", errInvalidBody, err)
	}
	req := posting.Request{
		Principal: principal, IdempotencyKey: key, Kind: b.Kind,
		Currency: money.Currency(b.Currency), BusinessDate: bd, ValueDate: vd,
		PostedAt: at.UTC(), Entries: b.Entries,
	}
	if b.ReversesPostingID != nil && *b.ReversesPostingID != "" {
		id, err := uuid.Parse(*b.ReversesPostingID)
		if err != nil {
			return posting.Request{}, fmt.Errorf("%w: reverses_posting_id: %w", errInvalidBody, err)
		}
		req.ReversesPostingID = &id
	}
	return req, nil
}

// ---------------------------------------------------------------- holds

type holdRequestBody struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	PlacedAt    string `json:"placed_at"`
	AsOf        string `json:"as_of"`
}

type holdResponseBody struct {
	HoldID      string `json:"hold_id"`
	AccountID   string `json:"account_id"`
	AmountMinor int64  `json:"amount_minor"`
	PlacedAt    string `json:"placed_at"`
	ExpiresAt   string `json:"expires_at"`
}

func (s *Server) handlePlaceHold(w http.ResponseWriter, r *http.Request) {
	reqID := s.nextRequestID()
	if _, err := s.principal(r); err != nil {
		s.writeJSON(w, http.StatusForbidden,
			errorEnvelope{errorBody{Code: CodeUnknownPrincipal, Message: err.Error(), RequestID: reqID}})
		return
	}
	acct, err := uuid.Parse(r.PathValue("account_id"))
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: account_id: %w", errInvalidBody, err))
		return
	}
	var body holdRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: %w", errInvalidBody, err))
		return
	}
	amt, err := money.New(body.AmountMinor, money.Currency(body.Currency))
	if err != nil {
		s.writeErr(w, reqID, err)
		return
	}
	placedAt, err := time.Parse(time.RFC3339Nano, body.PlacedAt)
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: placed_at: %w", errInvalidBody, err))
		return
	}
	asOf, err := bizdate.Parse(body.AsOf)
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: as_of: %w", errInvalidBody, err))
		return
	}
	h, err := s.balance.PlaceHold(r.Context(), acct, amt, placedAt, asOf)
	if err != nil {
		s.writeErr(w, reqID, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, holdResponseBody{
		HoldID: h.ID.String(), AccountID: acct.String(), AmountMinor: h.Amount.Minor,
		PlacedAt: h.PlacedAt.Format(time.RFC3339Nano), ExpiresAt: h.ExpiresAt.Format(time.RFC3339Nano),
	})
}

func (s *Server) handleReleaseHold(w http.ResponseWriter, r *http.Request) {
	reqID := s.nextRequestID()
	if _, err := s.principal(r); err != nil {
		s.writeJSON(w, http.StatusForbidden,
			errorEnvelope{errorBody{Code: CodeUnknownPrincipal, Message: err.Error(), RequestID: reqID}})
		return
	}
	id, err := uuid.Parse(r.PathValue("hold_id"))
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: hold_id: %w", errInvalidBody, err))
		return
	}
	kind := r.URL.Query().Get("release_kind")
	if kind == "" {
		kind = "cancelled"
	}
	if err := s.balance.ReleaseHold(r.Context(), id, s.clock(), kind); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeJSON(w, http.StatusConflict, errorEnvelope{errorBody{
				Code: CodeHoldAlreadyReleased, Message: err.Error(), RequestID: reqID}})
			return
		}
		s.writeErr(w, reqID, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"hold_id": id.String(), "release_kind": kind})
}

// ---------------------------------------------------------------- reads

type balanceResponseBody struct {
	AccountID      string `json:"account_id"`
	Currency       string `json:"currency"`
	Scale          uint8  `json:"scale"`
	AsOf           string `json:"as_of"`
	LedgerMinor    int64  `json:"ledger_minor"`
	AvailableMinor int64  `json:"available_minor"`
	PendingMinor   int64  `json:"pending_minor"`
}

func (s *Server) handleBalances(w http.ResponseWriter, r *http.Request) {
	reqID := s.nextRequestID()
	acct, err := uuid.Parse(r.PathValue("account_id"))
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: account_id: %w", errInvalidBody, err))
		return
	}
	asOfStr := r.URL.Query().Get("as_of")
	if asOfStr == "" {
		s.writeErr(w, reqID, fmt.Errorf("%w: as_of is required", errInvalidBody))
		return
	}
	asOf, err := bizdate.Parse(asOfStr)
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: as_of: %w", errInvalidBody, err))
		return
	}
	now := s.clock()
	if q := r.URL.Query().Get("now"); q != "" {
		if now, err = time.Parse(time.RFC3339Nano, q); err != nil {
			s.writeErr(w, reqID, fmt.Errorf("%w: now: %w", errInvalidBody, err))
			return
		}
	}
	b, err := s.balance.At(r.Context(), acct, asOf, now)
	if err != nil {
		s.writeErr(w, reqID, err)
		return
	}
	s.writeJSON(w, http.StatusOK, balanceResponseBody{
		AccountID: b.AccountID.String(), Currency: string(b.Currency), Scale: b.Scale,
		AsOf: b.AsOf.String(), LedgerMinor: b.Ledger, AvailableMinor: b.Available,
		PendingMinor: b.Pending,
	})
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	reqID := s.nextRequestID()
	acct, err := uuid.Parse(r.PathValue("account_id"))
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: account_id: %w", errInvalidBody, err))
		return
	}
	d, err := bizdate.Parse(r.URL.Query().Get("business_date"))
	if err != nil {
		s.writeErr(w, reqID, fmt.Errorf("%w: business_date: %w", errInvalidBody, err))
		return
	}
	entries, err := store.EntriesForAccountDay(r.Context(), s.st.Pool, acct, d)
	if err != nil {
		s.writeErr(w, reqID, err)
		return
	}
	out := make([]entryOutput, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryOutput{
			EntryID: e.ID, AccountID: e.AccountID.String(), AmountMinor: e.Amount.Minor})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"account_id": acct.String(), "business_date": d.String(), "entries": out,
		"count": strconv.Itoa(len(out)),
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz is readiness: migrations applied, database reachable, invariant known.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Ping(r.Context()); err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unreachable"})
		return
	}
	body := map[string]any{"status": "ready"}
	if s.checker != nil {
		last := s.checker.Last()
		body["invariant_ok"] = last.OK
	}
	s.writeJSON(w, http.StatusOK, body)
}
