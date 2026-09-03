"""Finding 1 end to end: every quirk must be detected, in isolation.

This is the headline result, so it is asserted rather than inspected. If a quirk
stops being detectable -- because a window changed, or the simulator stopped
exercising it, or a classification rule regressed -- this test fails and says
which one.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from legacy_sim.quirks import load_specs
from reconcile.finding1 import run

QUIRKS = Path(__file__).resolve().parents[2] / "legacy-sim" / "quirks.yaml"
SEED = 20260903


@pytest.fixture(scope="module")
def finding(tmp_path_factory: pytest.TempPathFactory) -> dict[str, object]:
    return run(SEED, QUIRKS, tmp_path_factory.mktemp("f1"))


def _best(finding: dict[str, object], quirk_id: str) -> dict[str, object]:
    """The best detection of a quirk across all windows."""
    per_quirk: dict[str, dict] = finding["per_quirk"]  # type: ignore[assignment]
    rows = [
        v["discoveries"][quirk_id]
        for k, v in per_quirk.items()
        if k.split("/")[0] == quirk_id and quirk_id in v["discoveries"]
    ]
    detected = [r for r in rows if r["detected"]]
    return detected[0] if detected else rows[0]


def test_every_quirk_is_detected_in_at_least_one_window(finding: dict[str, object]) -> None:
    specs = load_specs(QUIRKS)
    enabled = sorted((q for q, s in specs.items() if s.enabled), key=lambda q: int(q[1:]))
    undetected = [q for q in enabled if not _best(finding, q)["detected"]]
    assert not undetected, (
        f"{undetected} produced no attributable break. A quirk that cannot be "
        "detected is a false negative in Finding 1, and the cause is almost "
        "always the simulator or the windows, not the reconciler."
    )


def test_q5_is_detected_only_in_w2_and_q6_only_in_w1(finding: dict[str, object]) -> None:
    """The two-window design of D-010, asserted on the actual result."""
    per_quirk: dict[str, dict] = finding["per_quirk"]  # type: ignore[assignment]
    assert per_quirk["Q5/W2"]["discoveries"]["Q5"]["detected"]
    assert not per_quirk["Q5/W1"]["discoveries"]["Q5"]["detected"]
    assert per_quirk["Q6/W1"]["discoveries"]["Q6"]["detected"]


def test_detection_days_are_plausible(finding: dict[str, object]) -> None:
    """Daily quirks surface immediately; calendar-gated ones take longer. If a
    month-end quirk were detected on day 1 the classifier would be guessing."""
    daily = ("Q2", "Q9", "Q10", "Q11")
    for q in daily:
        assert _best(finding, q)["first_detected_business_day"] <= 2, q
    for q in ("Q4", "Q7", "Q12"):
        day = _best(finding, q)["first_detected_business_day"]
        assert day is not None and day >= 2, f"{q} is month-end gated but surfaced on day {day}"


def test_basis_quirks_isolate_immediately(finding: dict[str, object]) -> None:
    """A day-count ratio names exactly one quirk, so Q3 and Q6 isolate on their
    first break. If they stop isolating, the model-rule table has become
    ambiguous."""
    for q in ("Q3", "Q6", "Q8"):
        row = _best(finding, q)
        assert row["isolated"], f"{q} never isolated"
        assert row["breaks_to_isolate"] == 1, f"{q} took {row['breaks_to_isolate']} breaks"


def test_the_combined_run_finds_fewer_quirks_than_the_isolated_runs(
    finding: dict[str, object],
) -> None:
    """Quirks compound, and the compounded case is harder.

    Q2, Q9 and Q11 change which transactions land on which day, so the daily
    balances Q3 and Q6 accrue on are no longer the shadow's and their exact
    basis ratio is destroyed. Reporting only the compounded number would
    understate what continuous reconciliation can do; reporting only the
    isolated number would overstate it. Finding 1 reports both.
    """
    combined: dict[str, dict] = finding["combined"]  # type: ignore[assignment]
    w1 = combined["W1"]["discoveries"]
    combined_detected = {q for q, d in w1.items() if d["detected"]}
    specs = load_specs(QUIRKS)
    enabled = {q for q, s in specs.items() if s.enabled}
    isolated_detected = {q for q in enabled if _best(finding, q)["detected"]}
    assert isolated_detected == enabled
    assert combined_detected <= isolated_detected


def test_the_result_is_reproducible(tmp_path: Path) -> None:
    a = run(SEED, QUIRKS, tmp_path / "a")
    b = run(SEED, QUIRKS, tmp_path / "b")
    assert a["per_quirk"] == b["per_quirk"]
    assert a["combined"] == b["combined"]
