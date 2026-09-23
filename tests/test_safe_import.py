from sqlite_utils import Database
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    ImportValidationError,
    NotFoundError,
    SafeImportNotEnabledError,
)
from sqlite_utils.utils import sqlite3
import glob
import io
import json
import os
import pytest
import tempfile


@pytest.fixture(params=["memory", "file", "wal"])
def db(request, tmpdir):
    if request.param == "memory":
        database = Database(memory=True)
    else:
        database = Database(str(tmpdir / "safe.db"))
        if request.param == "wal":
            database.enable_wal()
    database.enable_safe_import()
    database["dogs"].insert_all(
        [{"id": 1, "name": "Cleo", "age": 5}, {"id": 2, "name": "Pancakes", "age": 3}],
        pk="id",
    )
    return database


def rows(db, table="dogs"):
    return list(db[table].rows)


def test_safe_import_enabled_persists(tmpdir):
    path = str(tmpdir / "persist.db")
    db = Database(path)
    assert not db.safe_import_enabled
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()
    db.enable_safe_import()
    assert db.safe_import_enabled
    db.close()
    db2 = Database(path)
    assert db2.safe_import_enabled
    assert db2.create_import_checkpoint()
    db2.disable_safe_import()
    assert not db2.safe_import_enabled
    with pytest.raises(SafeImportNotEnabledError):
        db2.create_import_checkpoint()


def test_disable_safe_import_on_fresh_database_creates_nothing(fresh_db):
    fresh_db.disable_safe_import()
    assert fresh_db.table_names() == []


def test_rollback_restores_data_and_schema(db):
    before_schema = db.schema
    before_rows = rows(db)
    checkpoint_id = db.create_import_checkpoint()
    assert isinstance(checkpoint_id, str) and checkpoint_id
    db["dogs"].insert({"id": 3, "name": "Bailey", "age": 1})
    db["dogs"].update(1, {"age": 50})
    db["dogs"].delete(2)
    db["dogs"].add_column("weight", float)
    db["dogs"].create_index(["name"])
    db.execute("create trigger dogs_ai after insert on dogs begin select 1; end")
    db["cats"].insert({"id": 1, "name": "Tom"})
    db["dogs"].transform(types={"age": str})
    db.rollback_to_checkpoint(checkpoint_id)
    assert db.schema == before_schema
    assert rows(db) == before_rows
    assert db["dogs"].indexes == []
    assert db.triggers == []
    assert "cats" not in db.table_names()
    assert db["dogs"].columns_dict == {"id": int, "name": str, "age": int}


def test_rollback_discards_uncommitted_changes(db):
    checkpoint_id = db.create_import_checkpoint()
    db.execute("insert into dogs (id, name, age) values (3, 'Pending', 1)")
    assert db.conn.in_transaction
    db.rollback_to_checkpoint(checkpoint_id)
    assert [r["id"] for r in rows(db)] == [1, 2]


def test_create_checkpoint_commits_pending_transaction(db):
    db.execute("insert into dogs (id, name, age) values (3, 'Pending', 1)")
    checkpoint_id = db.create_import_checkpoint()
    assert not db.conn.in_transaction
    db["dogs"].insert({"id": 4, "name": "Later", "age": 1})
    db.rollback_to_checkpoint(checkpoint_id)
    assert [r["id"] for r in rows(db)] == [1, 2, 3]


def test_rollback_is_visible_to_other_connections(tmpdir):
    path = str(tmpdir / "shared.db")
    db = Database(path)
    db.enable_safe_import()
    db["dogs"].insert({"id": 1}, pk="id")
    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].insert({"id": 2})
    db["new_table"].insert({"id": 1})
    db.rollback_to_checkpoint(checkpoint_id)
    other = sqlite3.connect(path)
    assert other.execute("select id from dogs").fetchall() == [(1,)]
    assert other.execute(
        "select count(*) from sqlite_master where name = 'new_table'"
    ).fetchone() == (0,)
    other.close()


def test_commit_checkpoint_keeps_changes(db):
    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Bailey", "age": 1})
    db.execute("insert into dogs (id, name, age) values (4, 'Pending', 1)")
    db.commit_checkpoint(checkpoint_id)
    assert not db.conn.in_transaction
    assert [r["id"] for r in rows(db)] == [1, 2, 3, 4]


