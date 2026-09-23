"""Tests for the dataset.features raw feature schema."""

import json
from pathlib import Path

import joblib
import numpy as np
import onnx
import pandas as pd
import pytest
from fastapi.testclient import TestClient
from igel import Igel
from igel.configs import configs
from igel.constants import Constants as IgelConstants
from igel.feature_schema import FeatureSchemaError
from igel.servers.fastapi_server import app

from .helper import remove_folder

RESULTS_DIR = Path(configs["results_path"])
DESCRIPTION_FILE = Path(configs["description_file"])
EVALUATION_FILE = Path(configs["evaluation_file"])
SCHEMA_FILE = Path(configs["feature_schema_file"])
MODEL_FILE = Path(configs["default_model_path"])
ONNX_FILE = Path(configs["default_onnx_model_path"])

FEATURES = {
    "include": ["f3", "f1", "const", "f1_copy", "f2", "noise", "f1_copy2"],
    "exclude": "noise",
    "drop_constant": True,
    "drop_duplicate": True,
}
INPUT_FEATURES = ["f3", "f1", "f2"]
DROPPED_FEATURES = {
    "excluded": ["noise"],
    "constant": ["const"],
    "duplicate": ["f1_copy", "f1_copy2"],
}
ALIASES = {"f1": ["f1_copy", "f1_copy2"]}

MODELS = {
    "single_target": (
        {
            "type": "classification",
            "algorithm": "RandomForest",
            "arguments": {"n_estimators": 10, "random_state": 0},
        },
        ["label"],
    ),
    "multi_target": (
        {"type": "regression", "algorithm": "LinearRegression"},
        ["y1", "y2"],
    ),
    "clustering": (
        {
            "type": "clustering",
            "algorithm": "KMeans",
            "arguments": {"n_clusters": 2, "n_init": 10, "random_state": 0},
        },
        None,
    ),
}
KINDS = list(MODELS)


@pytest.fixture(autouse=True)
def clean_results():
    remove_folder(RESULTS_DIR)
    yield
    remove_folder(RESULTS_DIR)


def make_frame(target, n=40, seed=0):
    rs = np.random.RandomState(seed)
    f1, f2, f3 = rs.randn(n), rs.randn(n), rs.randn(n)
    frame = pd.DataFrame(
        {
            "f1": f1,
            "const": 1.0,
            "f2": f2,
            "f1_copy": f1,
            "noise": rs.randn(n),
            "f3": f3,
            "f1_copy2": f1,
            "label": (f1 + f2 > 0).astype(int),
            "y1": 2 * f1 - f3,
            "y2": f2 + 0.5 * f3,
        }
    )
    unused_targets = [
        t for t in ("label", "y1", "y2") if t not in (target or [])
    ]
    return frame.drop(columns=unused_targets)


def fit(tmp_path, kind, features=FEATURES, frame=None):
    model, target = MODELS[kind]
    frame = make_frame(target) if frame is None else frame
    data_path = tmp_path / "train.csv"
    frame.to_csv(data_path, index=False)
    config_path = tmp_path / "igel.json"
    config_path.write_text(
        json.dumps(
            {"dataset": {"features": features}, "model": model, "target": target}
        )
    )
    Igel(cmd="fit", data_path=str(data_path), yaml_path=str(config_path))
    return json.loads(DESCRIPTION_FILE.read_text())


def write_csv(tmp_path, frame, name="new.csv"):
    path = tmp_path / name
    frame.to_csv(path, index=False)
    return str(path)


def predict(tmp_path, frame):
    return Igel(cmd="predict", data_path=write_csv(tmp_path, frame)).predictions


def expected_predictions(frame):
    model = joblib.load(MODEL_FILE)
    return np.asarray(model.predict(frame[INPUT_FEATURES].to_numpy()))


def raw_features(n=10, seed=1):
    """prediction inputs (without targets), columns shuffled and with an extra column"""
    frame = make_frame(None, n=n, seed=seed)
    frame["unrelated"] = "ignore me"
    return frame[
        ["unrelated", "f2", "f1_copy2", "noise", "f3", "f1", "f1_copy", "const"]
    ].copy()


