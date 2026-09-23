import io
import pytest
from click.testing import CliRunner

from sqlite_utils import Database
from sqlite_utils.cli import cli
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    ImportInvariantError,
    SafeImportNotEnabledError,
)


@pytest.fixture
def db():
    database = Database(memory=True)
    database.enable_safe_import()
    return database


def test_checkpoint_requires_safe_import(tmp_path):
    database = Database(tmp_path / "plain.db")
    with pytest.raises(SafeImportNotEnabledError):
        database.create_import_checkpoint()


def test_checkpoint_commit_rollback_and_cleanup(db):
    db["dogs"].insert({"id": 1, "name": "Cleo"})
    outer = db.create_import_checkpoint()
    assert outer
    db["dogs"].insert({"id": 2, "name": "Pancakes"})
    inner = db.create_import_checkpoint()
    db["dogs"].add_column("age", int)
    db.execute("CREATE INDEX idx_dogs_name ON dogs(name)")
    db.execute(
        "CREATE TRIGGER dogs_insert AFTER INSERT ON dogs "
        "BEGIN SELECT 1; END"
    )
    db["dogs"].insert({"id": 3, "name": "Suna", "age": 4})
    db.rollback_to_checkpoint(inner)
    assert "age" not in db["dogs"].columns_dict
    assert [row["id"] for row in db["dogs"].rows] == [1, 2]
    assert db["dogs"].indexes == []
    assert db.triggers == []
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(inner)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)
    db.commit_checkpoint(outer)
    assert [row["id"] for row in db["dogs"].rows] == [1, 2]
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(outer)
    db.cleanup_checkpoint(outer)
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(outer)
    with pytest.raises(CheckpointNotFoundError):
        db.cleanup_checkpoint("missing")


def test_nested_rollback_of_outer_checkpoint(db):
    db["dogs"].insert({"id": 1})
    outer = db.create_import_checkpoint()
    db["dogs"].insert({"id": 2})
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3})
    db.rollback_to_checkpoint(outer)
    assert [row["id"] for row in db["dogs"].rows] == [1]
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(inner)


def test_invariants_persist_and_evaluate(tmp_path):
    path = tmp_path / "dogs.db"
    database = Database(path)
    database["dogs"].insert_all(
        [{"name": "Cleo", "age": 4}, {"name": "Pancakes", "age": 2}]
    )
    positive = database.add_import_invariant("dogs", "age > 0")
    database.add_import_invariant("dogs", "COUNT(*) = 2")
    database.add_import_invariant("dogs", "SELECT COUNT(*) = 2 FROM dogs")
    listed = database.list_import_invariants("dogs")
    assert [item["id"] for item in listed][0] == positive
    assert listed[1]["expression"] == "COUNT(*) = 2"
    assert database.validate_import_invariants("dogs")["valid"] is True
    database["dogs"].insert({"name": "Bad", "age": 0})
    result = database.validate_import_invariants("dogs")
    assert result["valid"] is False
    assert positive in [failure["id"] for failure in result["failures"]]
    database.remove_import_invariant("dogs", positive)
    assert positive not in [
        item["id"] for item in database.list_import_invariants("dogs")
    ]
    reopened = Database(path)
    assert len(reopened.list_import_invariants("dogs")) == 2


def test_safe_bulk_insert_and_upsert(db):
    db.add_import_invariant("dogs", "age > 0")
    ok = db.safe_bulk_insert(
        "dogs", [{"name": "Cleo", "age": 4}], pk="name"
    )
    assert ok == {"success": True}
    bad = db.safe_bulk_insert(
        "dogs", [{"name": "Nope", "age": 0}], strict=False
    )
    assert bad["success"] is False
    assert bad["checkpoint_id"]
    assert bad["failures"]
    assert "invariant" in bad["error_report"]
    assert [row["name"] for row in db["dogs"].rows] == ["Cleo"]
    with pytest.raises(ImportInvariantError, match="valid|invariant"):
        db.safe_bulk_insert("dogs", [{"name": "Nope", "age": 0}], strict=True)
    assert [row["name"] for row in db["dogs"].rows] == ["Cleo"]
    broken = db.safe_bulk_insert("dogs", [{"missing": object()}], strict=False)
    assert broken["success"] is False
    assert broken["failures"] == []
    assert broken["error_report"]
    updated = db.safe_bulk_upsert(
        "dogs", [{"name": "Cleo", "age": 5}], pk="name", strict=False
    )
    assert updated["success"] is True
    assert db["dogs"].get("Cleo")["age"] == 5


