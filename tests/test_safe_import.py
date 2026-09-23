from click.testing import CliRunner
from sqlite_utils import Database, cli
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    ImportInvariantNotFoundError,
    ImportValidationError,
    SafeImportNotEnabledError,
)
from sqlite_utils.utils import sqlite3
import io
import json
import pytest


@pytest.fixture(params=["memory", "file", "wal"])
def db(request, tmpdir):
    if request.param == "memory":
        database = Database(memory=True)
    else:
        database = Database(str(tmpdir / "safe.db"))
        if request.param == "wal":
            database.enable_wal()
    database["dogs"].insert_all(
        [{"id": 1, "name": "Cleo", "age": 5}, {"id": 2, "name": "Pancakes", "age": 3}],
        pk="id",
    )
    return database


def full_state(db):
    return (
        db.execute(
            "select type, name, tbl_name, sql from sqlite_master order by name"
        ).fetchall(),
        {table: list(db[table].rows) for table in db.table_names()},
    )


def test_create_checkpoint_requires_enable(db):
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()
    db.enable_safe_import()
    assert db.create_import_checkpoint()
    db.disable_safe_import()
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()


def test_rollback_restores_data_and_schema(db):
    db.enable_safe_import()
    before = full_state(db)
    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Rex", "age": 1})
    db["dogs"].add_column("weight", float)
    db["dogs"].create_index(["name"])
    db["cats"].insert({"name": "Tom"})
    db.execute("create trigger dogs_ai after insert on dogs begin select 1; end")
    db["dogs"].delete(1)
    db.rollback_to_checkpoint(checkpoint_id)
    assert full_state(db) == before


def test_rollback_after_external_commit_visible_to_other_connections(tmpdir):
    path = str(tmpdir / "shared.db")
    db = Database(path)
    db["t"].insert({"id": 1}, pk="id")
    db.enable_safe_import()
    checkpoint_id = db.create_import_checkpoint()
    db["t"].insert({"id": 2})
    db["new"].insert({"x": 1})
    db.rollback_to_checkpoint(checkpoint_id)
    other = sqlite3.connect(path)
    assert other.execute("select id from t").fetchall() == [(1,)]
    assert (
        other.execute("select name from sqlite_master where name = 'new'").fetchall()
        == []
    )
    other.close()


def test_commit_checkpoint(db):
    db.enable_safe_import()
    checkpoint_id = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Rex", "age": 1})
    db.commit_checkpoint(checkpoint_id)
    assert db["dogs"].count == 3
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(checkpoint_id)
    db.cleanup_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotFoundError):
        db.cleanup_checkpoint(checkpoint_id)


def test_unknown_checkpoint(db):
    db.enable_safe_import()
    for method in (db.rollback_to_checkpoint, db.commit_checkpoint):
        with pytest.raises(CheckpointNotFoundError):
            method("does-not-exist")


def test_rollback_commits_pending_transaction_first(db):
    db.enable_safe_import()
    db.execute("insert into dogs (id, name, age) values (3, 'Rex', 1)")
    checkpoint_id = db.create_import_checkpoint()
    db.execute("insert into dogs (id, name, age) values (4, 'Fido', 2)")
    db.rollback_to_checkpoint(checkpoint_id)
    assert [r["id"] for r in db["dogs"].rows] == [1, 2, 3]


def test_nested_checkpoints(db):
    db.enable_safe_import()
    outer = db.create_import_checkpoint()
    db["dogs"].insert({"id": 3, "name": "Rex", "age": 1})
    inner = db.create_import_checkpoint()
    db["dogs"].insert({"id": 4, "name": "Fido", "age": 2})
    db.rollback_to_checkpoint(inner)
    assert db["dogs"].count == 3
    inner2 = db.create_import_checkpoint()
    db["dogs"].insert({"id": 5, "name": "Spot", "age": 2})
    db.commit_checkpoint(inner2)
    assert db["dogs"].count == 4
    db.rollback_to_checkpoint(outer)
    assert db["dogs"].count == 2


def test_rolling_back_outer_finalizes_inner(db):
    db.enable_safe_import()
    outer = db.create_import_checkpoint()
    inner = db.create_import_checkpoint()
    db.rollback_to_checkpoint(outer)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(inner)


def test_invariants_crud_and_persistence(tmpdir):
    path = str(tmpdir / "inv.db")
    db = Database(path)
    first = db.add_import_invariant("dogs", "age >= 0")
    second = db.add_import_invariant("dogs", "count(*) < 10")
    assert first and second and first != second
    assert Database(path).list_import_invariants("dogs") == [
        {"id": first, "expression": "age >= 0"},
        {"id": second, "expression": "count(*) < 10"},
    ]
    assert db.list_import_invariants("cats") == []
    db.remove_import_invariant("dogs", first)
    assert [i["id"] for i in db.list_import_invariants("dogs")] == [second]
    with pytest.raises(ImportInvariantNotFoundError):
        db.remove_import_invariant("dogs", first)