@pytest.mark.parametrize("finalize", ["commit_checkpoint", "rollback_to_checkpoint"])
@pytest.mark.parametrize("again", ["commit_checkpoint", "rollback_to_checkpoint"])
def test_finalized_checkpoint_is_not_active(db, finalize, again):
    checkpoint_id = db.create_import_checkpoint()
    getattr(db, finalize)(checkpoint_id)
    with pytest.raises(CheckpointNotActiveError):
        getattr(db, again)(checkpoint_id)


@pytest.mark.parametrize(
    "method", ["commit_checkpoint", "rollback_to_checkpoint", "cleanup_checkpoint"]
)
def test_unknown_checkpoint(db, method):
    with pytest.raises(CheckpointNotFoundError):
        getattr(db, method)("does-not-exist")


@pytest.mark.parametrize(
    "method", ["commit_checkpoint", "rollback_to_checkpoint", "cleanup_checkpoint"]
)
def test_cleanup_checkpoint_removes_id(db, method):
    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Bailey", "age": 1})
    db.cleanup_checkpoint(checkpoint_id)
    # Cleaning up leaves the database as it is
    assert [r["id"] for r in rows(db)] == [1, 2, 3]
    with pytest.raises(CheckpointNotFoundError):
        getattr(db, method)(checkpoint_id)


def test_nested_checkpoints(db):
    outer = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Outer", "age": 1})
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 4, "name": "Inner", "age": 1})
    db["dogs"].add_column("inner_column")
    db.rollback_to_checkpoint(inner)
    assert [r["id"] for r in rows(db)] == [1, 2, 3]
    assert "inner_column" not in db["dogs"].columns_dict
    inner2 = db.create_import_checkpoint()
    db["dogs"].insert({"id": 5, "name": "Inner 2", "age": 1})
    db.commit_checkpoint(inner2)
    db.rollback_to_checkpoint(outer)
    assert [r["id"] for r in rows(db)] == [1, 2]


def test_rolling_back_outer_checkpoint_finalizes_nested_ones(db):
    outer = db.create_import_checkpoint()
    inner = db.create_import_checkpoint()
    db.rollback_to_checkpoint(outer)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)


def test_committing_outer_checkpoint_finalizes_nested_ones(db):
    outer = db.create_import_checkpoint()
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Bailey", "age": 1})
    db.commit_checkpoint(outer)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)
    assert [r["id"] for r in rows(db)] == [1, 2, 3]


def test_checkpoint_snapshot_files_are_removed(tmpdir):
    db = Database(str(tmpdir / "files.db"))
    db.enable_safe_import()
    pattern = os.path.join(tempfile.gettempdir(), "sqlite-utils-checkpoint-*")
    before = set(glob.glob(pattern))
    rolled_back = db.create_import_checkpoint()
    committed = db.create_import_checkpoint()
    db.create_import_checkpoint()
    assert len(set(glob.glob(pattern)) - before) == 3
    db.commit_checkpoint(committed)
    db.rollback_to_checkpoint(rolled_back)
    db.create_import_checkpoint()
    db.close()
    assert set(glob.glob(pattern)) - before == set()


def test_add_list_remove_import_invariants(tmpdir):
    path = str(tmpdir / "invariants.db")
    db = Database(path)
    first = db.add_import_invariant("dogs", "age >= 0")
    second = db.add_import_invariant("dogs", "  SELECT count(*) > 0 FROM dogs;  ")
    db.add_import_invariant("cats", "name is not null")
    assert first != second
    db.close()
    db = Database(path)
    assert db.list_import_invariants("dogs") == [
        {"id": first, "expression": "age >= 0"},
        {"id": second, "expression": "SELECT count(*) > 0 FROM dogs"},
    ]
    db.remove_import_invariant("dogs", first)
    assert db.list_import_invariants("dogs") == [
        {"id": second, "expression": "SELECT count(*) > 0 FROM dogs"}
    ]
    with pytest.raises(NotFoundError):
        db.remove_import_invariant("dogs", first)
    with pytest.raises(NotFoundError):
        db.remove_import_invariant("cats", second)
    with pytest.raises(ValueError):
        db.add_import_invariant("dogs", "   ")


