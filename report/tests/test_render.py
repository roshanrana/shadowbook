"""FINDINGS.md is generated, deterministic, and honest about what it lacks."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from report.render import build_context, render

REPO = Path(__file__).resolve().parents[2]
ARTEFACT = REPO / "reports" / "runs" / "demo" / "finding1.json"


@pytest.fixture(scope="module")
def artefact() -> Path:
    if not ARTEFACT.exists():
        pytest.skip("run `make demo` first")
    return ARTEFACT


def build(artefact: Path) -> str:
    ctx = build_context(artefact, None, REPO, "fixed")
    return render(ctx)


def test_two_renders_of_the_same_artefacts_are_byte_identical(artefact: Path) -> None:
    assert build(artefact) == build(artefact)


def test_every_mandatory_section_is_present(artefact: Path) -> None:
    body = build(artefact)
    for heading in (
        "## Finding 1 — Time-to-discovery per quirk",
        "## Finding 2 — Delivery-semantics ablation under broker loss",
        "## Methods",
        "## What this does not prove",
        "## Reproduce",
    ):
        assert heading in body, f"missing section: {heading}"


def test_every_quirk_appears_including_undetected_ones(artefact: Path) -> None:
    body = build(artefact)
    finding = json.loads(artefact.read_text(encoding="utf-8"))
    for quirk_id in finding["quirk_specs"]:
        assert f"| {quirk_id} |" in body, f"{quirk_id} is missing from the table"


def test_the_disclaimers_that_must_never_be_dropped(artefact: Path) -> None:
    body = build(artefact)
    for claim in (
        "seeded and known",
        "simulator is not a core",
        "Single-box numbers are not production numbers",
        "ends at the database boundary",
        "Portfolio work, not a production system",
    ):
        assert claim in body, f"the disclaimer {claim!r} was dropped"


def test_finding2_says_it_was_not_run_rather_than_implying_numbers(artefact: Path) -> None:
    """`make report` must render a valid document with Finding 2 absent, and
    must not imply results it does not have (execution plan §4, T-051)."""
    body = build(artefact)
    assert "**Not run.**" in body
    assert "are not claimed here" in body


def test_the_header_carries_everything_needed_to_reproduce(artefact: Path) -> None:
    body = build(artefact)
    for field in ("Git SHA", "Seed", "Windows", "Go / Python", "PostgreSQL", "Machine"):
        assert f"| {field} |" in body


def test_attribution_separates_detection_from_isolation() -> None:
    """Detected-but-unattributable must not be counted as isolated.

    The whole point of Finding 1a is that these two numbers differ; a bug that
    conflated them would make the section silently claim a stronger result.
    """
    rows = [
        {
            "quirk_id": "Q1",
            "detected": True,
            "breaks_to_isolate": 1,
            "first_detected_business_day": 3,
        },
        {
            "quirk_id": "Q2",
            "detected": True,
            "breaks_to_isolate": None,
            "first_detected_business_day": 1,
        },
        {
            "quirk_id": "Q3",
            "detected": False,
            "breaks_to_isolate": None,
            "first_detected_business_day": None,
        },
    ]
    from report.render import _attribution

    a = _attribution(rows)
    assert a["detected"] == 2
    assert a["total"] == 3
    assert a["isolated"] == 1
    assert a["isolated_ids"] == ["Q1"]
    assert a["unattributed_ids"] == ["Q2"]
    # Undetected quirks contribute no day at all; including them as 0 would
    # drag the median toward "found immediately", which is the opposite of true.
    assert a["median_days"] == 2
    assert a["best_days"] == 1
    assert a["worst_days"] == 3


def test_cadence_floor_groups_by_cadence_and_ignores_undetected() -> None:
    rows = [
        {"quirk_id": "Q1", "cadence": "daily", "detected": True, "first_detected_business_day": 1},
        {"quirk_id": "Q2", "cadence": "daily", "detected": True, "first_detected_business_day": 3},
        {
            "quirk_id": "Q3",
            "cadence": "month_end",
            "detected": True,
            "first_detected_business_day": 25,
        },
        {
            "quirk_id": "Q4",
            "cadence": "month_end",
            "detected": False,
            "first_detected_business_day": None,
        },
    ]
    from report.render import _cadence_floor

    c = _cadence_floor(rows)
    assert c["daily"] == {"count": 2, "median": 2, "min": 1, "max": 3}
    assert c["month_end"] == {"count": 1, "median": 25, "min": 25, "max": 25}


def test_report_states_the_attribution_gap(artefact: Path) -> None:
    """A reader must be able to see that 12-of-12 detected is not 12-of-12
    explained, without inferring it from a column of 'not isolated'."""
    out = build(artefact)
    assert "detection is not attribution" in out
    assert "Isolated to a single quirk" in out
    assert "tripwire, not a diagnosis" in out
