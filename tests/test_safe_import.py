from click.testing import CliRunner
from sqlite_utils import cli, Database
from sqlite_utils.db import (
    CheckpointNotActiveError,
    CheckpointNotFoundError,
    InvariantNotFoundError,
    InvariantValidationError,
    SafeImportNotEnabledError,
)
import io
import json
import pytest
import sqlite3


@pytest.fixture(params=["memory", "file"])
def db(request, tmpdir):
    if request.param == "memory":
        database = Database(memory=True)
    else:
        database = Database(str(tmpdir / "safe.db"))
    database["products"].insert_all([{"id": 1, "name": "Widget", "price": 5}], pk="id")
    yield database
    database.close()


def full_state(db):
    return (
        db.schema,
        {table: list(db[table].rows) for table in db.table_names()},
    )


def test_checkpoint_requires_safe_import_enabled(db):
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()
    db.enable_safe_import()
    assert db.safe_import_enabled
    assert db.create_import_checkpoint()
    db.disable_safe_import()
    assert not db.safe_import_enabled
    with pytest.raises(SafeImportNotEnabledError):
        db.create_import_checkpoint()


def test_rollback_restores_data_and_schema(db):
    db.enable_safe_import()
    before = full_state(db)
    checkpoint_id = db.create_import_checkpoint()
    db["products"].insert(
        {"id": 2, "name": "Gadget", "price": 3, "color": "red"}, alter=True
    )
    db["products"].create_index(["name"])
    db.execute(
        "create trigger products_ai after insert on products begin select 1; end"
    )
    db["other"].insert({"x": 1})
    db["products"].transform(rename={"price": "cost"})
    assert full_state(db) != before
    db.rollback_to_checkpoint(checkpoint_id)
    assert full_state(db) == before
    assert db["products"].indexes == []
    assert db.triggers == []


def test_commit_keeps_changes(db):
    db.enable_safe_import()
    checkpoint_id = db.create_import_checkpoint()
    db["products"].insert({"id": 2, "name": "Gadget", "price": 3})
    db.commit_checkpoint(checkpoint_id)
    assert db["products"].count == 2


def test_checkpoint_lifecycle_errors(db):
    db.enable_safe_import()
    checkpoint_id = db.create_import_checkpoint()
    db.commit_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(checkpoint_id)
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(checkpoint_id)
    db.cleanup_checkpoint(checkpoint_id)
    for method in (
        db.commit_checkpoint,
        db.rollback_to_checkpoint,
        db.cleanup_checkpoint,
    ):
        with pytest.raises(CheckpointNotFoundError):
            method(checkpoint_id)
    with pytest.raises(CheckpointNotFoundError):
        db.rollback_to_checkpoint("does-not-exist")


def test_nested_checkpoints(db):
    db.enable_safe_import()
    outer = db.create_import_checkpoint()
    db["products"].insert({"id": 2, "name": "Gadget", "price": 3})
    inner = db.create_import_checkpoint()
    db["products"].insert({"id": 3, "name": "Doohickey", "price": 1})
    db.rollback_to_checkpoint(inner)
    assert [r["id"] for r in db["products"].rows] == [1, 2]
    db.rollback_to_checkpoint(outer)
    assert [r["id"] for r in db["products"].rows] == [1]


def test_rollback_outer_finalizes_inner(db):
    db.enable_safe_import()
    outer = db.create_import_checkpoint()
    inner = db.create_import_checkpoint()
    db.rollback_to_checkpoint(outer)
    with pytest.raises(CheckpointNotActiveError):
        db.commit_checkpoint(inner)


