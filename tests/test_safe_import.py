import io
import json

import pytest
from click.testing import CliRunner

from sqlite_utils import Database
from sqlite_utils.cli import cli
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    ImportInvariantNotFoundError,
    SafeImportError,
    SafeImportNotEnabledError,
)


def _ids(rows):
    return [row["id"] for row in rows]


def test_safe_import_flag_persists(tmpdir):
    path = str(tmpdir / "data.db")
    Database(path).enable_safe_import()
    assert Database(path).safe_import_enabled() is True
    Database(path).disable_safe_import()
    assert Database(path).safe_import_enabled() is False
    Database(path).enable_safe_import()
    assert Database(path).safe_import_enabled() is True


def test_checkpoint_requires_safe_import():
    db = Database(memory=True)
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()
    db.enable_safe_import()
    checkpoint_id = db.create_import_checkpoint()
    assert isinstance(checkpoint_id, str) and checkpoint_id
    db.disable_safe_import()
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()


def test_checkpoint_commit_rollback_and_cleanup_rules():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")

    missing = "does-not-exist"
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(missing)
    with pytest.raises(CheckpointNotFoundError):
        db.commit_checkpoint(missing)
    with pytest.raises(CheckpointNotFoundError):
        db.cleanup_checkpoint(missing)

    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].insert({"id": 2, "name": "Pancakes"}, pk="id")
    db.commit_checkpoint(checkpoint_id)
    assert _ids(db["dogs"].rows) == [1, 2]
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(checkpoint_id)

    rolled = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Toby"}, pk="id")
    db.rollback_to_checkpoint(rolled)
    assert _ids(db["dogs"].rows) == [1, 2]
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(rolled)

    cleaned = db.create_import_checkpoint()
    db.cleanup_checkpoint(cleaned)
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(cleaned)
    with pytest.raises(CheckpointNotFoundError):
        db.cleanup_checkpoint(cleaned)
    # Cleanup forgets the id and leaves later writes in place.
    db["dogs"].insert({"id": 4, "name": "Ellie"}, pk="id")
    assert 4 in _ids(db["dogs"].rows)


def test_nested_checkpoints_restore_schema_and_rows():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    outer = db.create_import_checkpoint()
    db["dogs"].insert({"id": 2, "name": "Pancakes"}, pk="id")
    db["extra"].insert({"id": 1})
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Toby"}, pk="id")
    db["dogs"].add_column("age", int)
    db["dogs"].create_index(["name"], index_name="idx_dogs_name")
    db.execute(
        "create trigger trg_dogs after insert on dogs "
        "begin select 1; end"
    )
    db.conn.commit()

    db.rollback_to_checkpoint(inner)
    assert _ids(db["dogs"].rows) == [1, 2]
    assert "age" not in db["dogs"].columns_dict
    assert "idx_dogs_name" not in {index.name for index in db["dogs"].indexes}
    assert "trg_dogs" not in {trigger.name for trigger in db.triggers}
    assert db["extra"].exists()
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(inner)

    db.rollback_to_checkpoint(outer)
    assert _ids(db["dogs"].rows) == [1]
    assert not db["extra"].exists()
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)


def test_committing_outer_checkpoint_finalizes_inner():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1}, pk="id")
    outer = db.create_import_checkpoint()
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 2}, pk="id")
    db.commit_checkpoint(outer)
    assert _ids(db["dogs"].rows) == [1, 2]
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)


def test_import_invariants_round_trip_and_evaluation():
    db = Database(memory=True)
    db["books"].insert_all(
        [{"id": 1, "price": 5}, {"id": 2, "price": 8}], pk="id"
    )
    per_row = db.add_import_invariant("books", "price > 0")
    aggregate = db.add_import_invariant("books", "COUNT(*) = 2")
    select_ok = db.add_import_invariant("books", "SELECT SUM(price) = 13")
    select_bad = db.add_import_invariant("books", "SELECT 0")
    listed = db.list_import_invariants("books")
    assert [item["id"] for item in listed] == [
        per_row,
        aggregate,
        select_ok,
        select_bad,
    ]
    assert listed[0]["expression"] == "price > 0"

    result = db.validate_import_invariants("books")
    assert result["valid"] is False
    assert [failure["id"] for failure in result["failures"]] == [select_bad]
    assert result["failures"][0]["expression"] == "SELECT 0"
    assert result["failures"][0]["error"]

    db.remove_import_invariant("books", select_bad)
    assert db.validate_import_invariants("books")["valid"] is True
    with pytest.raises(ImportInvariantNotFoundError):
        db.remove_import_invariant("books", select_bad)

    # Empty table: per-row expressions are vacuously true, aggregates run once.
    db["empty"].create({"id": int})
    db.add_import_invariant("empty", "id > 0")
    zero = db.add_import_invariant("empty", "COUNT(*) = 0")
    positive = db.add_import_invariant("empty", "COUNT(*) > 0")
    empty_result = db.validate_import_invariants("empty")
    assert empty_result["valid"] is False
    assert [failure["id"] for failure in empty_result["failures"]] == [positive]
    assert zero not in [failure["id"] for failure in empty_result["failures"]]


