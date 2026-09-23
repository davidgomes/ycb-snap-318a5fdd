import io
import json

import pytest
from click.testing import CliRunner

from sqlite_utils import Database, cli
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    ImportValidationError,
    SafeImportNotEnabledError,
)


def _schema(db):
    return list(
        db.execute(
            "SELECT type, name, sql FROM sqlite_master "
            "WHERE name NOT LIKE 'sqlite_%' "
            "AND name NOT LIKE '\\_sqlite_utils%' ESCAPE '\\' "
            "ORDER BY type, name"
        )
    )


def test_checkpoint_requires_safe_import():
    db = Database(memory=True)
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()


def test_enable_persists_across_connections(tmpdir):
    path = str(tmpdir / "data.db")
    db = Database(path)
    assert db.is_safe_import_enabled() is False
    db.enable_safe_import()
    assert db.is_safe_import_enabled() is True
    db.close()
    again = Database(path)
    assert again.is_safe_import_enabled() is True
    again.disable_safe_import()
    again.close()
    assert Database(path).is_safe_import_enabled() is False


def test_checkpoint_commit_rollback_and_cleanup():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    checkpoint_id = db.create_import_checkpoint()
    assert checkpoint_id
    db["dogs"].insert({"id": 2, "name": "Pancakes"}, pk="id")
    db.rollback_to_checkpoint(checkpoint_id)
    assert [row["id"] for row in db["dogs"].rows] == [1]
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(checkpoint_id)

    kept = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Suna"}, pk="id")
    db.commit_checkpoint(kept)
    assert [row["id"] for row in db["dogs"].rows] == [1, 3]
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(kept)

    forgotten = db.create_import_checkpoint()
    db["dogs"].insert({"id": 4, "name": "Lila"}, pk="id")
    db.cleanup_checkpoint(forgotten)
    assert [row["id"] for row in db["dogs"].rows] == [1, 3, 4]
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(forgotten)
    with pytest.raises(CheckpointNotFoundError):
        db.commit_checkpoint(forgotten)
    with pytest.raises(CheckpointNotFoundError):
        db.cleanup_checkpoint("missing")


def test_nested_checkpoints_restore_schema():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    db["dogs"].create_index(["name"])
    db.execute("CREATE TRIGGER dogs_touch AFTER UPDATE ON dogs " "BEGIN SELECT 1; END")
    before = _schema(db)
    outer = db.create_import_checkpoint()
    db["dogs"].add_column("age", int)
    db["dogs"].insert({"id": 2, "name": "Pancakes", "age": 4}, pk="id")
    inner = db.create_import_checkpoint()
    db["visits"].insert({"id": 1, "dog": 2}, pk="id")
    db.execute("CREATE INDEX idx_visits_dog ON visits(dog)")
    db.execute("CREATE TRIGGER visits_touch AFTER INSERT ON visits BEGIN SELECT 1; END")
    db.rollback_to_checkpoint(inner)
    assert "visits" not in db.table_names()
    assert "age" in db["dogs"].columns_dict
    assert [row["id"] for row in db["dogs"].rows] == [1, 2]
    db.rollback_to_checkpoint(outer)
    assert _schema(db) == before
    assert [row["id"] for row in db["dogs"].rows] == [1]
    assert "age" not in db["dogs"].columns_dict


def test_import_invariants_select_aggregate_and_row():
    db = Database(memory=True)
    db["dogs"].insert_all(
        [{"id": 1, "name": "Cleo", "age": 4}, {"id": 2, "name": "Pancakes", "age": 5}],
        pk="id",
    )
    select_id = db.add_import_invariant("dogs", "SELECT count(*) = 2 FROM dogs")
    aggregate_id = db.add_import_invariant("dogs", "sum(age) = 9")
    row_id = db.add_import_invariant("dogs", "age > 0")
    listed = db.list_import_invariants("dogs")
    assert [item["id"] for item in listed] == [select_id, aggregate_id, row_id]
    assert listed[1]["expression"] == "sum(age) = 9"
    assert db.validate_import_invariants("dogs")["valid"] is True

    db["dogs"].insert({"id": 3, "name": "Suna", "age": 1}, pk="id")
    result = db.validate_import_invariants("dogs")
    assert result["valid"] is False
    failed_ids = {failure["id"] for failure in result["failures"]}
    assert select_id in failed_ids
    assert aggregate_id in failed_ids
    assert row_id not in failed_ids
    for failure in result["failures"]:
        assert set(failure) == {"id", "expression", "error"}

    db["dogs"].delete_where("id = 3")
    db["dogs"].update(2, {"age": 0})
    result = db.validate_import_invariants("dogs")
    assert {failure["id"] for failure in result["failures"]} == {aggregate_id, row_id}

    db.remove_import_invariant("dogs", aggregate_id)
    assert [item["id"] for item in db.list_import_invariants("dogs")] == [
        select_id,
        row_id,
    ]