def test_list_import_invariants_without_table(fresh_db):
    assert fresh_db.list_import_invariants("dogs") == []
    assert fresh_db.validate_import_invariants("dogs") == {
        "valid": True,
        "failures": [],
    }
    assert fresh_db.table_names() == []


@pytest.mark.parametrize(
    "expression,valid",
    (
        # Row-level expressions must hold for every row
        ("age >= 0", True),
        ("age > 3", False),
        ("max(age, 4) >= 4", True),
        ("weight > 0", False),
        # Aggregates are evaluated once for the whole table
        ("count(*) = 3", True),
        ("count(*) > 3", False),
        ("sum(age) = 8", True),
        ("avg(age) > 10", False),
        ("MIN(age) >= 0 AND MAX(age) < 10", True),
        # SELECT queries use the first column of the first row
        ("SELECT count(*) FROM dogs", True),
        ("select count(*) from dogs where age > 100", False),
        ("SELECT name FROM dogs WHERE id = 99", False),
        ("SELECT '0'", False),
        ("SELECT 1, 0", True),
        ("SELECT 0, 1", False),
        ("with t as (select 1 as v) select v from t", True),
        # Errors are failures
        ("no_such_column > 1", False),
        ("SELECT * FROM no_such_table", False),
    ),
)
def test_validate_import_invariants(fresh_db, expression, valid):
    fresh_db["dogs"].insert_all(
        [
            {"id": 1, "age": 5, "weight": 2.5},
            {"id": 2, "age": 3, "weight": None},
            {"id": 3, "age": 0, "weight": 1.0},
        ],
        pk="id",
    )
    invariant_id = fresh_db.add_import_invariant("dogs", expression)
    result = fresh_db.validate_import_invariants("dogs")
    assert result["valid"] is valid
    if valid:
        assert result["failures"] == []
    else:
        assert len(result["failures"]) == 1
        failure = result["failures"][0]
        assert set(failure) == {"id", "expression", "error"}
        assert failure["id"] == invariant_id
        assert failure["expression"] == expression
        assert failure["error"]


def test_validate_import_invariants_error_messages(fresh_db):
    fresh_db["dogs"].insert_all([{"id": 1, "age": -1}, {"id": 2, "age": -2}])
    fresh_db.add_import_invariant("dogs", "age >= 0")
    fresh_db.add_import_invariant("dogs", "count(*) > 5")
    fresh_db.add_import_invariant("dogs", "bad_column = 1")
    errors = [
        f["error"] for f in fresh_db.validate_import_invariants("dogs")["failures"]
    ]
    assert errors == [
        "2 rows did not satisfy the expression",
        "Expression evaluated to 0",
        "no such column: bad_column",
    ]


def test_aggregate_invariant_on_empty_table(fresh_db):
    fresh_db["dogs"].create({"id": int})
    fresh_db.add_import_invariant("dogs", "id > 0")
    assert fresh_db.validate_import_invariants("dogs")["valid"]
    fresh_db.add_import_invariant("dogs", "count(*) > 0")
    assert not fresh_db.validate_import_invariants("dogs")["valid"]


def test_safe_bulk_insert_success(db):
    db.add_import_invariant("dogs", "age >= 0")
    result = db.safe_bulk_insert(
        "dogs",
        [{"id": 3, "name": "Bailey", "age": 1}, {"id": 4, "name": "Ro", "age": 2}],
    )
    assert result == {"success": True}
    assert [r["id"] for r in rows(db)] == [1, 2, 3, 4]
    # The checkpoint used for the import has been cleaned up
    assert db._import_checkpoints == {}


