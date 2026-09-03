# Design Templates — HLD, LLD, and Stack Recommendation

Read this when entering Phase 1 (HLD) or Phase 2 (LLD). Use the templates as the
document skeleton; delete sections that genuinely don't apply (say so in one line
rather than leaving empty headers).

## Writing rules for design docs

- Every claim traces to a requirement. Reference requirement IDs (`FR-3`, `NFR-2`)
  from `01-requirements.md` so nothing in the design is unmotivated.
- Prefer tables and Mermaid diagrams over prose. Diagrams-as-code stay diffable and
  cost few tokens to re-read later.
- Decisions with alternatives go into `docs/design/decisions.md` as mini-ADRs
  (5 lines: context, options, decision, rationale, consequences). The HLD/LLD then
  states the decision and links the ADR — it does not re-argue it.
- Design for the scale the requirements state, not the scale that is fun. If
  requirements say 500 users, a modular monolith beats microservices — write that
  reasoning down so future sessions don't "improve" it.

---

## HLD template (`docs/design/02-hld.md`)

```markdown
# High-Level Design — <project>
Status: draft | approved <date>    Requirements: docs/design/01-requirements.md

## 1. Overview
3–6 sentences: what the system does and the architectural approach in plain words.

## 2. Architecture style
Chosen style (modular monolith / microservices / serverless / event-driven / hybrid)
and a short rationale tied to NFRs (team size, scale targets, ops budget).
Default to the simplest style that meets the NFRs.

## 3. System context
Mermaid diagram: the system, its users/actor types, and every external system it
talks to (auth provider, payment, email, third-party APIs).

## 4. Components
| Component | Responsibility | Talks to | Owns data? |
One row per deployable/major module. Responsibilities must not overlap.

## 5. Data architecture
Stores (and why each), which component owns which data (single-writer principle),
data flow for the main lifecycle, retention/PII handling if relevant.

## 6. Critical flows
Sequence diagrams (Mermaid) for the 3–5 flows where the design could fail:
the core business transaction, authn/z, and the highest-load path at minimum.

## 7. Technology stack recommendation
See format below. One subsection per layer.

## 8. Cross-cutting concerns
Authentication & authorization model; configuration & secrets strategy;
logging/metrics/tracing approach; error-handling philosophy (fail fast vs degrade);
background jobs; caching policy.

## 9. Non-functional design
How the architecture meets each NFR, one line per NFR-ID: performance budgets,
availability approach, scaling model, security posture, compliance notes.

## 10. Risks & mitigations
Top 3–7 risks (technical and delivery), each with a mitigation or a spike task.

## 11. Explicitly out of scope
```

## Tech-stack recommendation format (HLD §7)

For **each layer** — language/runtime, backend framework, frontend framework (if
any), primary datastore, cache/queue (if needed), infra & deployment target, CI,
test tooling — present:

```markdown
### <Layer>
| Option | Strengths | Costs / risks |
| A (2–3 rows) | one line | one line |

**Recommendation: <option>** — 2–3 sentences tying the choice to THIS project's
requirements, the user's stated skills, hiring/ecosystem reality, and operational
burden. Not a generic "X is popular" argument.
```

Rules:

- Recommend boring, well-documented technology unless an NFR demands otherwise —
  AI agents are dramatically more reliable in ecosystems with deep public training
  data and stable APIs (mainstream languages, mature frameworks, SQL databases).
- Bias toward strong static typing and first-class test tooling: the validation
  gates in this lifecycle are only as good as what the toolchain can check.
- Minimize the number of languages and services. Every additional runtime
  multiplies gate configuration, CI time, and context size.
- End the section with: "Confirm or override each recommendation at this gate —
  overrides are fine and will be recorded in decisions.md."

---

## LLD template (`docs/design/03-lld.md`)

The LLD is the implementation contract. A worker agent holding only the LLD
section for its component plus its task pack must be able to build without asking
questions. Precision here is what makes cheap, parallel implementation possible.

```markdown
# Low-Level Design — <project>
Status: draft | approved <date>    HLD: docs/design/02-hld.md

## 1. Repository layout
Full directory tree with one-line annotations. This is the layout Phase 4
scaffolds — treat it as an exact spec, not a sketch.

## 2. Conventions
Naming, module boundaries, import rules, shared error/response envelope format,
lint & formatter configs to be used, commit message format (include task IDs).

## 3. Component designs   ← one subsection per HLD component
### 3.x <Component>
- Responsibility recap (1 line) and boundaries (what it must NOT do)
- Public interface: exact API contract — endpoint table (method, path, request,
  response, errors, authz) or typed function signatures for internal modules
- Data model: DDL / schema definitions / typed models, with indexes and
  constraints spelled out
- Internal structure: modules and key functions with signatures
- Sequence detail for this component's part of the critical flows
- Error taxonomy: enumerated error cases → code, HTTP status, user-visible message,
  retryable?
- Config: env vars table (name, purpose, default, required?)
- Test plan: what unit tests cover vs what integration tests cover; fixtures needed

## 4. Shared contracts
Cross-component types/events/API schemas. These are the FROZEN interfaces that
allow parallel implementation. Changing one after approval = plan change.

## 5. Data migration & versioning policy
Migration tool, forward-only vs reversible, seed data strategy, API versioning.

## 6. Test strategy (global)
Test pyramid targets, coverage target for core logic (default: 80% lines on
business logic; do not chase 100%), what e2e covers, how external services are
faked (contract tests / test containers / recorded fixtures).

## 7. Observability plan
What gets logged (and what must never be logged — secrets, PII), key metrics,
trace propagation, health/readiness endpoints.
```

## Gate presentation (both phases)

End the phase with exactly this shape of message to the user:

1. "Design doc written to `<path>` (N lines)."
2. A ≤10-line summary of the decisions that most deserve their attention.
3. Numbered open questions, if any (aim for zero by making a recommendation for
   each and asking for confirmation rather than asking open-ended questions).
4. *"Reply 'approved' to proceed, or tell me what to change."*

On approval: flip the doc's Status line, log the approval in `STATE.md`, proceed.
