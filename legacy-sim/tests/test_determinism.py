"""FR-S6 and NFR-5: the same seed must produce byte-identical output."""

from __future__ import annotations

import datetime as dt
import hashlib
from pathlib import Path

from legacy_sim.generator import LegacyCore, seed_for
from legacy_sim.run import run_window
from legacy_sim.windows import W1, W2

QUIRKS = Path(__file__).resolve().parents[1] / "quirks.yaml"
SEED = 20260903


def _hash_tree(root: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    for p in sorted(root.rglob("*.txt")):
        out[str(p.relative_to(root))] = hashlib.sha256(p.read_bytes()).hexdigest()
    return out


def test_two_runs_of_the_same_seed_are_byte_identical(tmp_path: Path) -> None:
    a, b = tmp_path / "a", tmp_path / "b"
    run_window(W1, SEED, QUIRKS, a)
    run_window(W1, SEED, QUIRKS, b)
    assert _hash_tree(a) == _hash_tree(b)


def test_a_different_seed_produces_different_output(tmp_path: Path) -> None:
    a, b = tmp_path / "a", tmp_path / "b"
    run_window(W1, SEED, QUIRKS, a)
    run_window(W1, SEED + 1, QUIRKS, b)
    assert _hash_tree(a) != _hash_tree(b), "the seed is not actually driving the stream"


def test_seed_split_keeps_component_streams_apart() -> None:
    """legacy-sim and the harness must never share a random stream."""
    assert seed_for("legacy-sim", SEED) != seed_for("harness", SEED)
    assert seed_for("legacy-sim", SEED) == seed_for("legacy-sim", SEED)
    assert seed_for("legacy-sim", SEED) != seed_for("legacy-sim", SEED + 1)


def test_accounts_are_deterministic_and_straddle_the_grandfather_date() -> None:
    one = LegacyCore.build_accounts(SEED)
    two = LegacyCore.build_accounts(SEED)
    assert [a.account_id for a in one] == [a.account_id for a in two]
    assert one == sorted(one, key=lambda a: a.account_id), "accounts must be sorted"

    cutoff = dt.date(2019, 1, 1)
    before = [a for a in one if a.opened_on < cutoff]
    after = [a for a in one if a.opened_on >= cutoff]
    assert before and after, "Q4's waiver needs accounts on both sides of 2019-01-01"


def test_windows_write_one_txn_and_one_bal_file_per_legacy_business_day(tmp_path: Path) -> None:
    """The LEGACY core's business days, not the documented calendar's.

    W2 spans 9 documented business days but the quirked core produces 10, because
    Q5 has it treating Columbus Day as a working day. That extra file is the only
    place Q5 exists -- if the simulator iterated the documented calendar it would
    never run on 2028-10-09 and Q5 would be permanently undetectable.
    """
    quirked_w1 = run_window(W1, SEED, QUIRKS, tmp_path / "q")
    quirked_w2 = run_window(W2, SEED, QUIRKS, tmp_path / "q")
    control_w2 = run_window(W2, SEED, QUIRKS, tmp_path / "c", documented_only=True)

    assert quirked_w1["business_days"] == 30
    assert control_w2["business_days"] == 9, "the documented calendar excludes Columbus Day"
    assert quirked_w2["business_days"] == 10, "Q5 adds Columbus Day to the legacy core's calendar"

    assert len(list((tmp_path / "q" / "W1").glob("*.txt"))) == 60
    assert len(list((tmp_path / "q" / "W2").glob("*.txt"))) == 20
    assert len(list((tmp_path / "c" / "W2").glob("*.txt"))) == 18


def test_the_control_run_disables_every_quirk(tmp_path: Path) -> None:
    quirked = run_window(W1, SEED, QUIRKS, tmp_path / "q")
    control = run_window(W1, SEED, QUIRKS, tmp_path / "c", documented_only=True)
    assert quirked["quirks_enabled"], "the quirked run should have quirks on"
    assert control["quirks_enabled"] == []
    # The control must move MORE transactions: Q9 and Q11 delete some.
    assert int(control["transactions"]) > int(quirked["transactions"])  # type: ignore[call-overload]
