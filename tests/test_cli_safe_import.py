from click.testing import CliRunner
from sqlite_utils import cli, Database
import json
import pytest


@pytest.fixture
def db_path(tmpdir):
    path = str(tmpdir / "products.db")
    db = Database(path)
    db["products"].insert_all(
        [
            {"id": 1, "name": "Widget", "price": 2.5},
            {"id": 2, "name": "Gadget", "price": 10},
        ],
        pk="id",
    )
    db.add_import_invariant("products", "price >= 0")
    db.close()
    return path


def invoke(*args, input=None):
    return CliRunner().invoke(cli.cli, list(args), input=input)


def product_rows(db_path):
    db = Database(db_path)
    try:
        return [(row["id"], row["price"]) for row in db["products"].rows]
    finally:
        db.close()


def test_enable_disable_safe_import(tmpdir):
    path = str(tmpdir / "new.db")
    result = invoke("enable-safe-import", path)
    assert result.exit_code == 0, result.output
    assert Database(path).safe_import_enabled
    result = invoke("disable-safe-import", path)
    assert result.exit_code == 0, result.output
    assert not Database(path).safe_import_enabled


def test_add_list_remove_import_invariants(db_path):
    result = invoke("add-import-invariant", db_path, "products", "name is not null")
    assert result.exit_code == 0, result.output
    invariant_id = result.output.strip()
    existing_id = Database(db_path).list_import_invariants("products")[0]["id"]
    result = invoke("list-import-invariants", db_path, "products")
    assert result.exit_code == 0
    assert result.output == "{}\tprice >= 0\n{}\tname is not null\n".format(
        existing_id, invariant_id
    )
    result = invoke("remove-import-invariant", db_path, "products", invariant_id)
    assert result.exit_code == 0, result.output
    result = invoke("list-import-invariants", db_path, "products")
    assert result.output == "{}\tprice >= 0\n".format(existing_id)
    result = invoke("remove-import-invariant", db_path, "products", invariant_id)
    assert result.exit_code == 1
    assert "not found" in result.output


def test_add_import_invariant_empty_sql(db_path):
    result = invoke("add-import-invariant", db_path, "products", " ")
    assert result.exit_code == 1
    assert "cannot be empty" in result.output


def test_validate_import_invariants(db_path):
    result = invoke("validate-import-invariants", db_path, "products")
    assert result.exit_code == 0
    assert result.output == (
        "Valid: 1 of 1 import invariant passed for table products\n"
    )
    db = Database(db_path)
    failing_id = db.add_import_invariant("products", "price > 5")
    passing_id = db.add_import_invariant("products", "count(*) = 2")
    db.close()
    result = invoke("validate-import-invariants", db_path, "products")
    assert result.exit_code == 0
    assert result.output == (
        "Invalid: 1 of 3 import invariants failed for table products\n"
        "{}\tprice > 5\t1 row did not satisfy the expression\n".format(failing_id)
    )
    assert passing_id not in result.output
    result = invoke("validate-import-invariants", db_path, "products", "--json")
    assert result.exit_code == 0
    assert json.loads(result.output) == {
        "valid": False,
        "failures": [
            {
                "id": failing_id,
                "expression": "price > 5",
                "error": "1 row did not satisfy the expression",
            }
        ],
    }


@pytest.mark.parametrize("command", ("insert", "upsert"))
def test_safe_mode_insert_upsert_commits(db_path, command):
    result = invoke(
        command,
        db_path,
        "products",
        "-",
        "--pk",
        "id",
        "--safe-mode",
        input='[{"id": 3, "name": "Doohickey", "price": 1}]',
    )
    assert result.exit_code == 0, result.output
    assert product_rows(db_path) == [(1, 2.5), (2, 10), (3, 1)]


@pytest.mark.parametrize(
    "command,input",
    (
        (
            "insert",
            '[{"id": 3, "name": "A", "price": 1}, {"id": 4, "name": "B", "price": -1}]',
        ),
        ("upsert", '[{"id": 1, "price": 3}, {"id": 2, "price": -10}]'),
    ),
)
def test_safe_mode_insert_upsert_rolls_back_invariant_failure(db_path, command, input):
    before_schema = Database(db_path).schema
    result = invoke(
        command,
        db_path,
        "products",
        "-",
        "--pk",
        "id",
        "--batch-size",
        "1",
        "--alter",
        "--safe-mode",
        input=input,
    )
    assert result.exit_code == 1
    assert "Import invariant validation failed" in result.output
    assert "price >= 0" in result.output
    assert product_rows(db_path) == [(1, 2.5), (2, 10)]
    assert Database(db_path).schema == before_schema