def test_invariant_crud(db):
    first = db.add_import_invariant("products", "price > 0")
    second = db.add_import_invariant("products", "count(*) < 10")
    assert first and second and first != second
    assert db.list_import_invariants("products") == [
        {"id": first, "expression": "price > 0"},
        {"id": second, "expression": "count(*) < 10"},
    ]
    assert db.list_import_invariants("other") == []
    db.remove_import_invariant("products", first)
    assert db.list_import_invariants("products") == [
        {"id": second, "expression": "count(*) < 10"}
    ]
    with pytest.raises(InvariantNotFoundError):
        db.remove_import_invariant("products", first)
    # Persisted in the database
    if db.memory:
        return
    path = db.execute("pragma database_list").fetchone()[2]
    assert Database(path).list_import_invariants("products") == [
        {"id": second, "expression": "count(*) < 10"}
    ]


@pytest.mark.parametrize(
    "sql,valid",
    (
        ("price > 0", True),
        ("price > 4", True),
        ("price > 5", False),
        ("count(*) = 1", True),
        ("COUNT(*) > 1", False),
        ("sum(price) = 5", True),
        ("max(price) < 3", False),
        ("select count(*) from products", True),
        ("SELECT count(*) from products where price > 100", False),
        ("select 1 from products where 0", False),
        ("no_such_column > 1", False),
    ),
)
def test_validate_import_invariants(db, sql, valid):
    invariant_id = db.add_import_invariant("products", sql)
    result = db.validate_import_invariants("products")
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
    db.add_import_invariant("products", "price > 0")
    result = db.safe_bulk_insert("products", [{"id": 2, "name": "Gadget", "price": 3}])
    assert result == {"success": True}
    assert db["products"].count == 2


def test_safe_bulk_insert_invariant_failure_rolls_back(db):
    invariant_id = db.add_import_invariant("products", "price > 0")
    before = full_state(db)
    result = db.safe_bulk_insert(
        "products",
        [
            {"id": 2, "name": "Gadget", "price": 3, "color": "red"},
            {"id": 3, "name": "Broken", "price": -1},
        ],
        alter=True,
    )
    assert result["success"] is False
    assert result["checkpoint_id"]
    assert [f["id"] for f in result["failures"]] == [invariant_id]
    assert "invariant" in result["error_report"].lower()
    assert full_state(db) == before
    with pytest.raises(CheckpointNotActiveError):
        db.rollback_to_checkpoint(result["checkpoint_id"])


def test_safe_bulk_insert_sql_error_rolls_back(db):
    before = full_state(db)
    result = db.safe_bulk_insert(
        "products",
        [
            {"id": 2, "name": "Gadget", "price": 3},
            {"id": 1, "name": "Dupe", "price": 1},
        ],
    )
    assert result["success"] is False
    assert result["failures"] == []
    assert "UNIQUE" in result["error_report"]
    assert full_state(db) == before


def test_safe_bulk_insert_new_table_rolled_back(db):
    db.add_import_invariant("fresh", "count(*) < 2")
    before = full_state(db)
    result = db.safe_bulk_insert("fresh", [{"a": 1}, {"a": 2}])
    assert result["success"] is False
    assert "fresh" not in db.table_names()
    assert full_state(db) == before


def test_safe_bulk_insert_strict(db):
    db.add_import_invariant("products", "price > 0")
    before = full_state(db)
    with pytest.raises(InvariantValidationError) as excinfo:
        db.safe_bulk_insert(
            "products", [{"id": 2, "name": "Bad", "price": 0}], strict=True
        )
    assert "validation" in str(excinfo.value).lower()
    assert excinfo.value.failures[0]["expression"] == "price > 0"
    assert full_state(db) == before
    with pytest.raises(sqlite3.IntegrityError):
        db.safe_bulk_insert(
            "products", [{"id": 1, "name": "Dupe", "price": 1}], strict=True
        )
    assert full_state(db) == before


def test_safe_bulk_upsert(db):
    db.add_import_invariant("products", "price > 0")
    assert db.safe_bulk_upsert(
        "products",
        [{"id": 1, "price": 7}, {"id": 2, "name": "New", "price": 2}],
        pk="id",
    ) == {"success": True}
    assert db["products"].get(1)["price"] == 7
    before = full_state(db)
    result = db.safe_bulk_upsert("products", [{"id": 1, "price": -7}], pk="id")
    assert result["success"] is False
    assert full_state(db) == before


