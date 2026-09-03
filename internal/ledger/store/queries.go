package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/money"
)

// ErrNotFound is returned when a lookup finds nothing. It is the one error this
// package translates, because "no rows" is a fact rather than a database
// failure; every other error is returned unwrapped for SQLSTATE inspection.
var ErrNotFound = errors.New("store: not found")

// ---------------------------------------------------------------- accounts

// InsertAccount adds an account. Accounts come from legacy-sim, never from a
// migration -- fixture data in a migration would break FR-S6 determinism.
func InsertAccount(ctx context.Context, q Queryer, a Account) error {
	_, err := q.Exec(ctx,
		`INSERT INTO accounts (account_id, product_code, currency, opened_on)
		 VALUES ($1,$2,$3,$4)`,
		a.ID, a.ProductCode, string(a.Currency), toDate(a.OpenedOn))
	return err
}

// GetAccount reads one account.
func GetAccount(ctx context.Context, q Queryer, id uuid.UUID) (Account, error) {
	var (
		a      Account
		cur    string
		opened time.Time
	)
	err := q.QueryRow(ctx,
		`SELECT account_id, product_code, currency, opened_on FROM accounts WHERE account_id = $1`, id).
		Scan(&a.ID, &a.ProductCode, &cur, &opened)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, fmt.Errorf("%w: account %s", ErrNotFound, id)
		}
		return Account{}, err
	}
	a.Currency = money.Currency(cur)
	a.OpenedOn = fromDate(opened)
	return a, nil
}