def test_safe_bulk_insert_invariant_failure_rolls_back(db):
    invariant_id = db.add_import_invariant("dogs", "age >= 0")
    before_schema = db.schema
    result = db.safe_bulk_insert(
        "dogs",
        [
            {"id": 3, "name": "Bailey", "age": 1},
            {"id": 4, "name": "Negative", "age": -1, "new_column": "x"},
        ],
        alter=True,
        batch_size=1,
    )
    assert result["success"] is False
    assert isinstance(result["checkpoint_id"], str)
    assert [f["id"] for f in result["failures"]] == [invariant_id]
    assert "invariant" in result["error_report"]
    assert invariant_id in result["error_report"]
    assert "age >= 0" in result["error_report"]
    assert db.schema == before_schema
    assert [r["id"] for r in rows(db)] == [1, 2]
    # The returned checkpoint has already been rolled back
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(result["checkpoint_id"])
    db.cleanup_checkpoint(result["checkpoint_id"])


def test_safe_bulk_insert_sql_error_rolls_back(db):
    result = db.safe_bulk_insert(
        "dogs",
        [
            {"id": 3, "name": "Bailey", "age": 1},
            {"id": 1, "name": "Duplicate", "age": 1},
        ],
        batch_size=1,
    )
    assert result["success"] is False
    assert result["failures"] == []
    assert "UNIQUE constraint failed" in result["error_report"]
    assert [r["id"] for r in rows(db)] == [1, 2]


def test_safe_bulk_insert_creating_table_rolls_back(db):
    db.add_import_invariant("birds", "SELECT count(*) > 5 FROM birds")
    result = db.safe_bulk_insert("birds", [{"id": 1, "name": "Tweety"}], pk="id")
    assert result["success"] is False
    assert "birds" not in db.table_names()


def test_safe_bulk_insert_does_not_require_safe_import_enabled(fresh_db):
    result = fresh_db.safe_bulk_insert("dogs", [{"id": 1}])
    assert result == {"success": True}
    assert not fresh_db.safe_import_enabled


def test_safe_bulk_insert_strict_invariant_failure(db):
    db.add_import_invariant("dogs", "age >= 0")
    with pytest.raises(ImportValidationError) as ex:
        db.safe_bulk_insert("dogs", [{"id": 3, "name": "Neg", "age": -1}], strict=True)
    message = str(ex.value).lower()
    assert "invariant" in message and "validation" in message
    assert ex.value.failures[0]["expression"] == "age >= 0"
    assert ex.value.error_report == str(ex.value)
    assert [r["id"] for r in rows(db)] == [1, 2]


def test_safe_bulk_insert_strict_reraises_original_error(db):
    with pytest.raises(sqlite3.IntegrityError):
        db.safe_bulk_insert(
            "dogs",
            [{"id": 3, "name": "New", "age": 1}, {"id": 1, "name": "Dup", "age": 1}],
            batch_size=1,
            strict=True,
        )
    assert [r["id"] for r in rows(db)] == [1, 2]
    assert db._import_checkpoints == {}


def test_safe_bulk_insert_rolls_back_on_keyboard_interrupt(db):
    def records():
        yield {"id": 3, "name": "Bailey", "age": 1}
        raise KeyboardInterrupt

    with pytest.raises(KeyboardInterrupt):
        db.safe_bulk_insert("dogs", records(), batch_size=1)
    assert [r["id"] for r in rows(db)] == [1, 2]


def test_safe_bulk_upsert(db):
    db.add_import_invariant("dogs", "age >= 0")
    result = db.safe_bulk_upsert(
        "dogs", [{"id": 1, "age": 6}, {"id": 3, "name": "New", "age": 1}], pk="id"
    )
    assert result == {"success": True}
    assert db["dogs"].get(1)["age"] == 6
    result = db.safe_bulk_upsert(
        "dogs", [{"id": 2, "age": 4}, {"id": 1, "age": -1}], "id", batch_size=1
    )
    assert result["success"] is False
    assert len(result["failures"]) == 1
    assert db["dogs"].get(1)["age"] == 6
    assert db["dogs"].get(2)["age"] == 3
    with pytest.raises(ImportValidationError):
        db.safe_bulk_upsert("dogs", [{"id": 1, "age": -1}], pk="id", strict=True)