def test_invariants_persist_on_disk(tmpdir):
    path = str(tmpdir / "inv.db")
    db = Database(path)
    invariant_id = db.add_import_invariant("books", "price > 0")
    db.close()
    reopened = Database(path)
    assert reopened.list_import_invariants("books") == [
        {"id": invariant_id, "expression": "price > 0"}
    ]


def test_safe_bulk_insert_and_upsert(tmpdir):
    db = Database(memory=True)
    db.enable_safe_import()
    db.add_import_invariant("books", "price > 0")

    created = db.safe_bulk_insert(
        "books", [{"id": 1, "price": 5}, {"id": 2, "price": 9}], pk="id"
    )
    assert created == {"success": True}
    assert _ids(db["books"].rows) == [1, 2]

    failed = db.safe_bulk_insert(
        "books",
        [
            {"id": 3, "price": 4, "title": "ok"},
            {"id": 4, "price": -1, "title": "nope"},
        ],
        pk="id",
        alter=True,
    )
    assert failed["success"] is False
    assert failed["checkpoint_id"]
    assert failed["failures"]
    assert "invariant" in failed["error_report"].lower()
    assert "validation" in failed["error_report"].lower()
    assert _ids(db["books"].rows) == [1, 2]
    assert "title" not in db["books"].columns_dict
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(failed["checkpoint_id"])

    # Non-invariant SQL errors return an empty failures list and roll back.
    db["books"].insert({"id": 5, "price": 3}, pk="id")
    duplicate = db.safe_bulk_insert("books", [{"id": 5, "price": 3}], pk="id")
    assert duplicate["success"] is False
    assert duplicate["failures"] == []
    assert duplicate["error_report"]
    assert db["books"].count == 3

    with pytest.raises(SafeImportError) as excinfo:
        db.safe_bulk_insert("books", [{"id": 9, "price": -5}], pk="id", strict=True)
    message = str(excinfo.value).lower()
    assert "invariant" in message or "validation" in message or "valid" in message
    assert 9 not in _ids(db["books"].rows)

    upserted = db.safe_bulk_upsert(
        "books", [{"id": 1, "price": 11}], pk="id"
    )
    assert upserted == {"success": True}
    assert db["books"].get(1)["price"] == 11
    rejected = db.safe_bulk_upsert(
        "books", [{"id": 1, "price": -2}], pk="id", strict=False
    )
    assert rejected["success"] is False
    assert db["books"].get(1)["price"] == 11

    # New table created by a failed safe insert is removed again.
    db.add_import_invariant("novels", "price > 0")
    missing = db.safe_bulk_insert("novels", [{"price": -1}])
    assert missing["success"] is False
    assert not db["novels"].exists()


def test_import_csv_and_json_safe_mode(tmpdir):
    db = Database(memory=True)
    db.enable_safe_import()
    db.add_import_invariant("books", "price > 0")
    csv_path = str(tmpdir / "books.csv")
    with open(csv_path, "w", encoding="utf-8") as fh:
        fh.write("id,price\n1,5\n2,8\n")
    assert db.import_csv("books", csv_path, safe_mode=True, pk="id") == {
        "success": True
    }
    # SQLite compares numeric text, so the invariant sees the CSV values.
    assert db["books"].count == 2

    buffer = io.StringIO("id,price\n3,4\n4,-1\n")
    failed = db.import_csv("books", buffer, safe_mode=True, pk="id")
    assert failed["success"] is False
    assert db["books"].count == 2

    payload = [{"id": 5, "price": 6}]
    assert db.import_json("books", payload, safe_mode=True, pk="id")["success"] is True
    json_path = str(tmpdir / "more.json")
    with open(json_path, "w", encoding="utf-8") as fh:
        json.dump([{"id": 6, "price": -3}], fh)
    failed_json = db.import_json("books", json_path, safe_mode=True, pk="id")
    assert failed_json["success"] is False
    assert 6 not in _ids(db["books"].rows)

    ndjson = io.StringIO('{"id": 7, "price": 2}\n{"id": 8, "price": 4}\n')
    assert db.import_json("books", ndjson, safe_mode=True, pk="id")["success"] is True
    assert db.import_json(
        "books", '{"id": 9, "price": 3}', safe_mode=True, pk="id"
    )["success"] is True

    plain = db.import_csv(
        "other", io.StringIO("id,price\n1,2\n"), safe_mode=False, pk="id"
    )
    assert plain == {"success": True}
    assert db["other"].count == 1


def test_safe_operations_require_enabled_mode():
    db = Database(memory=True)
    with pytest.raises(SafeImportNotEnabledError):
        db.safe_bulk_insert("books", [{"id": 1}])