// AllAccounts returns every account, ordered by id so any output derived from
// it is deterministic (NFR-5).
func AllAccounts(ctx context.Context, q Queryer) ([]Account, error) {
	rows, err := q.Query(ctx,
		`SELECT account_id, product_code, currency, opened_on FROM accounts ORDER BY account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var (
			a      Account
			cur    string
			opened time.Time
		)
		if err := rows.Scan(&a.ID, &a.ProductCode, &cur, &opened); err != nil {
			return nil, err
		}
		a.Currency = money.Currency(cur)
		a.OpenedOn = fromDate(opened)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- idempotency

// IdempotencyRecord is a stored response keyed by (principal, key).
type IdempotencyRecord struct {
	BodyHash  []byte
	PostingID *uuid.UUID
	Response  []byte
}

// ClaimIdempotencyKey inserts the key. A duplicate raises 23505 on
// idempotency_keys_pkey and the error is returned UNWRAPPED so the caller can
// branch on it -- that violation IS the duplicate detection (CLAUDE.md). There
// is deliberately no prior SELECT.
func ClaimIdempotencyKey(ctx context.Context, q Queryer, principal, key string, bodyHash []byte) error {
	_, err := q.Exec(ctx,
		`INSERT INTO idempotency_keys (principal, idem_key, body_hash, response)
		 VALUES ($1,$2,$3,'{}'::jsonb)`,
		principal, key, bodyHash)
	return err
}

// LoadIdempotency reads a previously stored claim. Called only after a 23505.
func LoadIdempotency(ctx context.Context, q Queryer, principal, key string) (IdempotencyRecord, error) {
	var r IdempotencyRecord
	err := q.QueryRow(ctx,
		`SELECT body_hash, posting_id, response FROM idempotency_keys
		 WHERE principal = $1 AND idem_key = $2`, principal, key).
		Scan(&r.BodyHash, &r.PostingID, &r.Response)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("%w: idempotency %s/%s", ErrNotFound, principal, key)
	}
	return r, err
}

// CompleteIdempotency attaches the posting and stored response to the claim.
// idempotency_keys is NOT append-only, so this update is legal by design.
func CompleteIdempotency(ctx context.Context, q Queryer, principal, key string, postingID uuid.UUID, response []byte) error {
	_, err := q.Exec(ctx,
		`UPDATE idempotency_keys SET posting_id = $3, response = $4
		 WHERE principal = $1 AND idem_key = $2`, principal, key, postingID, response)
	return err
}

// ---------------------------------------------------------------- postings

// InsertPosting writes the envelope.
func InsertPosting(ctx context.Context, q Queryer, p Posting) error {
	_, err := q.Exec(ctx,
		`INSERT INTO postings
		   (posting_id, principal, kind, currency, business_date, value_date, posted_at, reverses_posting_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.ID, p.Principal, p.Kind, string(p.Currency),
		toDate(p.BusinessDate), toDate(p.ValueDate), p.PostedAt, p.ReversesPostingID)
	return err
}

// InsertEntries writes the legs and returns their assigned ids in order. The
// zero-sum and entry-count checks are deferred to COMMIT, so a violation
// surfaces from tx.Commit, not from here.
func InsertEntries(ctx context.Context, q Queryer, entries []Entry) ([]int64, error) {
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		var id int64
		err := q.QueryRow(ctx,
			`INSERT INTO entries
			   (posting_id, account_id, currency, amount_minor, scale, business_date, value_date, reverses_entry_id)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING entry_id`,
			e.PostingID, e.AccountID, string(e.Amount.Currency), e.Amount.Minor, int16(e.Amount.Scale),
			toDate(e.BusinessDate), toDate(e.ValueDate), e.ReversesEntryID).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// EntriesForAccountDay returns one account's entries on one business date,
// ordered deterministically. The reconciler reads through this.
func EntriesForAccountDay(ctx context.Context, q Queryer, accountID uuid.UUID, d bizdate.BusinessDate) ([]Entry, error) {
	rows, err := q.Query(ctx,
		`SELECT entry_id, posting_id, account_id, currency, amount_minor, scale
		   FROM entries WHERE account_id = $1 AND business_date = $2
		  ORDER BY entry_id`, accountID, toDate(d))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		var cur string
		var minor int64
		var scale int16
		if err := rows.Scan(&e.ID, &e.PostingID, &e.AccountID, &cur, &minor, &scale); err != nil {
			return nil, err
		}
		// Scale is rebuilt from the registry, not trusted from the column: the
		// registry is the authority (FR-L11). A stored scale that disagrees is
		// corruption and must not be silently propagated.
		amt, err := money.New(minor, money.Currency(cur))
		if err != nil {
			return nil, fmt.Errorf("store: entry %d has unusable money: %w", e.ID, err)
		}
		if int16(amt.Scale) != scale {
			return nil, fmt.Errorf("store: entry %d stored scale %d, registry says %d for %s",
				e.ID, scale, amt.Scale, cur)
		}
		e.Amount = amt
		e.BusinessDate = d
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- balances

// LedgerBalance is the derived balance (FR-L4). No column stores it.
//
// The LEFT JOIN LATERAL and the two coalesces are load-bearing: a plain join
// drops the account entirely when no checkpoint exists yet, and GROUP BY is
// mandatory because balance_minor sits beside an aggregate (D-015).
const ledgerBalanceSQL = `
SELECT coalesce(c.balance_minor, 0) + coalesce(sum(e.amount_minor), 0)
FROM       (SELECT 1) AS anchor
LEFT JOIN LATERAL (SELECT balance_minor, last_entry_id FROM checkpoints
                   WHERE account_id = $1 AND business_date <= $2
                   ORDER BY business_date DESC LIMIT 1) c ON true
LEFT JOIN  entries e ON e.account_id = $1
                    AND e.entry_id > coalesce(c.last_entry_id, 0)
                    AND e.business_date <= $2
GROUP BY c.balance_minor`

// LedgerBalance returns the ledger balance as of a business date.
func LedgerBalance(ctx context.Context, q Queryer, accountID uuid.UUID, asOf bizdate.BusinessDate) (int64, error) {
	var v int64
	err := q.QueryRow(ctx, ledgerBalanceSQL, accountID, toDate(asOf)).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil // an account with neither checkpoint nor entries is zero
	}
	return v, err
}

// GlobalInvariant returns the per-currency sum of every entry. Each must be
// zero at all times (FR-L9). This is a query, not a stored aggregate.
func GlobalInvariant(ctx context.Context, q Queryer) (map[string]int64, error) {
	rows, err := q.Query(ctx,
		`SELECT currency, sum(amount_minor) FROM entries GROUP BY currency ORDER BY currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var cur string
		var drift int64
		if err := rows.Scan(&cur, &drift); err != nil {
			return nil, err
		}
		out[cur] = drift
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- outbox

// OutboxRow is one unsent event.
type OutboxRow struct {
	ID           int64
	PostingID    uuid.UUID
	PartitionKey string
	Payload      []byte
}

// InsertOutbox writes the event in the SAME transaction as the entries. That
// co-commit is the whole of the transactional-outbox pattern (FR-L7).
func InsertOutbox(ctx context.Context, q Queryer, postingID uuid.UUID, partitionKey string, payload []byte) error {
	_, err := q.Exec(ctx,
		`INSERT INTO outbox (posting_id, partition_key, payload) VALUES ($1,$2,$3)`,
		postingID, partitionKey, payload)
	return err
}

// ClaimOutboxBatch reads the oldest unsent rows in id order, so per-account
// ordering is preserved when they are produced under an account key.
func ClaimOutboxBatch(ctx context.Context, q Queryer, limit int) ([]OutboxRow, error) {
	rows, err := q.Query(ctx,
		`SELECT outbox_id, posting_id, partition_key, payload
		   FROM outbox WHERE sent_at IS NULL ORDER BY outbox_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.ID, &r.PostingID, &r.PartitionKey, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkOutboxSent is called only after every produce promise in the batch has
// resolved, which makes the relay at-least-once by construction.
func MarkOutboxSent(ctx context.Context, q Queryer, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE outbox SET sent_at = now() WHERE outbox_id = ANY($1)`, ids)
	return err
}

// OutboxDepth is the unsent count, exported as a metric.
func OutboxDepth(ctx context.Context, q Queryer) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE sent_at IS NULL`).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------- inbox

// ClaimInboxMessage records a consumed message id. A duplicate raises 23505 on
// inbox_pkey; the error is returned unwrapped because that violation IS
// configuration C's exactly-once effect (LLD §3.7).
func ClaimInboxMessage(ctx context.Context, q Queryer, messageID, topic string, partition int32, offset int64) error {
	_, err := q.Exec(ctx,
		`INSERT INTO inbox (message_id, topic, partition, msg_offset) VALUES ($1,$2,$3,$4)`,
		messageID, topic, partition, offset)
	return err
}

// CountAppliedMessage reports how many times a message id was applied. Finding
// 2 measures duplication by asking the ledger, never by inferring from the
// broker (LLD §3.11).
func CountAppliedMessage(ctx context.Context, q Queryer, messageID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM inbox WHERE message_id = $1`, messageID).Scan(&n)
	return n, err
}