def test_safe_operation_inside_user_checkpoint(db):
    db.enable_safe_import()
    db.add_import_invariant("products", "price > 0")
    outer = db.create_import_checkpoint()
    db["products"].insert({"id": 2, "name": "Gadget", "price": 3})
    assert db.safe_bulk_insert("products", [{"id": 3, "price": -1}])["success"] is False
    assert db["products"].count == 2
    db.rollback_to_checkpoint(outer)
    assert db["products"].count == 1


def test_import_csv(db, tmpdir):
    path = str(tmpdir / "items.csv")
    with open(path, "w") as fp:
        fp.write("id,qty\n1,5\n2,7\n")
    assert db.import_csv("items", path) == {"success": True}
    assert list(db["items"].rows) == [{"id": 1, "qty": 5}, {"id": 2, "qty": 7}]
    db.add_import_invariant("items", "qty > 0")
    before = full_state(db)
    result = db.import_csv("items", io.StringIO("id,qty\n3,-1\n"), safe_mode=True)
    assert result["success"] is False
    assert full_state(db) == before
    with pytest.raises(InvariantValidationError):
        db.import_csv(
            "items", io.StringIO("id,qty\n3,-1\n"), safe_mode=True, strict=True
        )
    assert db.import_csv("items", io.StringIO("id,qty\n3,1\n"), safe_mode=True) == {
        "success": True
    }
    assert db["items"].count == 3


@pytest.mark.parametrize(
    "make_data",
    (
        lambda: [{"id": 1, "tags": ["a"]}],
        lambda: {"id": 1, "tags": ["a"]},
        lambda: '[{"id": 1, "tags": ["a"]}]',
        lambda: '{"id": 1, "tags": ["a"]}\n',
        lambda: io.StringIO('[{"id": 1, "tags": ["a"]}]'),
        lambda: io.BytesIO(b'[{"id": 1, "tags": ["a"]}]'),
    ),
)
def test_import_json_inputs(db, make_data):
    assert db.import_json("things", make_data(), safe_mode=True) == {"success": True}
    assert list(db["things"].rows) == [{"id": 1, "tags": '["a"]'}]


def test_import_json_safe_mode_failure(db):
    db.add_import_invariant("products", "name is not null")
    before = full_state(db)
    result = db.import_json(
        "products", json.dumps([{"id": 9, "price": 1}]), safe_mode=True
    )
    assert result["success"] is False
    assert result["failures"][0]["expression"] == "name is not null"
    assert full_state(db) == before


# CLI tests


@pytest.fixture
def db_path(tmpdir):
    path = str(tmpdir / "cli.db")
    db = Database(path)
    db["products"].insert_all([{"id": 1, "name": "Widget", "price": 5}], pk="id")
    db.close()
    return path


def invoke(*args, input=None):
    return CliRunner().invoke(cli.cli, list(args), input=input)


def test_cli_enable_disable_safe_import(db_path):
    assert invoke("enable-safe-import", db_path).exit_code == 0
    assert Database(db_path).safe_import_enabled
    assert invoke("disable-safe-import", db_path).exit_code == 0
    assert not Database(db_path).safe_import_enabled