@pytest.mark.parametrize(
    "sql,valid",
    (
        ("age >= 0", True),
        ("age > 3", False),
        ("name is not null", True),
        ("count(*) = 2", True),
        ("COUNT(*) > 2", False),
        ("sum(age) = 8", True),
        ("max(age) < 5", False),
        ("avg(age) = 4", True),
        ("max(age, 4) >= 4", True),
        ("select count(*) = 2 from dogs", True),
        ("SELECT count(*) from dogs where age > 10", False),
        ("select 1 from dogs where 0", False),
        ("no_such_column > 1", False),
    ),
)
def test_validate_import_invariants(db, sql, valid):
    invariant_id = db.add_import_invariant("dogs", sql)
    result = db.validate_import_invariants("dogs")
    assert result["valid"] is valid
    if valid:
        assert result["failures"] == []
    else:
        assert len(result["failures"]) == 1
        failure = result["failures"][0]
        assert failure["id"] == invariant_id
        assert failure["expression"] == sql
        assert failure["error"]


def test_safe_bulk_insert_success(db):
    db.add_import_invariant("dogs", "age >= 0")
    result = db.safe_bulk_insert("dogs", [{"id": 3, "name": "Rex", "age": 1}])
    assert result == {"success": True}
    assert db["dogs"].count == 3


def test_safe_bulk_insert_invariant_failure_rolls_back(db):
    db.add_import_invariant("dogs", "age >= 0")
    before = full_state(db)
    result = db.safe_bulk_insert(
        "dogs",
        [{"id": 3, "name": "Rex", "age": 1, "weight": 3.5}, {"id": 4, "age": -1}],
        alter=True,
    )
    assert result["success"] is False
    assert result["checkpoint_id"]
    assert len(result["failures"]) == 1
    assert "invariant" in result["error_report"]
    assert full_state(db) == before
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(result["checkpoint_id"])
    db.cleanup_checkpoint(result["checkpoint_id"])


def test_safe_bulk_insert_new_table_rolled_back(db):
    db.add_import_invariant("cats", "count(*) < 2")
    result = db.safe_bulk_insert("cats", [{"name": "Tom"}, {"name": "Garfield"}])
    assert result["success"] is False
    assert "cats" not in db.table_names()


def test_safe_bulk_insert_sql_error(db):
    before = full_state(db)
    records = [{"id": 3, "name": "Rex", "age": 1}] * 150
    result = db.safe_bulk_insert("dogs", records, batch_size=10)
    assert result["success"] is False
    assert result["failures"] == []
    assert "UNIQUE" in result["error_report"]
    assert full_state(db) == before


def test_safe_bulk_insert_strict(db):
    db.add_import_invariant("dogs", "age >= 0")
    before = full_state(db)
    with pytest.raises(ImportValidationError) as ex:
        db.safe_bulk_insert("dogs", [{"id": 3, "age": -1}], strict=True)
    assert "validation" in str(ex.value)
    assert ex.value.checkpoint_id
    assert full_state(db) == before
    with pytest.raises(sqlite3.IntegrityError):
        db.safe_bulk_insert("dogs", [{"id": 1, "age": 1}], strict=True)
    assert full_state(db) == before


def test_safe_bulk_upsert(db):
    db.add_import_invariant("dogs", "age >= 0")
    result = db.safe_bulk_upsert("dogs", [{"id": 1, "age": 6}], pk="id")
    assert result == {"success": True}
    assert db["dogs"].get(1)["age"] == 6
    result = db.safe_bulk_upsert(
        "dogs", [{"id": 1, "age": 7}, {"id": 2, "age": -2}], pk="id"
    )
    assert result["success"] is False
    assert db["dogs"].get(1)["age"] == 6
    assert db["dogs"].get(2)["age"] == 3


def test_import_csv(db, tmpdir):
    path = str(tmpdir / "dogs.csv")
    with open(path, "w") as fp:
        fp.write("id,name,age\n3,Rex,1\n")
    assert db.import_csv("dogs", path, safe_mode=True) == {"success": True}
    assert db["dogs"].get(3)["age"] == 1
    assert db.import_csv("new", io.StringIO("a,b\n1,2.5\n")) == {"success": True}
    assert db["new"].columns_dict == {"a": int, "b": float}
    db.add_import_invariant("dogs", "age >= 0")
    before = full_state(db)
    result = db.import_csv(
        "dogs", io.StringIO("id,name,age\n4,Fido,-1\n"), safe_mode=True
    )
    assert result["success"] is False
    assert full_state(db) == before
    with pytest.raises(ImportValidationError):
        db.import_csv(
            "dogs", io.StringIO("id,name,age\n4,Fido,-1\n"), safe_mode=True, strict=True
        )
    assert full_state(db) == before