@pytest.mark.parametrize("kind", KINDS)
def test_fit_persists_feature_schema(tmp_path, kind):
    description = fit(tmp_path, kind)

    assert SCHEMA_FILE.exists()
    assert description["feature_schema_path"] == str(SCHEMA_FILE)
    assert description["input_features"] == INPUT_FEATURES
    assert description["dropped_features"] == DROPPED_FEATURES
    assert description["duplicate_feature_aliases"] == ALIASES
    assert description["train_data_shape"][1] == len(INPUT_FEATURES)

    schema = joblib.load(SCHEMA_FILE)
    assert schema["input_features"] == INPUT_FEATURES
    assert schema["dropped_features"] == DROPPED_FEATURES
    assert schema["duplicate_feature_aliases"] == ALIASES


@pytest.mark.parametrize("kind", KINDS)
def test_predict_ignores_extras_and_accepts_aliases(tmp_path, kind):
    fit(tmp_path, kind)
    frame = raw_features()
    expected = expected_predictions(frame)

    alias_only = frame.drop(columns=["f1", "f1_copy"])
    predictions = predict(tmp_path, alias_only)

    assert predictions.shape[0] == len(frame)
    np.testing.assert_allclose(
        predictions.to_numpy(), expected.reshape(len(frame), -1)
    )
    # every agreeing duplicate source may be supplied at once
    np.testing.assert_allclose(
        predict(tmp_path, frame).to_numpy(), expected.reshape(len(frame), -1)
    )


@pytest.mark.parametrize("kind", KINDS)
def test_evaluate_applies_schema(tmp_path, kind):
    fit(tmp_path, kind)
    _, target = MODELS[kind]
    frame = make_frame(target, seed=2)
    frame["unrelated"] = 7
    frame = frame.drop(columns=["f1"])[::-1].reset_index(drop=True)
    frame = frame[list(reversed(frame.columns))]

    Igel(cmd="evaluate", data_path=write_csv(tmp_path, frame, "eval.csv"))
    assert EVALUATION_FILE.exists()

    with pytest.raises(FeatureSchemaError, match="f3"):
        Igel(
            cmd="evaluate",
            data_path=write_csv(tmp_path, frame.drop(columns=["f3"]), "bad.csv"),
        )


@pytest.mark.parametrize("kind", KINDS)
def test_missing_features_are_named(tmp_path, kind):
    fit(tmp_path, kind)
    frame = raw_features()

    with pytest.raises(FeatureSchemaError) as err:
        predict(tmp_path, frame.drop(columns=["f2", "f3"]))
    assert "'f2'" in str(err.value) and "'f3'" in str(err.value)

    with pytest.raises(FeatureSchemaError) as err:
        predict(tmp_path, frame.drop(columns=["f1", "f1_copy", "f1_copy2"]))
    message = str(err.value)
    assert "missing required feature(s): ['f1']" in message
    assert "f1_copy" in message and "f1_copy2" in message


@pytest.mark.parametrize("kind", KINDS)
def test_conflicting_duplicates_are_named(tmp_path, kind):
    fit(tmp_path, kind)
    frame = raw_features()
    frame.loc[3, "f1_copy"] = frame.loc[3, "f1"] + 1

    with pytest.raises(FeatureSchemaError) as err:
        predict(tmp_path, frame)
    message = str(err.value)
    assert "['f1', 'f1_copy']" in message
    assert "f1_copy2" not in message
    assert "row: 3" in message

    # the conflict is detected even when the canonical column is absent
    with pytest.raises(FeatureSchemaError, match=r"\['f1_copy', 'f1_copy2'\]"):
        predict(tmp_path, frame.drop(columns=["f1"]))


def test_include_order_decides_the_canonical_column(tmp_path):
    description = fit(
        tmp_path,
        "single_target",
        features={
            "include": ["f1_copy", "f2", "f1"],
            "drop_duplicate": True,
        },
    )
    assert description["input_features"] == ["f1_copy", "f2"]
    assert description["duplicate_feature_aliases"] == {"f1_copy": ["f1"]}
    assert description["dropped_features"] == {
        "excluded": [],
        "constant": [],
        "duplicate": ["f1"],
    }

    frame = raw_features()[["f2", "f1"]]
    predictions = predict(tmp_path, frame)
    model = joblib.load(MODEL_FILE)
    np.testing.assert_array_equal(
        predictions["label"].to_numpy(),
        model.predict(frame[["f1", "f2"]].to_numpy()),
    )


def test_single_names_and_default_flags(tmp_path):
    description = fit(tmp_path, "single_target", features={"include": "f2"})
    assert description["input_features"] == ["f2"]

    description = fit(tmp_path, "single_target", features={"exclude": "noise"})
    assert description["input_features"] == [
        "f1",
        "const",
        "f2",
        "f1_copy",
        "f3",
        "f1_copy2",
    ]
    assert description["dropped_features"] == {
        "excluded": ["noise"],
        "constant": [],
        "duplicate": [],
    }
    assert description["duplicate_feature_aliases"] == {}