def test_import_csv_detects_types_for_new_tables(fresh_db, tmpdir):
    csv_path = str(tmpdir / "products.csv")
    with open(csv_path, "w") as fp:
        fp.write("id,name,price\n1,Widget,2.5\n2,Gadget,10\n")
    assert fresh_db.import_csv("products", csv_path) == {"success": True}
    assert fresh_db["products"].columns_dict == {
        "id": int,
        "name": str,
        "price": float,
    }
    assert rows(fresh_db, "products") == [
        {"id": 1, "name": "Widget", "price": 2.5},
        {"id": 2, "name": "Gadget", "price": 10.0},
    ]


@pytest.mark.parametrize("table_exists", (True, False))
def test_import_csv_safe_mode(fresh_db, table_exists):
    if table_exists:
        fresh_db["products"].create({"id": int, "price": float}, pk="id")
    fresh_db.add_import_invariant("products", "price >= 0")
    result = fresh_db.import_csv(
        "products", io.StringIO("id,price\n1,10\n2,-5\n"), safe_mode=True
    )
    assert result["success"] is False
    assert result["failures"][0]["expression"] == "price >= 0"
    assert fresh_db["products"].exists() is table_exists
    if table_exists:
        assert fresh_db["products"].count == 0
    result = fresh_db.import_csv(
        "products", io.StringIO("id,price\n1,10\n2,5\n"), safe_mode=True
    )
    assert result == {"success": True}
    assert fresh_db["products"].count == 2


def test_import_csv_safe_mode_strict(fresh_db):
    fresh_db.add_import_invariant("products", "price >= 0")
    with pytest.raises(ImportValidationError, match="invariant"):
        fresh_db.import_csv(
            "products", io.StringIO("id,price\n1,-1\n"), safe_mode=True, strict=True
        )
    assert not fresh_db["products"].exists()


def test_import_csv_malformed_row_rolls_back(fresh_db):
    result = fresh_db.import_csv(
        "products", io.StringIO("id,price\n1,10\n2,5,extra\n"), safe_mode=True
    )
    assert result["success"] is False
    assert result["failures"] == []
    assert "extra values" in result["error_report"]
    assert not fresh_db["products"].exists()


def test_import_csv_without_safe_mode_ignores_invariants(fresh_db):
    fresh_db.add_import_invariant("products", "price >= 0")
    result = fresh_db.import_csv("products", io.StringIO("id,price\n1,-5\n"))
    assert result == {"success": True}
    assert fresh_db["products"].count == 1


@pytest.mark.parametrize("kind", ("list", "dict", "str", "bytes", "path", "file"))
def test_import_json(fresh_db, tmpdir, kind):
    records = [{"id": 1, "name": "Cleo"}, {"id": 2, "name": "Pancakes"}]
    json_path = str(tmpdir / "dogs.json")
    with open(json_path, "w") as fp:
        json.dump(records, fp)
    data = {
        "list": records,
        "dict": records[0],
        "str": json.dumps(records),
        "bytes": json.dumps(records).encode("utf-8"),
        "path": json_path,
        "file": io.StringIO(json.dumps(records)),
    }[kind]
    assert fresh_db.import_json("dogs", data, safe_mode=True) == {"success": True}
    expected = records[:1] if kind == "dict" else records
    assert rows(fresh_db) == expected


def test_import_json_safe_mode_rolls_back(fresh_db):
    fresh_db["dogs"].insert({"id": 1, "name": "Cleo"}, pk="id")
    fresh_db.add_import_invariant("dogs", "name is not null")
    result = fresh_db.import_json(
        "dogs", '[{"id": 2, "name": "Pancakes"}, {"id": 3}]', safe_mode=True
    )
    assert result["success"] is False
    assert result["failures"][0]["expression"] == "name is not null"
    assert fresh_db["dogs"].count == 1
    with pytest.raises(ImportValidationError):
        fresh_db.import_json("dogs", [{"id": 3}], safe_mode=True, strict=True)
    assert fresh_db["dogs"].count == 1


def test_import_json_invalid_data_rolls_back(fresh_db):
    result = fresh_db.import_json("dogs", [{"id": 1}, "not an object"], safe_mode=True)
    assert result["success"] is False
    assert result["failures"] == []
    assert "must be objects" in result["error_report"]
    assert not fresh_db["dogs"].exists()
    result = fresh_db.import_json("dogs", "[{bad json", safe_mode=True)
    assert result["success"] is False
