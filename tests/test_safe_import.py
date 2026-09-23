import io
import json

import pytest
from click.testing import CliRunner

from sqlite_utils import Database
from sqlite_utils.cli import cli
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    ImportValidationError,
    SafeImportNotEnabledError,
)


def _schema_objects(db):
    return db.execute(
        "select type, name, sql from sqlite_master "
        "where name not like '\\_sqlite\\_utils\\_%' escape '\\' "
        "order by type, name"
    ).fetchall()


def test_checkpoint_requires_safe_import():
    db = Database(memory=True)
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()
    db.enable_safe_import()
    checkpoint_id = db.create_import_checkpoint()
    assert checkpoint_id
    db.rollback_to_checkpoint(checkpoint_id)


def test_checkpoint_lifecycle_and_nesting():
    db = Database(memory=True)
    db.enable_safe_import()
    outer = db.create_import_checkpoint()
    inner = db.create_import_checkpoint()
    assert outer and inner and outer != inner

    db.commit_checkpoint(inner)
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(inner)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)

    db.rollback_to_checkpoint(outer)
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(outer)

    db.cleanup_checkpoint(outer)
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(outer)
    with pytest.raises(CheckpointNotFoundError):
        db.cleanup_checkpoint(outer)
    with pytest.raises(CheckpointNotFoundError):
        db.commit_checkpoint("missing")

    active = db.create_import_checkpoint()
    db.cleanup_checkpoint(active)
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(active)


def test_executescript_during_checkpoint_rolls_back_schema():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    before_schema = _schema_objects(db)
    checkpoint_id = db.create_import_checkpoint()
    db.executescript(
        "create table puppies (id integer primary key, name text);"
        "create index idx_puppies_name on puppies (name);"
        "create trigger puppies_touch after update on puppies "
        "begin update puppies set name = name where id = new.id; end;"
        "insert into puppies (id, name) values (1, 'Newbie');"
    )
    assert db["puppies"].exists()
    db.rollback_to_checkpoint(checkpoint_id)
    assert not db["puppies"].exists()
    assert _schema_objects(db) == before_schema
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Cleo"}]


def test_rollback_restores_rows_schema_indexes_and_triggers():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    before_rows = list(db["dogs"].rows)
    before_schema = _schema_objects(db)

    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].add_column("age", int)
    db["dogs"].insert({"id": 2, "name": "Pancakes", "age": 4}, alter=True)
    db["dogs"].create_index(["name"], index_name="idx_dogs_name")
    db.execute(
        "create trigger dogs_touch after update on dogs "
        "begin update dogs set name = name where id = new.id; end"
    )
    db["puppies"].insert({"id": 1, "name": "Newbie"}, pk="id")
    assert "age" in db["dogs"].columns_dict
    assert db["puppies"].exists()

    db.rollback_to_checkpoint(checkpoint_id)

    assert list(db["dogs"].rows) == before_rows
    assert _schema_objects(db) == before_schema
    assert not db["puppies"].exists()
    assert "age" not in db["dogs"].columns_dict


def test_nested_rollback_keeps_outer_changes():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    outer = db.create_import_checkpoint()
    db["dogs"].insert({"id": 2, "name": "Pancakes"}, pk="id")
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Toby"}, pk="id")
    db.rollback_to_checkpoint(inner)
    assert {row["id"] for row in db["dogs"].rows} == {1, 2}
    db.commit_checkpoint(outer)
    assert {row["id"] for row in db["dogs"].rows} == {1, 2}


