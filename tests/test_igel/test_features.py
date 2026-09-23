"""Tests for the persisted raw feature schema (dataset.features)."""

import json

import joblib
import numpy as np
import pandas as pd
import pytest
import yaml
from fastapi.testclient import TestClient
from igel import Igel
from igel.constants import Constants
from igel.features import FeatureSchemaError
from igel.servers import fastapi_server

N_ROWS = 40


@pytest.fixture
def results_dir(tmp_path, monkeypatch):
    res = tmp_path / "model_results"
    monkeypatch.setattr(Igel, "results_path", res)
    monkeypatch.setattr(Igel, "default_model_path", res / "model.joblib")
    monkeypatch.setattr(
        Igel, "default_onnx_model_path", res / "model.onnx"
    )
    monkeypatch.setattr(Igel, "description_file", res / "description.json")
    monkeypatch.setattr(
        Igel, "feature_schema_file", res / "feature_schema.joblib"
    )
    monkeypatch.setattr(Igel, "evaluation_file", res / "evaluation.json")
    monkeypatch.setattr(Igel, "prediction_file", res / "predictions.csv")
    return res


def raw_frame(seed=0):
    rng = np.random.RandomState(seed)
    a = rng.randint(0, 10, N_ROWS)
    b = rng.randint(0, 10, N_ROWS)
    return pd.DataFrame(
        {
            "a": a,
            "const": np.ones(N_ROWS, dtype=int),
            "b": b,
            "a_dup": a,
            "a_dup2": a,
            "noise": rng.randint(0, 10, N_ROWS),
            "y": (a + b > 9).astype(int),
            "y2": a * 2 + b,
        }
    )


def write_csv(df, path):
    df.to_csv(path, index=False)
    return str(path)


def fit(tmp_path, features, model, target, df=None):
    config = {
        "dataset": {"type": "csv", "features": features},
        "model": model,
        "target": target,
    }
    yaml_path = tmp_path / "igel.yaml"
    yaml_path.write_text(yaml.safe_dump(config))
    data_path = write_csv(
        raw_frame() if df is None else df, tmp_path / "train.csv"
    )
    return Igel(cmd="fit", data_path=data_path, yaml_path=str(yaml_path))


def predict(tmp_path, results_dir, df):
    return Igel(
        cmd="predict",
        data_path=write_csv(df, tmp_path / "predict.csv"),
        model_path=results_dir / "model.joblib",
        description_file=results_dir / "description.json",
        prediction_file=results_dir / "predictions.csv",
    )


def read_description(results_dir):
    return json.loads((results_dir / "description.json").read_text())


CLASSIFIER = {"type": "classification", "algorithm": "RandomForest"}
REGRESSOR = {"type": "regression", "algorithm": "LinearRegression"}
CLUSTERING = {"type": "clustering", "algorithm": "KMeans"}


def test_fit_persists_schema_single_target(tmp_path, results_dir):
    fit(
        tmp_path,
        {"include": ["b", "a", "const", "a_dup", "a_dup2", "noise"], "exclude": "noise"},
        CLASSIFIER,
        ["y"],
    )
    desc = read_description(results_dir)
    schema_path = results_dir / Constants.feature_schema_file
    assert schema_path.exists()
    assert desc["feature_schema_path"] == str(schema_path)
    assert desc["input_features"] == ["b", "a"]
    assert desc["dropped_features"] == {
        "excluded": ["noise"],
        "constant": ["const"],
        "duplicate": ["a_dup", "a_dup2"],
    }
    assert desc["duplicate_feature_aliases"] == {"a": ["a_dup", "a_dup2"]}
    assert desc["train_data_shape"][1] == 2
    assert joblib.load(schema_path)["input_features"] == ["b", "a"]


