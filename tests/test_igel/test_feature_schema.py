"""Tests for the persisted raw feature schema (dataset.features)."""

import json
import os
import shutil

import joblib
import numpy as np
import pandas as pd
import pytest
from fastapi.testclient import TestClient
from igel import Igel
from igel.constants import Constants
from igel.feature_schema import FeatureSchemaError
from igel.servers import fastapi_server

N_ROWS = 40


@pytest.fixture(autouse=True)
def clean_results():
    shutil.rmtree(Igel.results_path, ignore_errors=True)
    yield
    shutil.rmtree(Igel.results_path, ignore_errors=True)


@pytest.fixture
def train_df():
    rng = np.random.RandomState(0)
    a = rng.randint(0, 100, N_ROWS)
    b = rng.randint(0, 100, N_ROWS)
    return pd.DataFrame(
        {
            "a": a,
            "b": b,
            "const": np.ones(N_ROWS, dtype=int),
            "b_copy": b,
            "noise": rng.randint(0, 100, N_ROWS),
            "b_copy2": b,
            "y": (a > 50).astype(int),
            "y2": (b > 50).astype(int),
        }
    )


def _write(tmp_path, name, df):
    path = tmp_path / name
    df.to_csv(path, index=False)
    return str(path)


def _fit(tmp_path, df, features, model=None, target=("y",)):
    config = {
        "dataset": {"type": "csv", "features": features},
        "model": model or {"type": "classification", "algorithm": "RandomForest"},
        "target": list(target),
    }
    if features is None:
        del config["dataset"]["features"]
    yaml_path = tmp_path / "igel.json"
    yaml_path.write_text(json.dumps(config))
    data_path = _write(tmp_path, "train.csv", df)
    return Igel(cmd="fit", data_path=data_path, yaml_path=str(yaml_path))


def _description():
    with open(Igel.description_file) as f:
        return json.load(f)


FEATURES = {
    "include": ["noise", "b", "const", "b_copy", "a", "b_copy2"],
    "exclude": "noise",
    "drop_constant": True,
    "drop_duplicate": True,
}


def test_fit_persists_schema_and_description(tmp_path, train_df):
    igel = _fit(tmp_path, train_df, FEATURES)
    desc = _description()

    assert os.path.exists(Igel.feature_schema_file)
    assert desc["feature_schema_path"] == str(Igel.feature_schema_file)
    assert desc["input_features"] == ["b", "a"]
    assert desc["dropped_features"] == {
        "excluded": ["noise"],
        "constant": ["const"],
        "duplicate": ["b_copy", "b_copy2"],
    }
    assert desc["duplicate_feature_aliases"] == {"b": ["b_copy", "b_copy2"]}
    assert desc["train_data_shape"][1] == 2
    assert joblib.load(Igel.feature_schema_file) == igel.feature_schema


def test_fit_without_features_has_no_schema(tmp_path, train_df):
    _fit(tmp_path, train_df, None)
    assert not os.path.exists(Igel.feature_schema_file)
    assert "feature_schema_path" not in _description()


def test_predict_applies_schema_with_aliases_and_extras(tmp_path, train_df):
    _fit(tmp_path, train_df, FEATURES)
    new = train_df[["a", "b"]].copy()
    new["unrelated"] = 7
    expected = Igel(cmd="predict", data_path=_write(tmp_path, "p1.csv", new))

    # reordered columns, alias instead of canonical, extra columns ignored
    alias_only = pd.DataFrame(
        {"extra": 1, "a": train_df["a"], "b_copy2": train_df["b"]}
    )
    res = Igel(cmd="predict", data_path=_write(tmp_path, "p2.csv", alias_only))
    pd.testing.assert_frame_equal(res.predictions, expected.predictions)

    agreeing = train_df[["a", "b", "b_copy"]]
    res = Igel(cmd="predict", data_path=_write(tmp_path, "p3.csv", agreeing))
    pd.testing.assert_frame_equal(res.predictions, expected.predictions)


def test_predict_missing_feature_raises(tmp_path, train_df):
    _fit(tmp_path, train_df, FEATURES)
    with pytest.raises(FeatureSchemaError, match="'a'"):
        Igel(
            cmd="predict",
            data_path=_write(tmp_path, "p.csv", train_df[["b", "noise"]]),
        )


def test_predict_conflicting_aliases_raise(tmp_path, train_df):
    _fit(tmp_path, train_df, FEATURES)
    bad = train_df[["a", "b", "b_copy"]].copy()
    bad.loc[3, "b_copy"] = bad.loc[3, "b"] + 1
    with pytest.raises(FeatureSchemaError, match="b_copy"):
        Igel(cmd="predict", data_path=_write(tmp_path, "p.csv", bad))