def test_safe_mode_insert_rolls_back_sql_error(db_path):
    result = invoke(
        "insert",
        db_path,
        "products",
        "-",
        "--batch-size",
        "1",
        "--safe-mode",
        input='[{"id": 3, "name": "New", "price": 1}, {"id": 1, "name": "Duplicate", "price": 1}]',
    )
    assert result.exit_code == 1
    assert "UNIQUE constraint failed" in result.output
    assert product_rows(db_path) == [(1, 2.5), (2, 10)]


def test_safe_mode_insert_new_table_rolled_back(db_path):
    db = Database(db_path)
    db.add_import_invariant("other", "SELECT count(*) > 10 FROM other")
    db.close()
    result = invoke("insert", db_path, "other", "-", "--safe-mode", input='{"id": 1}')
    assert result.exit_code == 1
    assert "other" not in Database(db_path).table_names()


@pytest.mark.parametrize(
    "filename,content",
    (
        ("new.csv", "id,name,price\n3,New,-1\n"),
        ("new.tsv", "id\tname\tprice\n3\tNew\t-1\n"),
        ("new.json", '[{"id": 3, "name": "New", "price": -1}]'),
        (
            "new.jsonl",
            '{"id": 3, "name": "New", "price": 4}\n{"id": 4, "name": "New", "price": -1}\n',
        ),
        ("-", "id,name,price\n3,New,-1\n"),
        ("-", "id\tname\tprice\n3\tNew\t-1\n"),
        (
            "-",
            '{"id": 3, "name": "New", "price": 4}\n{"id": 4, "name": "New", "price": -1}\n',
        ),
        ("-", '{"id": 3, "name": "New", "price": -1}'),
    ),
)
def test_safe_mode_detects_format(db_path, tmpdir, filename, content):
    # A negative price only violates the invariant if the file was parsed correctly
    if filename == "-":
        file_arg, stdin = "-", content
    else:
        file_arg, stdin = str(tmpdir / filename), None
        with open(file_arg, "w") as fp:
            fp.write(content)
    result = invoke("insert", db_path, "products", file_arg, "--safe-mode", input=stdin)
    assert result.exit_code == 1, result.output
    assert "Import invariant validation failed" in result.output
    assert product_rows(db_path) == [(1, 2.5), (2, 10)]
    fixed = content.replace("-1", "1")
    if filename == "-":
        stdin = fixed
    else:
        with open(file_arg, "w") as fp:
            fp.write(fixed)
    result = invoke("insert", db_path, "products", file_arg, "--safe-mode", input=stdin)
    assert result.exit_code == 0, result.output
    assert len(product_rows(db_path)) > 2


def test_safe_mode_empty_input(db_path):
    result = invoke("insert", db_path, "products", "-", "--safe-mode", input="")
    assert result.exit_code == 1
    assert "Invalid JSON" in result.output


def test_safe_mode_explicit_format(db_path):
    result = invoke(
        "insert",
        db_path,
        "products",
        "-",
        "--csv",
        "--safe-mode",
        input="id,name,price\n3,New,-1\n",
    )
    assert result.exit_code == 1
    assert product_rows(db_path) == [(1, 2.5), (2, 10)]


def test_bulk_safe_mode_update(db_path):
    sql = "update products set price = :price where id = :id"
    result = invoke(
        "bulk",
        db_path,
        sql,
        "-",
        "--batch-size",
        "1",
        "--safe-mode",
        input='[{"id": 1, "price": 5}, {"id": 2, "price": -5}]',
    )
    assert result.exit_code == 1
    assert "Import invariant validation failed" in result.output
    assert product_rows(db_path) == [(1, 2.5), (2, 10)]
    result = invoke(
        "bulk",
        db_path,
        sql,
        "-",
        "--safe-mode",
        input='[{"id": 1, "price": 5}, {"id": 2, "price": 6}]',
    )
    assert result.exit_code == 0, result.output
    assert product_rows(db_path) == [(1, 5), (2, 6)]


def test_bulk_safe_mode_sql_error(db_path):
    result = invoke(
        "bulk",
        db_path,
        "insert into products (id, name, price) values (:id, :name, :price)",
        "-",
        "--batch-size",
        "1",
        "--safe-mode",
        input="id,name,price\n3,New,1\n1,Duplicate,1\n",
    )
    assert result.exit_code == 1
    assert "UNIQUE constraint failed" in result.output
    assert product_rows(db_path) == [(1, 2.5), (2, 10)]