def test_invariants_persist_and_validate(tmp_path):
    db = Database(tmp_path / "dogs.db")
    db["dogs"].insert_all(
        [{"id": 1, "name": "Cleo", "age": 4}, {"id": 2, "name": "", "age": 1}],
        pk="id",
    )
    count_id = db.add_import_invariant("dogs", "count(*) = 2")
    select_id = db.add_import_invariant("dogs", "select count(*) = 2 from dogs")
    row_id = db.add_import_invariant("dogs", "age > 0")
    bad_id = db.add_import_invariant("dogs", "length(name) > 0")
    listed = db.list_import_invariants("dogs")
    assert [item["id"] for item in listed] == [count_id, select_id, row_id, bad_id]
    assert listed[0]["expression"] == "count(*) = 2"

    result = db.validate_import_invariants("dogs")
    assert result["valid"] is False
    failed_ids = {failure["id"] for failure in result["failures"]}
    assert failed_ids == {bad_id}
    assert result["failures"][0]["expression"] == "length(name) > 0"
    assert result["failures"][0]["error"]

    db.remove_import_invariant("dogs", bad_id)
    assert db.validate_import_invariants("dogs")["valid"] is True

    # A fresh connection sees the same invariants.
    db.close()
    persisted = Database(tmp_path / "dogs.db")
    assert [item["id"] for item in persisted.list_import_invariants("dogs")] == [
        count_id,
        select_id,
        row_id,
    ]
    persisted.close()


def test_safe_bulk_insert_rolls_back_partial_batches_and_schema():
    db = Database(memory=True)
    db.enable_safe_import()
    db.add_import_invariant("dogs", "count(*) <= 1")
    result = db.safe_bulk_insert(
        "dogs",
        [{"id": 1, "name": "Cleo"}, {"id": 2, "name": "Pancakes"}],
        pk="id",
        batch_size=1,
        strict=False,
    )
    assert result["success"] is False
    assert result["checkpoint_id"]
    assert result["failures"]
    assert "invariant" in result["error_report"]
    assert "valid" in result["error_report"].lower()
    assert not db["dogs"].exists()
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(result["checkpoint_id"])


def test_safe_bulk_insert_sql_error_has_empty_failures():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    result = db.safe_bulk_insert(
        "dogs",
        [{"id": 2, "name": "Pancakes"}, {"id": 1, "name": "Other"}],
        pk="id",
        batch_size=1,
    )
    assert result["success"] is False
    assert result["failures"] == []
    assert result["error_report"]
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Cleo"}]


def test_safe_bulk_insert_commits_on_success():
    db = Database(memory=True)
    db.enable_safe_import()
    db.add_import_invariant("dogs", "count(*) = 2")
    result = db.safe_bulk_insert(
        "dogs",
        [{"id": 1, "name": "Cleo"}, {"id": 2, "name": "Pancakes"}],
        pk="id",
    )
    assert result == {"success": True}
    assert {row["id"] for row in db["dogs"].rows} == {1, 2}


def test_strict_invariant_failure_raises_after_rollback():
    db = Database(memory=True)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    db.add_import_invariant("dogs", "count(*) < 1")
    with pytest.raises(ImportValidationError) as exc_info:
        db.safe_bulk_upsert(
            "dogs",
            [{"id": 2, "name": "Pancakes"}],
            pk="id",
            strict=True,
        )
    message = str(exc_info.value).lower()
    assert "valid" in message or "validation" in message or "invariant" in message
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Cleo"}]


def test_import_csv_and_json(tmp_path):
    db = Database(memory=True)
    db.enable_safe_import()
    db.add_import_invariant("dogs", "count(*) = 1")
    csv_path = tmp_path / "dogs.csv"
    csv_path.write_text("id,name\n1,Cleo\n2,Pancakes\n", encoding="utf-8")
    failed = db.import_csv("dogs", csv_path, safe_mode=True)
    assert failed["success"] is False
    assert not db["dogs"].exists()

    ok = db.import_csv(
        "dogs",
        io.StringIO("id,name\n1,Cleo\n"),
        safe_mode=True,
        pk="id",
    )
    assert ok == {"success": True}
    assert list(db["dogs"].rows) == [{"id": "1", "name": "Cleo"}]

    db["cats"].insert({"id": 1, "name": "Pancakes"}, pk="id")
    updated = db.import_json(
        "cats",
        json.dumps([{"id": 1, "name": "Binx"}]),
        safe_mode=False,
        pk="id",
        replace=True,
    )
    assert updated == {"success": True}
    assert list(db["cats"].rows) == [{"id": 1, "name": "Binx"}]

    db.add_import_invariant("birds", "count(*) < 1")
    denied = db.import_json("birds", [{"id": 1, "name": "Robin"}], safe_mode=True)
    assert denied["success"] is False
    assert not db["birds"].exists()