def test_safe_import_rolls_back_schema(db):
    db["dogs"].insert({"id": 1, "name": "Cleo"})
    db.add_import_invariant("dogs", "name != 'new-col'")
    result = db.safe_bulk_insert(
        "dogs",
        [{"id": 2, "name": "new-col", "extra": "x"}],
        pk="id",
        alter=True,
        strict=False,
    )
    assert result["success"] is False
    assert "extra" not in db["dogs"].columns_dict
    assert [row["id"] for row in db["dogs"].rows] == [1]


def test_import_csv_and_json(db, tmp_path):
    db.add_import_invariant("dogs", "name != ''")
    csv_path = tmp_path / "dogs.csv"
    csv_path.write_text("name,age\nCleo,4\n,1\n", encoding="utf-8")
    failed = db.import_csv("dogs", str(csv_path), safe_mode=True, strict=False)
    assert failed["success"] is False
    assert "dogs" not in db.table_names()
    handle = io.StringIO("name,age\nCleo,4\n")
    assert db.import_csv("dogs", handle, safe_mode=True)["success"] is True
    with pytest.raises(ImportInvariantError, match="invariant"):
        db.import_json(
            "dogs",
            [{"name": "", "age": 1}],
            safe_mode=True,
            strict=True,
        )
    assert db.import_json(
        "dogs", [{"name": "Pancakes", "age": 2}], safe_mode=False
    )["success"] is True
    assert {row["name"] for row in db["dogs"].rows} == {"Cleo", "Pancakes"}


def test_cli_safe_import(tmp_path):
    runner = CliRunner()
    db_path = tmp_path / "data.db"
    Database(db_path).enable_safe_import()
    csv_path = tmp_path / "dogs.csv"
    csv_path.write_text("name,age\nCleo,4\n", encoding="utf-8")
    added = runner.invoke(
        cli,
        ["add-import-invariant", str(db_path), "dogs", "age != '0'"],
    )
    assert added.exit_code == 0, added.output
    invariant_id = added.output.strip()
    listed = runner.invoke(
        cli, ["list-import-invariants", str(db_path), "dogs"]
    )
    assert listed.exit_code == 0
    assert invariant_id in listed.output
    assert "age != '0'" in listed.output
    inserted = runner.invoke(
        cli,
        [
            "insert",
            str(db_path),
            "dogs",
            str(csv_path),
            "--safe-mode",
        ],
    )
    assert inserted.exit_code == 0, inserted.output
    bad_csv = tmp_path / "bad.csv"
    bad_csv.write_text("name,age\nNope,0\n", encoding="utf-8")
    rejected = runner.invoke(
        cli,
        ["insert", str(db_path), "dogs", str(bad_csv), "--safe-mode", "--csv"],
    )
    assert rejected.exit_code != 0
    database = Database(db_path)
    assert [row["name"] for row in database["dogs"].rows] == ["Cleo"]
    validated = runner.invoke(
        cli, ["validate-import-invariants", str(db_path), "dogs"]
    )
    assert validated.exit_code == 0
    assert "pass" in validated.output
    database["dogs"].insert({"name": "Zero", "age": "0"})
    failed = runner.invoke(
        cli, ["validate-import-invariants", str(db_path), "dogs"]
    )
    assert failed.exit_code == 0
    assert "fail" in failed.output
    assert invariant_id in failed.output
    updates = tmp_path / "updates.json"
    updates.write_text(
        '[{"name": "Cleo", "age": "0"}]',
        encoding="utf-8",
    )
    bulk = runner.invoke(
        cli,
        [
            "bulk",
            str(db_path),
            "update dogs set age = :age where name = :name",
            str(updates),
            "--safe-mode",
        ],
    )
    assert bulk.exit_code != 0, bulk.output
    cleo = [
        row
        for row in Database(db_path)["dogs"].rows
        if row["name"] == "Cleo"
    ][0]
    assert str(cleo["age"]) == "4"
    removed = runner.invoke(
        cli,
        ["remove-import-invariant", str(db_path), "dogs", invariant_id],
    )
    assert removed.exit_code == 0
    runner.invoke(cli, ["disable-safe-import", str(db_path)])
    assert Database(db_path).safe_import_enabled() is False
    runner.invoke(cli, ["enable-safe-import", str(db_path)])
    assert Database(db_path).safe_import_enabled() is True