def test_predict_and_evaluate_apply_schema(tmp_path, results_dir):
    fit(tmp_path, {"exclude": ["noise", "y2"]}, CLASSIFIER, ["y"])
    assert read_description(results_dir)["input_features"] == ["a", "b"]
    df = raw_frame(seed=1)

    expected = predict(tmp_path, results_dir, df[["a", "b"]]).predictions

    # extra columns are ignored and column order does not matter
    shuffled = df[["noise", "b", "y", "a"]].assign(extra=1)
    res = predict(tmp_path, results_dir, shuffled)
    pd.testing.assert_frame_equal(res.predictions, expected)

    # any alias satisfies the canonical feature
    only_alias = df[["b", "a_dup2"]]
    res = predict(tmp_path, results_dir, only_alias)
    pd.testing.assert_frame_equal(res.predictions, expected)

    # agreeing duplicate sources are accepted
    res = predict(tmp_path, results_dir, df[["a", "b", "a_dup", "a_dup2"]])
    pd.testing.assert_frame_equal(res.predictions, expected)

    Igel(
        cmd="evaluate",
        data_path=write_csv(df.assign(extra=3), tmp_path / "eval.csv"),
    )
    assert (results_dir / "evaluation.json").exists()


def test_predict_missing_feature_raises(tmp_path, results_dir):
    fit(tmp_path, {"include": ["a", "b", "a_dup"]}, CLASSIFIER, ["y"])
    with pytest.raises(FeatureSchemaError, match=r"'b'"):
        predict(tmp_path, results_dir, raw_frame()[["a", "noise"]])
    with pytest.raises(FeatureSchemaError, match=r"'a'.*'b'"):
        predict(tmp_path, results_dir, raw_frame()[["noise"]])


def test_evaluate_missing_feature_raises(tmp_path, results_dir):
    fit(tmp_path, {"include": ["a", "b"]}, CLASSIFIER, ["y"])
    with pytest.raises(FeatureSchemaError, match=r"'a'"):
        Igel(
            cmd="evaluate",
            data_path=write_csv(
                raw_frame()[["b", "y"]], tmp_path / "eval.csv"
            ),
        )


def test_conflicting_duplicate_sources_raise(tmp_path, results_dir):
    fit(tmp_path, {"include": ["a", "b", "a_dup", "a_dup2"]}, CLASSIFIER, ["y"])
    df = raw_frame()
    df.loc[N_ROWS - 1, "a_dup2"] = 99
    with pytest.raises(FeatureSchemaError, match=r"'a_dup', 'a_dup2'"):
        predict(tmp_path, results_dir, df[["b", "a_dup", "a_dup2"]])


def test_multi_target(tmp_path, results_dir):
    fit(tmp_path, {"include": "a"}, REGRESSOR, ["y", "y2"])
    desc = read_description(results_dir)
    assert desc["input_features"] == ["a"]
    assert desc["target"] == ["y", "y2"]
    res = predict(tmp_path, results_dir, raw_frame()[["b", "a"]])
    assert list(res.predictions.columns) == ["y", "y2"]
    assert len(res.predictions) == N_ROWS
    with pytest.raises(FeatureSchemaError, match=r"'a'"):
        predict(tmp_path, results_dir, raw_frame()[["a_dup", "b"]])

    fit(tmp_path, {"exclude": ["noise"]}, REGRESSOR, ["y", "y2"])
    desc = read_description(results_dir)
    assert desc["input_features"] == ["a", "b"]
    assert desc["duplicate_feature_aliases"] == {"a": ["a_dup", "a_dup2"]}
    res = predict(tmp_path, results_dir, raw_frame()[["a_dup", "b"]])
    assert list(res.predictions.columns) == ["y", "y2"]


def test_clustering(tmp_path, results_dir):
    fit(
        tmp_path,
        {"exclude": ["y", "y2", "noise"]},
        CLUSTERING,
        None,
    )
    desc = read_description(results_dir)
    assert desc["input_features"] == ["a", "b"]
    assert desc["dropped_features"]["constant"] == ["const"]
    assert desc["duplicate_feature_aliases"] == {"a": ["a_dup", "a_dup2"]}
    res = predict(tmp_path, results_dir, raw_frame()[["b", "a_dup"]])
    assert len(res.predictions) == N_ROWS
    with pytest.raises(FeatureSchemaError, match=r"'b'"):
        predict(tmp_path, results_dir, raw_frame()[["a"]])