def test_cli_safe_import_commands(tmpdir):
    path = str(tmpdir / "cli.db")
    runner = CliRunner()
    assert runner.invoke(cli, ["enable-safe-import", path]).exit_code == 0
    assert Database(path).safe_import_enabled() is True

    added = runner.invoke(
        cli, ["add-import-invariant", path, "books", "price > 0"]
    )
    assert added.exit_code == 0
    invariant_id = added.output.strip()
    assert invariant_id

    listed = runner.invoke(cli, ["list-import-invariants", path, "books"])
    assert listed.exit_code == 0
    assert invariant_id in listed.output
    assert "price > 0" in listed.output

    Database(path)["books"].insert({"id": 1, "price": 5}, pk="id")
    passed = runner.invoke(cli, ["validate-import-invariants", path, "books"])
    assert passed.exit_code == 0
    assert "pass" in passed.output.lower()
    assert "valid: true" in passed.output

    Database(path)["books"].insert({"id": 2, "price": -4}, pk="id")
    failed = runner.invoke(cli, ["validate-import-invariants", path, "books"])
    assert failed.exit_code == 0
    assert "fail" in failed.output.lower()
    assert invariant_id in failed.output

    # Put the table back into a valid state for the import commands.
    cleanup = Database(path)
    cleanup.execute("delete from books where id = 2")
    cleanup.conn.commit()

    json_path = str(tmpdir / "ok.json")
    with open(json_path, "w", encoding="utf-8") as fh:
        fh.write(json.dumps([{"id": 3, "price": 7}]))
    inserted = runner.invoke(
        cli, ["insert", path, "books", json_path, "--pk", "id", "--safe-mode"]
    )
    assert inserted.exit_code == 0
    assert 3 in _ids(Database(path)["books"].rows)

    bad_path = str(tmpdir / "bad.json")
    with open(bad_path, "w", encoding="utf-8") as fh:
        fh.write(json.dumps([{"id": 4, "price": -1}]))
    rejected = runner.invoke(
        cli, ["insert", path, "books", bad_path, "--pk", "id", "--safe-mode"]
    )
    assert rejected.exit_code != 0
    assert 4 not in _ids(Database(path)["books"].rows)

    csv_path = str(tmpdir / "upsert.csv")
    with open(csv_path, "w", encoding="utf-8") as fh:
        fh.write("id,price\n3,12\n")
    upserted = runner.invoke(
        cli,
        ["upsert", path, "books", csv_path, "--pk", "id", "--safe-mode"],
    )
    assert upserted.exit_code == 0
    assert Database(path)["books"].get(3)["price"] in (12, "12")

    update_path = str(tmpdir / "update.csv")
    with open(update_path, "w", encoding="utf-8") as fh:
        fh.write("id,price\n1,15\n3,-9\n")
    # batch-size 1 forces the first UPDATE to commit before the second is checked
    updated = runner.invoke(
        cli,
        [
            "bulk",
            path,
            "update books set price = :price where id = :id",
            update_path,
            "--safe-mode",
            "--batch-size",
            "1",
        ],
    )
    assert updated.exit_code != 0
    assert Database(path)["books"].get(1)["price"] in (5, "5")
    assert Database(path)["books"].get(3)["price"] in (12, "12")

    good_update = str(tmpdir / "good-update.csv")
    with open(good_update, "w", encoding="utf-8") as fh:
        fh.write("id,price\n1,6\n")
    applied = runner.invoke(
        cli,
        [
            "bulk",
            path,
            "update books set price = :price where id = :id",
            good_update,
            "--safe-mode",
        ],
    )
    assert applied.exit_code == 0
    assert Database(path)["books"].get(1)["price"] in (6, "6")

    removed = runner.invoke(
        cli, ["remove-import-invariant", path, "books", invariant_id]
    )
    assert removed.exit_code == 0
    assert Database(path).list_import_invariants("books") == []

    assert runner.invoke(cli, ["disable-safe-import", path]).exit_code == 0
    assert Database(path).safe_import_enabled() is False
    blocked = runner.invoke(
        cli, ["insert", path, "books", json_path, "--pk", "id", "--safe-mode"]
    )
    assert blocked.exit_code != 0
    assert "enabled" in blocked.output.lower()


def test_cli_help_includes_safe_mode():
    runner = CliRunner()
    for command in ("insert", "upsert", "bulk"):
        result = runner.invoke(cli, [command, "--help"])
        assert result.exit_code == 0
        assert "--safe-mode" in result.output
    for command in (
        "enable-safe-import",
        "disable-safe-import",
        "add-import-invariant",
        "remove-import-invariant",
        "list-import-invariants",
        "validate-import-invariants",
    ):
        result = runner.invoke(cli, [command, "--help"])
        assert result.exit_code == 0, result.output
        assert result.output.strip()