def test_disable_blocks_new_checkpoints_but_keeps_invariants():
    db = Database(memory=True)
    db.enable_safe_import()
    invariant_id = db.add_import_invariant("dogs", "count(*) >= 0")
    db.disable_safe_import()
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()
    assert db.list_import_invariants("dogs")[0]["id"] == invariant_id


def test_cli_safe_import_commands(tmp_path):
    db_path = tmp_path / "data.db"
    runner = CliRunner()

    enabled = runner.invoke(cli, ["enable-safe-import", str(db_path)])
    assert enabled.exit_code == 0, enabled.output

    added = runner.invoke(
        cli,
        ["add-import-invariant", str(db_path), "dogs", "length(name) > 0"],
    )
    assert added.exit_code == 0, added.output
    invariant_id = added.output.strip()
    assert invariant_id

    listed = runner.invoke(cli, ["list-import-invariants", str(db_path), "dogs"])
    assert listed.exit_code == 0
    assert invariant_id in listed.output
    assert "length(name) > 0" in listed.output

    empty_check = runner.invoke(
        cli, ["validate-import-invariants", str(db_path), "dogs"]
    )
    assert empty_check.exit_code == 0
    assert "fail" in empty_check.output
    assert invariant_id in empty_check.output

    blocked = runner.invoke(
        cli,
        ["insert", str(db_path), "dogs", "-", "--safe-mode", "--csv"],
        input="id,name\n1,\n",
    )
    assert blocked.exit_code != 0
    assert "valid" in blocked.output.lower() or "invariant" in blocked.output.lower()
    db = Database(db_path)
    assert not db["dogs"].exists()

    allowed = runner.invoke(
        cli,
        ["insert", str(db_path), "dogs", "-", "--pk", "id", "--safe-mode"],
        input='[{"id": 1, "name": "Cleo"}]',
    )
    assert allowed.exit_code == 0, allowed.output
    db = Database(db_path)
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Cleo"}]

    passing = runner.invoke(cli, ["validate-import-invariants", str(db_path), "dogs"])
    assert passing.exit_code == 0
    assert "pass" in passing.output

    updated = runner.invoke(
        cli,
        [
            "bulk",
            str(db_path),
            "update dogs set name = :name where id = :id",
            "-",
            "--safe-mode",
        ],
        input='[{"id": 1, "name": ""}]',
    )
    assert updated.exit_code != 0, updated.output
    db = Database(db_path)
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Cleo"}]

    renamed = runner.invoke(
        cli,
        [
            "upsert",
            str(db_path),
            "dogs",
            "-",
            "--pk",
            "id",
            "--safe-mode",
            "--nl",
        ],
        input='{"id": 1, "name": "Pancakes"}\n',
    )
    assert renamed.exit_code == 0, renamed.output
    db = Database(db_path)
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Pancakes"}]

    removed = runner.invoke(
        cli,
        ["remove-import-invariant", str(db_path), "dogs", invariant_id],
    )
    assert removed.exit_code == 0, removed.output
    listed_after = runner.invoke(cli, ["list-import-invariants", str(db_path), "dogs"])
    assert invariant_id not in listed_after.output

    disabled = runner.invoke(cli, ["disable-safe-import", str(db_path)])
    assert disabled.exit_code == 0, disabled.output
    refused = runner.invoke(
        cli,
        ["insert", str(db_path), "dogs", "-", "--safe-mode"],
        input='{"id": 3, "name": "Toby"}',
    )
    assert refused.exit_code != 0


def test_cli_safe_mode_infers_csv(tmp_path):
    db_path = tmp_path / "data.db"
    runner = CliRunner()
    assert runner.invoke(cli, ["enable-safe-import", str(db_path)]).exit_code == 0
    result = runner.invoke(
        cli,
        ["insert", str(db_path), "dogs", "-", "--safe-mode", "--pk", "id"],
        input="id,name\n1,Cleo\n",
    )
    assert result.exit_code == 0, result.output
    db = Database(db_path)
    assert list(db["dogs"].rows) == [{"id": 1, "name": "Cleo"}]