def test_empty_table_expression_shapes():
    db = Database(memory=True)
    db["dogs"].create({"age": int})
    assert db.validate_import_invariants("dogs")["valid"] is True
    db.add_import_invariant("dogs", "age > 0")
    db.add_import_invariant("dogs", "count(*) = 0")
    db.add_import_invariant("dogs", "(SELECT count(*) FROM dogs) = 0")
    assert db.validate_import_invariants("dogs")["valid"] is True
    db.add_import_invariant("dogs", "count(*) > 0")
    db.add_import_invariant("dogs", "(SELECT count(*) FROM dogs) > 0")
    result = db.validate_import_invariants("dogs")
    assert result["valid"] is False
    assert len(result["failures"]) == 2


def test_invariants_persist(tmpdir):
    path = str(tmpdir / "inv.db")
    db = Database(path)
    db["dogs"].insert({"id": 1, "age": 4}, pk="id")
    invariant_id = db.add_import_invariant("dogs", "age > 0")
    db.close()
    again = Database(path)
    listed = again.list_import_invariants("dogs")
    assert listed == [{"id": invariant_id, "expression": "age > 0"}]
    assert again.validate_import_invariants("dogs")["valid"] is True


def test_safe_bulk_insert_and_upsert():
    db = Database(memory=True)
    db["dogs"].insert({"id": 1, "name": "Cleo", "age": 4}, pk="id")
    db.add_import_invariant("dogs", "age > 0")
    before = _schema(db)
    failed = db.safe_bulk_insert(
        "dogs",
        [{"id": 2, "name": "Bad", "age": -1}],
        pk="id",
    )
    assert failed["success"] is False
    assert failed["checkpoint_id"]
    assert failed["failures"]
    assert "invariant" in failed["error_report"]
    assert "valid" in failed["error_report"]
    assert [row["id"] for row in db["dogs"].rows] == [1]
    assert _schema(db) == before

    altered = db.safe_bulk_insert(
        "dogs",
        [{"id": 3, "name": "Nope", "age": -2, "extra": "x"}],
        pk="id",
        alter=True,
    )
    assert altered["success"] is False
    assert "extra" not in db["dogs"].columns_dict
    assert [row["id"] for row in db["dogs"].rows] == [1]

    ok = db.safe_bulk_upsert(
        "dogs",
        [{"id": 1, "name": "Cleo updated", "age": 5}],
        pk="id",
    )
    assert ok == {"success": True}
    assert db["dogs"].get(1)["name"] == "Cleo updated"

    with pytest.raises(ImportValidationError) as exc_info:
        db.safe_bulk_insert(
            "dogs",
            [{"id": 4, "name": "Bad", "age": 0}],
            pk="id",
            strict=True,
        )
    message = str(exc_info.value).lower()
    assert "invariant" in message or "validation" in message or "valid" in message
    assert [row["id"] for row in db["dogs"].rows] == [1]

    broken = db.safe_bulk_insert(
        "dogs",
        [{"id": 1, "name": "dup", "age": 3}],
        pk="id",
    )
    assert broken["success"] is False
    assert broken["failures"] == []
    assert broken["error_report"]
    assert [row["name"] for row in db["dogs"].rows] == ["Cleo updated"]


def test_safe_import_rolls_back_new_table_trigger_and_index():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    db.add_import_invariant("dogs", "SELECT count(*) = 1 FROM dogs")
    before = _schema(db)

    def operation():
        db["dogs"].insert({"id": 2, "name": "Pancakes"}, pk="id")
        db["dogs"].create_index(["name"], index_name="idx_dogs_name_2")
        db.execute("CREATE TRIGGER dogs_ai AFTER INSERT ON dogs BEGIN SELECT 1; END")
        db["visits"].insert({"id": 1}, pk="id")

    checkpoint = db.create_import_checkpoint()
    try:
        operation()
        result = db.validate_import_invariants("dogs")
        assert result["valid"] is False
        db.rollback_to_checkpoint(checkpoint)
    except Exception:
        if db._checkpoint_is_active(checkpoint):
            db.rollback_to_checkpoint(checkpoint)
        raise
    assert _schema(db) == before
    assert "visits" not in db.table_names()
    assert "dogs" in db.table_names()