def test_import_json(db):
    db.add_import_invariant("dogs", "age >= 0")
    assert db.import_json(
        "dogs", [{"id": 3, "name": "Rex", "age": 1}], safe_mode=True
    ) == {"success": True}
    assert db.import_json("dogs", '{"id": 4, "age": 2}', safe_mode=True) == {
        "success": True
    }
    before = full_state(db)
    result = db.import_json(
        "dogs", io.StringIO('[{"id": 5, "age": -1}]'), safe_mode=True
    )
    assert result["success"] is False
    assert full_state(db) == before
    result = db.import_json("dogs", "not json", safe_mode=True)
    assert result["success"] is False
    assert result["failures"] == []


# CLI tests


@pytest.fixture
def cli_db(tmpdir):
    path = str(tmpdir / "cli.db")
    Database(path)["dogs"].insert_all(
        [{"id": 1, "name": "Cleo", "age": 5}, {"id": 2, "name": "Pancakes", "age": 3}],
        pk="id",
    )
    return path


def invoke(*args, input=None):
    return CliRunner().invoke(cli.cli, list(args), input=input)


def test_cli_enable_disable_safe_import(cli_db):
    assert invoke("enable-safe-import", cli_db).exit_code == 0
    assert Database(cli_db).safe_import_enabled
    assert invoke("disable-safe-import", cli_db).exit_code == 0
    assert not Database(cli_db).safe_import_enabled


def test_cli_invariant_commands(cli_db):
    result = invoke("add-import-invariant", cli_db, "dogs", "age >= 4")
    assert result.exit_code == 0
    invariant_id = result.output.strip()
    assert Database(cli_db).list_import_invariants("dogs") == [
        {"id": invariant_id, "expression": "age >= 4"}
    ]
    result = invoke("list-import-invariants", cli_db, "dogs")
    assert result.exit_code == 0
    assert result.output == "{}\tage >= 4\n".format(invariant_id)
    listed = json.loads(
        invoke("list-import-invariants", cli_db, "dogs", "--json").output
    )
    assert listed == [{"id": invariant_id, "expression": "age >= 4"}]

    result = invoke("validate-import-invariants", cli_db, "dogs")
    assert result.exit_code == 0
    assert result.output.startswith("FAIL")
    assert invariant_id in result.output
    as_json = json.loads(
        invoke("validate-import-invariants", cli_db, "dogs", "--json").output
    )
    assert as_json["valid"] is False

    assert (
        invoke("remove-import-invariant", cli_db, "dogs", invariant_id).exit_code == 0
    )
    result = invoke("validate-import-invariants", cli_db, "dogs")
    assert result.exit_code == 0
    assert result.output.startswith("PASS")
    assert (
        invoke("remove-import-invariant", cli_db, "dogs", invariant_id).exit_code == 1
    )


@pytest.mark.parametrize("command", ("insert", "upsert"))
@pytest.mark.parametrize(
    "input,extra",
    (
        ('[{"id": 3, "name": "Rex", "age": AGE}]', []),
        ('{"id": 3, "name": "Rex", "age": AGE}\n', ["--nl"]),
        ("id,name,age\n3,Rex,AGE\n", []),
        ("id\tname\tage\n3\tRex\tAGE\n", []),
        ("id,name,age\n3,Rex,AGE\n", ["--csv"]),
    ),
)
def test_cli_insert_upsert_safe_mode(cli_db, command, input, extra):
    Database(cli_db).add_import_invariant("dogs", "age >= 0")
    args = [command, cli_db, "dogs", "-", "--pk", "id", "--safe-mode"] + extra
    before = full_state(Database(cli_db))
    result = invoke(*args, input=input.replace("AGE", "-1"))
    assert result.exit_code != 0
    assert "invariant" in result.output
    assert full_state(Database(cli_db)) == before
    result = invoke(*args, input=input.replace("AGE", "1"))
    assert result.exit_code == 0, result.output
    assert Database(cli_db)["dogs"].get(3)["age"] == 1


def test_cli_insert_safe_mode_insert_error(cli_db):
    before = full_state(Database(cli_db))
    result = invoke(
        "insert",
        cli_db,
        "dogs",
        "-",
        "--safe-mode",
        "--alter",
        input='[{"id": 3, "weight": 1.5}, {"id": 1}]',
    )
    assert result.exit_code != 0
    assert full_state(Database(cli_db)) == before


def test_cli_bulk_safe_mode_update(cli_db):
    Database(cli_db).add_import_invariant("dogs", "age >= 0")
    sql = "update dogs set age = :age where id = :id"
    result = invoke(
        "bulk", cli_db, sql, "-", "--safe-mode", input='[{"id": 1, "age": -5}]'
    )
    assert result.exit_code != 0
    assert Database(cli_db)["dogs"].get(1)["age"] == 5
    result = invoke("bulk", cli_db, sql, "-", "--safe-mode", input="id,age\n1,7\n")
    assert result.exit_code == 0, result.output
    assert Database(cli_db)["dogs"].get(1)["age"] == 7
