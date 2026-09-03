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