def test_import_csv_and_json(tmpdir):
    db = Database(memory=True)
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    db.add_import_invariant("dogs", "SELECT count(*) <= 3 FROM dogs")
    csv_path = tmpdir / "dogs.csv"
    csv_path.write_text("id,name\n2,Pancakes\n3,Suna\n4,Azi\n", encoding="utf-8")
    failed = db.import_csv("dogs", str(csv_path), safe_mode=True, pk="id")
    assert failed["success"] is False
    assert [row["id"] for row in db["dogs"].rows] == [1]

    handle = io.StringIO("id,name\n2,Pancakes\n")
    ok = db.import_csv("dogs", handle, safe_mode=True, pk="id")
    assert ok == {"success": True}
    assert [row["id"] for row in db["dogs"].rows] == [1, 2]

    payload = [{"id": 9, "name": "Too many"}, {"id": 10, "name": "Also"}]
    failed_json = db.import_json("dogs", payload, safe_mode=True, pk="id")
    assert failed_json["success"] is False
    assert failed_json["failures"]
    assert [row["id"] for row in db["dogs"].rows] == [1, 2]

    json_path = tmpdir / "one.json"
    json_path.write_text(json.dumps([{"id": 3, "name": "Suna"}]), encoding="utf-8")
    assert db.import_json("dogs", str(json_path), safe_mode=True, pk="id") == {
        "success": True
    }
    ndjson = io.StringIO('{"id": 4, "name": "A"}\n{"id": 5, "name": "B"}\n')
    too_many = db.import_json("dogs", ndjson, safe_mode=True, strict=False, pk="id")
    assert too_many["success"] is False
    assert [row["id"] for row in db["dogs"].rows] == [1, 2, 3]

    plain = db.import_json(
        "dogs", [{"id": 8, "name": "Skip"}], safe_mode=False, pk="id"
    )
    assert plain == {"success": True}


def test_cli_invariants_and_safe_mode(tmpdir):
    db_path = str(tmpdir / "dogs.db")
    db = Database(db_path)
    db["dogs"].insert({"id": 1, "name": "Cleo", "age": 4}, pk="id")
    db.close()
    runner = CliRunner()
    enabled = runner.invoke(cli.cli, ["enable-safe-import", db_path])
    assert enabled.exit_code == 0
    added = runner.invoke(cli.cli, ["add-import-invariant", db_path, "dogs", "age > 0"])
    assert added.exit_code == 0
    invariant_id = added.output.strip()
    assert invariant_id
    listed = runner.invoke(cli.cli, ["list-import-invariants", db_path, "dogs"])
    assert listed.exit_code == 0
    assert invariant_id in listed.output
    assert "age > 0" in listed.output
    passed = runner.invoke(cli.cli, ["validate-import-invariants", db_path, "dogs"])
    assert passed.exit_code == 0
    assert "pass" in passed.output.lower()

    bad_csv = tmpdir / "bad.csv"
    bad_csv.write_text("id,name,age\n2,Bad,-1\n", encoding="utf-8")
    failed = runner.invoke(
        cli.cli,
        ["insert", db_path, "dogs", str(bad_csv), "--safe-mode", "--pk", "id"],
    )
    assert failed.exit_code != 0
    assert "invariant" in failed.output.lower() or "valid" in failed.output.lower()
    assert [row["id"] for row in Database(db_path)["dogs"].rows] == [1]

    good = tmpdir / "good.json"
    good.write_text(
        json.dumps([{"id": 2, "name": "Pancakes", "age": 5}]), encoding="utf-8"
    )
    inserted = runner.invoke(
        cli.cli,
        ["upsert", db_path, "dogs", str(good), "--safe-mode", "--pk", "id"],
    )
    assert inserted.exit_code == 0
    assert [row["id"] for row in Database(db_path)["dogs"].rows] == [1, 2]

    updates = tmpdir / "updates.csv"
    updates.write_text("id,name\n2,Snowy\n", encoding="utf-8")
    updated = runner.invoke(
        cli.cli,
        [
            "bulk",
            db_path,
            "update dogs set name = :name where id = :id",
            str(updates),
            "--safe-mode",
        ],
    )
    assert updated.exit_code == 0, updated.output
    assert Database(db_path)["dogs"].get(2)["name"] == "Snowy"

    bad_updates = tmpdir / "bad-updates.csv"
    bad_updates.write_text("id,age\n2,-5\n", encoding="utf-8")
    rejected = runner.invoke(
        cli.cli,
        [
            "bulk",
            db_path,
            "update dogs set age = :age where id = :id",
            str(bad_updates),
            "--safe-mode",
        ],
    )
    assert rejected.exit_code != 0
    assert Database(db_path)["dogs"].get(2)["age"] == 5

    invalid = runner.invoke(
        cli.cli,
        ["validate-import-invariants", db_path, "dogs"],
    )
    assert invalid.exit_code == 0
    broken = Database(db_path)
    broken.execute("update dogs set age = -1 where id = 2")
    broken.conn.commit()
    broken.close()
    invalid = runner.invoke(cli.cli, ["validate-import-invariants", db_path, "dogs"])
    assert invalid.exit_code == 0
    assert "fail" in invalid.output.lower()
    assert invariant_id in invalid.output

    removed = runner.invoke(
        cli.cli,
        ["remove-import-invariant", db_path, "dogs", invariant_id],
    )
    assert removed.exit_code == 0
    assert (
        runner.invoke(
            cli.cli, ["list-import-invariants", db_path, "dogs"]
        ).output.strip()
        == ""
    )
    disabled = runner.invoke(cli.cli, ["disable-safe-import", db_path])
    assert disabled.exit_code == 0
    assert Database(db_path).is_safe_import_enabled() is False