def test_cli_invariant_commands(db_path):
    result = invoke("add-import-invariant", db_path, "products", "price > 0")
    assert result.exit_code == 0
    invariant_id = result.output.strip()
    listed = invoke("list-import-invariants", db_path, "products")
    assert listed.exit_code == 0
    assert invariant_id in listed.output
    assert "price > 0" in listed.output

    validated = invoke("validate-import-invariants", db_path, "products")
    assert validated.exit_code == 0
    assert "PASS" in validated.output

    Database(db_path).execute("update products set price = -1").connection.commit()
    validated = invoke("validate-import-invariants", db_path, "products")
    assert validated.exit_code == 0
    assert "FAIL" in validated.output
    assert invariant_id in validated.output

    assert (
        invoke("remove-import-invariant", db_path, "products", invariant_id).exit_code
        == 0
    )
    assert invoke("list-import-invariants", db_path, "products").output == ""
    result = invoke("remove-import-invariant", db_path, "products", invariant_id)
    assert result.exit_code != 0


@pytest.mark.parametrize("command", ("insert", "upsert"))
def test_cli_insert_upsert_safe_mode(db_path, command):
    invoke("add-import-invariant", db_path, "products", "price > 0")
    before = full_state(Database(db_path))
    bad = json.dumps(
        [{"id": 2, "name": "A", "price": 1, "extra": 1}, {"id": 3, "price": -1}]
    )
    result = invoke(
        command,
        db_path,
        "products",
        "-",
        "--pk",
        "id",
        "--alter",
        "--safe-mode",
        input=bad,
    )
    assert result.exit_code != 0
    assert "invariant" in result.output
    assert full_state(Database(db_path)) == before

    good = json.dumps([{"id": 2, "name": "A", "price": 1}])
    result = invoke(
        command, db_path, "products", "-", "--pk", "id", "--safe-mode", input=good
    )
    assert result.exit_code == 0, result.output
    assert Database(db_path)["products"].count == 2


def test_cli_insert_safe_mode_sql_error(db_path):
    before = full_state(Database(db_path))
    result = invoke(
        "insert",
        db_path,
        "products",
        "-",
        "--safe-mode",
        input=json.dumps([{"id": 2, "price": 1}, {"id": 1, "price": 1}]),
    )
    assert result.exit_code != 0
    assert full_state(Database(db_path)) == before


@pytest.mark.parametrize(
    "content",
    (
        "id,name,price\n2,A,-1\n",
        "id\tname\tprice\n2\tA\t-1\n",
        '{"id": 2, "name": "A", "price": -1}\n{"id": 3, "name": "B", "price": 1}\n',
    ),
)
def test_cli_insert_safe_mode_infers_format(db_path, content):
    invoke("add-import-invariant", db_path, "products", "price > 0")
    result = invoke("insert", db_path, "products", "-", "--safe-mode", input=content)
    assert result.exit_code != 0
    assert "invariant" in result.output
    good = content.replace("-1", "1").replace('"id": 3', '"id": 4')
    result = invoke("insert", db_path, "products", "-", "--safe-mode", input=good)
    assert result.exit_code == 0, result.output
    assert Database(db_path)["products"].get(2)["price"] == 1


def test_cli_insert_safe_mode_csv_detects_types(db_path, tmpdir):
    csv_path = str(tmpdir / "new.csv")
    with open(csv_path, "w") as fp:
        fp.write("id,qty\n1,5\n")
    invoke("add-import-invariant", db_path, "new", "qty > 1")
    result = invoke("insert", db_path, "new", csv_path, "--safe-mode")
    assert result.exit_code == 0, result.output
    assert list(Database(db_path)["new"].rows) == [{"id": 1, "qty": 5}]


def test_cli_bulk_update_safe_mode(db_path):
    invoke("add-import-invariant", db_path, "products", "price > 0")
    sql = "update products set price = :price where id = :id"
    result = invoke(
        "bulk", db_path, sql, "-", "--safe-mode", input='[{"id": 1, "price": -3}]'
    )
    assert result.exit_code != 0
    assert "invariant" in result.output
    assert Database(db_path)["products"].get(1)["price"] == 5
    result = invoke(
        "bulk", db_path, sql, "-", "--safe-mode", input='[{"id": 1, "price": 8}]'
    )
    assert result.exit_code == 0, result.output
    assert Database(db_path)["products"].get(1)["price"] == 8