def test_fit_without_features_keeps_previous_behaviour(tmp_path):
    model, target = MODELS["single_target"]
    frame = make_frame(target)
    config_path = tmp_path / "igel.json"
    config_path.write_text(
        json.dumps({"dataset": {"type": "csv"}, "model": model, "target": target})
    )
    Igel(
        cmd="fit",
        data_path=write_csv(tmp_path, frame, "train.csv"),
        yaml_path=str(config_path),
    )
    description = json.loads(DESCRIPTION_FILE.read_text())

    assert not SCHEMA_FILE.exists()
    for key in (
        "feature_schema_path",
        "input_features",
        "dropped_features",
        "duplicate_feature_aliases",
    ):
        assert key not in description
    predictions = predict(tmp_path, frame.drop(columns=target))
    assert predictions.shape == (len(frame), 1)


@pytest.mark.parametrize(
    "kind, features, message",
    [
        ("single_target", {"include": ["f1", "unknown"]}, "unknown column(s): ['unknown']"),
        ("single_target", {"exclude": "unknown"}, "unknown column(s): ['unknown']"),
        ("single_target", {"include": ["f1", "f2", "f1"]}, "duplicated entries: ['f1']"),
        ("single_target", {"exclude": ["noise", "noise"]}, "duplicated entries: ['noise']"),
        ("single_target", {"include": ["f1", "label"]}, "target column(s): ['label']"),
        ("single_target", {"exclude": "label"}, "target column(s): ['label']"),
        ("multi_target", {"include": ["f1", "y2"]}, "target column(s): ['y2']"),
        ("multi_target", {"exclude": ["y1"]}, "target column(s): ['y1']"),
        ("single_target", {"include": ["f1", ""]}, "non-empty column names"),
        ("single_target", {"exclude": [" "]}, "non-empty column names"),
        ("single_target", {"include": 3}, "a column name or a list"),
        ("single_target", {"include": []}, "removes every feature"),
        ("single_target", {"include": "const", "drop_constant": True}, "removes every feature"),
        (
            "clustering",
            {"exclude": ["f1", "const", "f2", "f1_copy", "noise", "f3", "f1_copy2"]},
            "removes every feature",
        ),
        ("single_target", {"drop_constants": True}, "unsupported option(s): ['drop_constants']"),
        ("single_target", {"drop_duplicate": "yes"}, "drop_duplicate must be true or false"),
        ("single_target", ["f1"], "dataset.features must be a mapping"),
    ],
)
def test_invalid_feature_configuration(tmp_path, kind, features, message):
    with pytest.raises(FeatureSchemaError) as err:
        fit(tmp_path, kind, features=features)
    assert message in str(err.value)
    assert not MODEL_FILE.exists()
    assert not SCHEMA_FILE.exists()


@pytest.mark.parametrize("kind", KINDS)
def test_predict_endpoint_enforces_schema(tmp_path, monkeypatch, kind):
    fit(tmp_path, kind)
    monkeypatch.setenv(IgelConstants.model_results_path, str(RESULTS_DIR))
    client = TestClient(app)
    frame = raw_features(n=4)
    expected = expected_predictions(frame)

    payload = frame.drop(columns=["f1"]).to_dict(orient="list")
    response = client.post("/predict", json=payload)
    assert response.status_code == 200
    np.testing.assert_allclose(
        np.asarray(response.json()["prediction"]), expected.reshape(len(frame), -1)
    )

    response = client.post(
        "/predict", json=frame.drop(columns=["f2"]).to_dict(orient="list")
    )
    assert response.status_code == 400
    assert "missing required feature(s): ['f2']" in response.json()["detail"]

    conflicting = frame.to_dict(orient="list")
    conflicting["f1_copy2"][0] += 1
    response = client.post("/predict", json=conflicting)
    assert response.status_code == 400
    assert "['f1', 'f1_copy2']" in response.json()["detail"]


@pytest.mark.parametrize("kind", KINDS)
def test_export_input_width_comes_from_description(tmp_path, kind):
    description = fit(tmp_path, kind)
    Igel(cmd="export", model_path=MODEL_FILE)

    graph_input = onnx.load(str(ONNX_FILE)).graph.input[0]
    width = graph_input.type.tensor_type.shape.dim[1].dim_value
    assert width == len(description["input_features"]) == len(INPUT_FEATURES)
