"""Tests for persisted raw feature schemas."""

import json
from pathlib import Path

import joblib
import pandas as pd
import pytest
import yaml
from fastapi.testclient import TestClient
from igel import Igel
from igel.constants import Constants
from igel.feature_schema import FeatureSchemaError, build_feature_schema
from igel.servers.fastapi_server import app


@pytest.fixture
def results_dir(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    results.mkdir()
    monkeypatch.setattr(Igel, "results_path", results)
    monkeypatch.setattr(Igel, "default_model_path", results / "model.joblib")
    monkeypatch.setattr(Igel, "default_onnx_model_path", results / "model.onnx")
    monkeypatch.setattr(Igel, "description_file", results / "description.json")
    monkeypatch.setattr(Igel, "evaluation_file", results / "evaluation.json")
    monkeypatch.setattr(Igel, "prediction_file", results / "predictions.csv")
    return results


def _fit(results_dir, frame, config):
    data_path = results_dir.parent / "train.csv"
    yaml_path = results_dir.parent / "igel.yaml"
    frame.to_csv(data_path, index=False)
    yaml_path.write_text(yaml.safe_dump(config, sort_keys=False))
    Igel(cmd="fit", data_path=str(data_path), yaml_path=str(yaml_path))
    description = json.loads(
        (results_dir / "description.json").read_text(encoding="utf-8")
    )
    return description


def _predict(results_dir, frame):
    data_path = results_dir.parent / "predict.csv"
    frame.to_csv(data_path, index=False)
    Igel(
        cmd="predict",
        data_path=str(data_path),
        model_path=str(results_dir / "model.joblib"),
        description_file=str(results_dir / "description.json"),
        prediction_file=str(results_dir / "predictions.csv"),
    )
    return pd.read_csv(results_dir / "predictions.csv")


def _regression(features, targets=None):
    return {
        "dataset": {"features": features},
        "model": {"type": "regression", "algorithm": "LinearRegression"},
        "target": ["y"] if targets is None else list(targets),
    }


def test_build_schema_orders_filters_and_aliases():
    frame = pd.DataFrame(
        {
            "id": [1, 2, 3],
            "a": [1, None, 3],
            "a_dup": [1.0, None, 3.0],
            "const": [5, 5, 5],
            "empty": [None, None, None],
            "b": [4, 5, 6],
            "y": [0, 1, 0],
        }
    )
    schema = build_feature_schema(
        frame,
        {
            "include": ["b", "a", "a_dup", "const", "empty", "id"],
            "exclude": "id",
            "drop_constant": True,
            "drop_duplicate": True,
        },
        targets=["y"],
    )

    assert schema["input_features"] == ["b", "a"]
    assert schema["dropped_features"] == {
        "excluded": ["id"],
        "constant": ["const", "empty"],
        "duplicate": ["a_dup"],
    }
    assert schema["duplicate_feature_aliases"] == {"a": ["a_dup"]}


def test_fit_persists_schema_for_single_target(results_dir):
    frame = pd.DataFrame(
        {
            "id": [1, 2, 3, 4],
            "a": [1, 2, 3, 4],
            "a_dup": [1, 2, 3, 4],
            "const": [7, 7, 7, 7],
            "b": [0, 1, 0, 1],
            "y": [2, 4, 6, 8],
        }
    )
    description = _fit(
        results_dir,
        frame,
        _regression(
            {
                "include": ["b", "a", "a_dup", "const", "id"],
                "exclude": ["id"],
                "drop_constant": True,
                "drop_duplicate": True,
            }
        ),
    )

    schema_path = Path(description["feature_schema_path"])
    assert schema_path.name == "feature_schema.joblib"
    assert schema_path.is_file()
    assert description["input_features"] == ["b", "a"]
    assert description["dropped_features"] == {
        "excluded": ["id"],
        "constant": ["const"],
        "duplicate": ["a_dup"],
    }
    assert description["duplicate_feature_aliases"] == {"a": ["a_dup"]}
    assert description["train_data_shape"][1] == 2
    assert joblib.load(schema_path)["input_features"] == ["b", "a"]

    canonical = _predict(
        results_dir, pd.DataFrame({"noise": [1], "b": [9], "a": [5]})
    )
    reordered = _predict(
        results_dir,
        pd.DataFrame({"a_dup": [5], "const": [100], "b": [9], "id": [4]}),
    )
    assert canonical["y"].tolist() == reordered["y"].tolist()
    assert canonical["y"].tolist() == pytest.approx([10.0])


def test_string_include_and_exclude(results_dir):
    frame = pd.DataFrame({"a": [1, 2, 3], "b": [4, 5, 6], "y": [1, 2, 3]})
    description = _fit(
        results_dir,
        frame,
        _regression({"include": "a", "exclude": "b"}),
    )
    assert description["input_features"] == ["a"]
    assert description["dropped_features"]["excluded"] == []


def test_multi_target_schema_and_alias(results_dir):
    frame = pd.DataFrame(
        {
            "a": [1, 2, 3, 4],
            "a_dup": [1, 2, 3, 4],
            "b": [1, 0, 1, 0],
            "y1": [2, 4, 6, 8],
            "y2": [1, 0, 1, 0],
        }
    )
    description = _fit(
        results_dir,
        frame,
        _regression(
            {"drop_duplicate": True},
            targets=["y1", "y2"],
        ),
    )
    assert description["input_features"] == ["a", "b"]
    assert description["duplicate_feature_aliases"] == {"a": ["a_dup"]}
    predicted = _predict(
        results_dir, pd.DataFrame({"a_dup": [5], "b": [1], "extra": [9]})
    )
    assert list(predicted.columns) == ["y1", "y2"]
    assert predicted["y1"].tolist() == pytest.approx([10.0])


def test_clustering_schema_and_alias(results_dir):
    frame = pd.DataFrame(
        {
            "a": [0, 0, 0, 5, 5, 5],
            "a_dup": [0, 0, 0, 5, 5, 5],
            "const": [1, 1, 1, 1, 1, 1],
            "b": [0, 1, 0, 5, 4, 5],
        }
    )
    description = _fit(
        results_dir,
        frame,
        {
            "dataset": {
                "features": {
                    "include": ["b", "a", "a_dup", "const"],
                    "drop_constant": True,
                    "drop_duplicate": True,
                }
            },
            "model": {
                "type": "clustering",
                "algorithm": "KMeans",
                "arguments": {"n_clusters": 2, "n_init": 10, "random_state": 0},
            },
        },
    )
    assert description["target"] is None
    assert description["input_features"] == ["b", "a"]
    assert description["dropped_features"]["constant"] == ["const"]
    assert description["duplicate_feature_aliases"] == {"a": ["a_dup"]}

    canonical = _predict(
        results_dir, pd.DataFrame({"a": [0, 5], "b": [0, 5], "extra": [8, 8]})
    )
    alias = _predict(results_dir, pd.DataFrame({"b": [0, 5], "a_dup": [0, 5]}))
    assert canonical["result"].tolist() == alias["result"].tolist()


def test_classification_schema(results_dir):
    frame = pd.DataFrame(
        {
            "a": [0, 0, 1, 1],
            "a_dup": [0, 0, 1, 1],
            "noise": [1, 2, 3, 4],
            "y": [0, 0, 1, 1],
        }
    )
    description = _fit(
        results_dir,
        frame,
        {
            "dataset": {
                "features": {"include": ["a", "a_dup"], "drop_duplicate": True}
            },
            "model": {"type": "classification", "algorithm": "DecisionTree"},
            "target": ["y"],
        },
    )
    assert description["input_features"] == ["a"]
    predicted = _predict(results_dir, pd.DataFrame({"a_dup": [0, 1]}))
    assert [int(value) for value in predicted["y"].tolist()] == [0, 1]


def test_evaluate_applies_persisted_schema(results_dir):
    frame = pd.DataFrame(
        {"a": [1, 2, 3, 4], "b": [0, 1, 0, 1], "y": [2, 4, 6, 8]}
    )
    _fit(results_dir, frame, _regression({"include": ["a", "b"]}))
    eval_path = results_dir.parent / "eval.csv"
    pd.DataFrame({"b": [1], "extra": [5], "a": [5], "y": [10]}).to_csv(
        eval_path, index=False
    )
    Igel(
        cmd="evaluate",
        data_path=str(eval_path),
        model_path=str(results_dir / "model.joblib"),
        description_file=str(results_dir / "description.json"),
    )
    saved = json.loads(
        (results_dir / "evaluation.json").read_text(encoding="utf-8")
    )
    assert saved

    with pytest.raises(FeatureSchemaError, match="a"):
        missing = results_dir.parent / "missing.csv"
        pd.DataFrame({"b": [1], "y": [10]}).to_csv(missing, index=False)
        Igel(
            cmd="evaluate",
            data_path=str(missing),
            model_path=str(results_dir / "model.joblib"),
            description_file=str(results_dir / "description.json"),
        )


@pytest.mark.parametrize(
    "features, message",
    [
        ({"include": ["a", "a"]}, "Duplicated include entries: a"),
        ({"include": ["a", "missing"]}, "Unknown include entries: missing"),
        (
            {"include": ["a", "y"]},
            "Target columns cannot be used in include: y",
        ),
        ({"exclude": ["b", "b"]}, "Duplicated exclude entries: b"),
        ({"exclude": "missing"}, "Unknown exclude entries: missing"),
        ({"exclude": ["y"]}, "Target columns cannot be used in exclude: y"),
        ({"include": ""}, "non-empty"),
        ({"include": ["a", ""]}, "non-empty"),
        ({"exclude": ["a", "b"]}, "removed every feature"),
        ({"drop_constant": True}, "removed every feature"),
    ],
)
def test_feature_configuration_validation(results_dir, features, message):
    frame = pd.DataFrame({"a": [1, 1], "b": [1, 1], "y": [0, 1]})
    with pytest.raises(FeatureSchemaError, match=message):
        _fit(results_dir, frame, _regression(features))


def test_predict_conflict_names_columns(results_dir):
    frame = pd.DataFrame(
        {
            "a": [1, 2, 3],
            "a_dup": [1, 2, 3],
            "a_alias": [1, 2, 3],
            "b": [0, 1, 0],
            "y": [2, 4, 6],
        }
    )
    _fit(results_dir, frame, _regression({"drop_duplicate": True}))
    with pytest.raises(FeatureSchemaError, match="a_dup") as caught:
        _predict(
            results_dir,
            pd.DataFrame({"a": [1, 1], "a_dup": [1, 9], "b": [0, 1]}),
        )
    assert "a" in str(caught.value)
    with pytest.raises(FeatureSchemaError, match="a_alias") as caught_aliases:
        _predict(
            results_dir,
            pd.DataFrame({"a_dup": [1, 1], "a_alias": [1, 8], "b": [0, 0]}),
        )
    assert "a_dup" in str(caught_aliases.value)


def test_features_absent_does_not_write_schema(results_dir):
    frame = pd.DataFrame({"a": [1, 2, 3], "y": [1, 2, 3]})
    _fit(
        results_dir,
        frame,
        {
            "dataset": {"type": "csv"},
            "model": {"type": "regression", "algorithm": "LinearRegression"},
            "target": ["y"],
        },
    )
    description = json.loads(
        (results_dir / "description.json").read_text(encoding="utf-8")
    )
    assert "feature_schema_path" not in description
    assert not (results_dir / "feature_schema.joblib").exists()


def test_export_uses_description_width(results_dir):
    import onnx

    frame = pd.DataFrame(
        {
            "a": [1, 2, 3, 4],
            "b": [0, 1, 0, 1],
            "c": [1, 1, 1, 1],
            "y": [2, 4, 6, 8],
        }
    )
    _fit(results_dir, frame, _regression({"include": ["a", "b"]}))
    Igel(cmd="export", model_path=str(results_dir / "model.joblib"))
    model = onnx.load(str(results_dir / "model.onnx"))
    width = model.graph.input[0].type.tensor_type.shape.dim[1].dim_value
    assert width == 2


def test_predict_endpoint_schema_errors(results_dir, monkeypatch):
    frame = pd.DataFrame(
        {
            "a": [1, 2, 3, 4],
            "a_dup": [1, 2, 3, 4],
            "b": [0, 1, 0, 1],
            "y": [2, 4, 6, 8],
        }
    )
    _fit(results_dir, frame, _regression({"drop_duplicate": True}))
    monkeypatch.setenv(Constants.model_results_path, str(results_dir))
    client = TestClient(app)

    ok = client.post("/predict", json={"extra": 3, "b": 9, "a_dup": 5})
    assert ok.status_code == 200
    assert ok.json()["prediction"][0][0] == pytest.approx(10.0)

    missing = client.post("/predict", json={"b": 9, "extra": 1})
    assert missing.status_code == 400
    assert "a" in missing.json()["detail"]

    conflict = client.post("/predict", json={"a": 5, "a_dup": 4, "b": 9})
    assert conflict.status_code == 400
    detail = conflict.json()["detail"]
    assert "a" in detail
    assert "a_dup" in detail
