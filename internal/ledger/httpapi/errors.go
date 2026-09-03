package httpapi

import (
	"errors"
	"net/http"

	"github.com/roshanrana/shadowbook/internal/ledger/balance"
	"github.com/roshanrana/shadowbook/internal/ledger/posting"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
	"github.com/roshanrana/shadowbook/internal/money"
)

// The error taxonomy of LLD §3.4. This file is the ONLY place in the ledger
// that knows about HTTP status codes; every other package speaks in sentinel
// errors, which is what keeps the two ingress paths of D-012 equivalent.
const (
	CodeInvalidRequest          = "InvalidRequest"
	CodeMissingIdempotencyKey   = "MissingIdempotencyKey"
	CodeUnknownPrincipal        = "UnknownPrincipal"
	CodeAccountNotFound         = "AccountNotFound"
	CodeIdempotencyBodyMismatch = "IdempotencyBodyMismatch"
	CodeHoldAlreadyReleased     = "HoldAlreadyReleased"
	CodeEODAlreadyRun           = "EODAlreadyRun"
	CodeEntriesNotBalanced      = "EntriesNotBalanced"
	CodeCurrencyMismatch        = "CurrencyMismatch"
	CodeInsufficientAvailable   = "InsufficientAvailable"
	CodeInternal                = "Internal"
)

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// classify maps a domain error to a code and status. Unrecognised errors are
// Internal/500 -- never a guessed 4xx, because a 4xx tells the caller not to
// retry and getting that wrong hides a bug.
func classify(err error) (code string, status int) {
	switch {
	// A malformed body is the client's fault. Reporting it as 500 tells the
	// caller to retry a request that can never succeed -- and hides a 400 in
	// the error-rate graph as if it were a ledger failure.
	case errors.Is(err, errInvalidBody):
		return CodeInvalidRequest, http.StatusBadRequest
	case errors.Is(err, posting.ErrMissingIdempotencyKey):
		return CodeMissingIdempotencyKey, http.StatusBadRequest
	case errors.Is(err, posting.ErrUnknownKind), errors.Is(err, money.ErrUnknownCurrency):
		return CodeInvalidRequest, http.StatusBadRequest
	case errors.Is(err, posting.ErrIdempotencyBodyMismatch):
		return CodeIdempotencyBodyMismatch, http.StatusConflict
	case errors.Is(err, posting.ErrEntriesNotBalanced):
		return CodeEntriesNotBalanced, http.StatusUnprocessableEntity
	case errors.Is(err, posting.ErrCurrencyMismatch), errors.Is(err, money.ErrCurrencyMismatch):
		return CodeCurrencyMismatch, http.StatusUnprocessableEntity
	case errors.Is(err, balance.ErrInsufficientAvailable):
		return CodeInsufficientAvailable, http.StatusUnprocessableEntity
	case errors.Is(err, store.ErrNotFound):
		return CodeAccountNotFound, http.StatusNotFound
	case store.IsUniqueViolation(err, store.ConstraintEODRun):
		return CodeEODAlreadyRun, http.StatusConflict
	case store.IsInvariantViolation(err):
		return CodeEntriesNotBalanced, http.StatusUnprocessableEntity
	default:
		return CodeInternal, http.StatusInternalServerError
	}
}