def test_evaluate_applies_schema(tmp_path, train_df):
    _fit(tmp_path, train_df, FEATURES)
    eval_df = train_df[["y", "b_copy", "a", "noise"]]
    Igel(cmd="evaluate", data_path=_write(tmp_path, "e.csv", eval_df))
    assert os.path.exists(Igel.evaluation_file)

    with pytest.raises(FeatureSchemaError, match="'b'"):
        Igel(
            cmd="evaluate",
            data_path=_write(tmp_path, "e2.csv", train_df[["y", "a"]]),
        )


def test_multi_target(tmp_path, train_df):
    _fit(tmp_path, train_df, {"exclude": ["noise"]}, target=("y", "y2"))
    desc = _description()
    assert desc["input_features"] == ["a", "b", "const", "b_copy", "b_copy2"]
    res = Igel(
        cmd="predict",
        data_path=_write(tmp_path, "p.csv", train_df.drop(columns=["y", "y2"])),
    )
    assert list(res.predictions.columns) == ["y", "y2"]


def test_clustering(tmp_path, train_df):
    features = {"include": ["a", "b", "b_copy"], "drop_duplicate": True}
    model = {
        "type": "clustering",
        "algorithm": "KMeans",
        "arguments": {"n_clusters": 2, "n_init": 10},
    }
    _fit(tmp_path, train_df, features, model=model)
    desc = _description()
    assert desc["input_features"] == ["a", "b"]
    assert desc["duplicate_feature_aliases"] == {"b": ["b_copy"]}
    assert len(desc["clustering_results"]["cluster_centers"][0]) == 2

    res = Igel(
        cmd="predict", data_path=_write(tmp_path, "p.csv", train_df[["b", "a"]])
    )
    assert len(res.predictions) == N_ROWS
    Igel(cmd="evaluate", data_path=_write(tmp_path, "e.csv", train_df))
    with pytest.raises(FeatureSchemaError, match="'b'"):
        Igel(cmd="predict", data_path=_write(tmp_path, "p2.csv", train_df[["a"]]))


@pytest.mark.parametrize(
    "features, message",
    [
        ({"include": ["a", "missing"]}, "unknown column"),
        ({"exclude": "missing"}, "unknown column"),
        ({"include": ["a", "a"]}, "duplicated"),
        ({"exclude": ["a", "a"]}, "duplicated"),
        ({"include": ["a", ""]}, "non-empty"),
        ({"include": ["a", "y"]}, "target"),
        ({"exclude": "y"}, "target"),
        ({"include": []}, "every feature"),
        ({"include": "const", "drop_constant": True}, "every feature"),
        (
            {"exclude": ["a", "b", "const", "b_copy", "noise", "b_copy2", "y2"]},
            "every feature",
        ),
        ({"drop_constant": "yes"}, "boolean"),
        ({"unknown": 1}, "unknown dataset.features option"),
        (["a"], "mapping"),
    ],
)
def test_invalid_config(tmp_path, train_df, features, message):
    with pytest.raises(FeatureSchemaError, match=message):
        _fit(tmp_path, train_df, features)


def test_single_string_include(tmp_path, train_df):
    _fit(tmp_path, train_df, {"include": "a"})
    assert _description()["input_features"] == ["a"]


def test_export_uses_description_width(tmp_path, train_df):
    _fit(tmp_path, train_df, FEATURES)
    Igel(cmd="export", model_path=Igel.default_model_path)
    import onnx

    model = onnx.load(str(Igel.default_onnx_model_path))
    dims = model.graph.input[0].type.tensor_type.shape.dim
    assert dims[1].dim_value == 2


def test_server_predict(tmp_path, train_df, monkeypatch):
    _fit(tmp_path, train_df, FEATURES)
    monkeypatch.setenv(Constants.model_results_path, str(Igel.results_path))
    client = TestClient(fastapi_server.app)

    ok = client.post("/predict", json={"a": [10, 90], "b_copy": [1, 2], "x": [0, 0]})
    assert ok.status_code == 200
    assert len(ok.json()["prediction"]) == 2

    missing = client.post("/predict", json={"b": [1, 2]})
    assert missing.status_code == 400
    assert "'a'" in missing.json()["detail"]

    conflict = client.post(
        "/predict", json={"a": [1, 2], "b": [1, 2], "b_copy2": [1, 3]}
    )
    assert conflict.status_code == 400
    assert "b_copy2" in conflict.json()["detail"]