def test_disabled_drop_options_keep_columns(tmp_path, results_dir):
    fit(
        tmp_path,
        {
            "include": ["a", "const", "a_dup"],
            "drop_constant": False,
            "drop_duplicate": False,
        },
        CLASSIFIER,
        ["y"],
    )
    desc = read_description(results_dir)
    assert desc["input_features"] == ["a", "const", "a_dup"]
    assert desc["dropped_features"] == {
        "excluded": [],
        "constant": [],
        "duplicate": [],
    }
    assert desc["duplicate_feature_aliases"] == {}


@pytest.mark.parametrize(
    "features, target, message",
    [
        ({"include": ["a", "missing"]}, ["y"], r"unknown column.*'missing'"),
        ({"exclude": "missing"}, ["y"], r"unknown column.*'missing'"),
        ({"include": ["a", "b", "a"]}, ["y"], r"duplicated entries.*'a'"),
        ({"exclude": ["noise", "noise"]}, ["y"], r"duplicated entries.*'noise'"),
        ({"include": ["a", "y"]}, ["y"], r"target column.*'y'"),
        ({"exclude": "y2"}, ["y", "y2"], r"target column.*'y2'"),
        ({"include": ["a", ""]}, ["y"], r"non-empty"),
        ({"include": ["a", 3]}, ["y"], r"non-empty"),
        ({"include": []}, ["y"], r"must not be empty"),
        ({"include": ["const"]}, ["y"], r"removes every feature"),
        (
            {"include": ["a", "b"], "exclude": ["a", "b"]},
            ["y"],
            r"removes every feature",
        ),
        ({"drop_constant": "yes"}, ["y"], r"drop_constant must be a boolean"),
        ({"includes": ["a"]}, ["y"], r"unknown dataset.features option"),
    ],
)
def test_invalid_features_config(tmp_path, results_dir, features, target, message):
    with pytest.raises(FeatureSchemaError, match=message):
        fit(tmp_path, features, REGRESSOR, target)


def test_fit_without_features_is_unchanged(tmp_path, results_dir):
    config = {
        "dataset": {"type": "csv"},
        "model": CLASSIFIER,
        "target": ["y"],
    }
    yaml_path = tmp_path / "igel.yaml"
    yaml_path.write_text(yaml.safe_dump(config))
    df = raw_frame().drop(columns=["y2"])
    Igel(
        cmd="fit",
        data_path=write_csv(df, tmp_path / "train.csv"),
        yaml_path=str(yaml_path),
    )
    desc = read_description(results_dir)
    assert "feature_schema_path" not in desc
    assert not (results_dir / Constants.feature_schema_file).exists()
    res = predict(tmp_path, results_dir, df.drop(columns=["y"]))
    assert len(res.predictions) == N_ROWS


def test_predict_endpoint(tmp_path, results_dir, monkeypatch):
    fit(tmp_path, {"include": ["a", "b", "a_dup"]}, CLASSIFIER, ["y"])
    monkeypatch.setenv(Constants.model_results_path, str(results_dir))
    monkeypatch.setattr(
        fastapi_server, "temp_post_req_data_path", tmp_path / "post.csv"
    )
    client = TestClient(fastapi_server.app)

    ok = client.post("/predict", json={"a_dup": [1, 5], "b": [2, 8], "x": [0, 0]})
    assert ok.status_code == 200
    assert len(ok.json()["prediction"]) == 2

    missing = client.post("/predict", json={"a": [1, 2]})
    assert missing.status_code == 400
    assert "'b'" in missing.json()["detail"]

    conflict = client.post(
        "/predict", json={"a": [1, 2], "a_dup": [1, 3], "b": [2, 2]}
    )
    assert conflict.status_code == 400
    assert "'a', 'a_dup'" in conflict.json()["detail"]
    assert not (tmp_path / "post.csv").exists()


def test_export_uses_recorded_input_width(tmp_path, results_dir):
    onnx = pytest.importorskip("onnx")
    fit(tmp_path, {"include": ["a", "b", "noise", "a_dup"]}, CLASSIFIER, ["y"])
    Igel(cmd="export", model_path=results_dir / "model.joblib")
    model = onnx.load(str(results_dir / "model.onnx"))
    dims = model.graph.input[0].type.tensor_type.shape.dim
    assert dims[1].dim_value == 3
