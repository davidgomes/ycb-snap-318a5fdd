import json
from pathlib import Path

import joblib
import onnx
import pandas as pd
import pytest
import yaml
from fastapi.testclient import TestClient
from igel import Igel
from igel.constants import Constants
from igel.feature_schema import FeatureSchemaError, build_feature_schema
from igel.servers.fastapi_server import app


def _write_frame(path, frame):
    frame.to_csv(path, index=False)
    return path


def _write_yaml(path, payload):
    path.write_text(yaml.safe_dump(payload), encoding="utf-8")
    return path


@pytest.fixture
def results_dir(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    model_path = results / Constants.model_file
    description = results / Constants.description_file
    monkeypatch.setattr(Igel, "results_path", results)
    monkeypatch.setattr(Igel, "default_model_path", model_path)
    monkeypatch.setattr(Igel, "description_file", description)
    monkeypatch.setattr(
        Igel, "default_onnx_model_path", results / Constants.onnx_model_file
    )
    monkeypatch.setattr(
        Igel, "evaluation_file", results / Constants.evaluation_file
    )
    monkeypatch.setattr(
        Igel, "prediction_file", results / Constants.prediction_file
    )
    return results


def _supervised_frame():
    return pd.DataFrame(
        {
            "a": [1, 2, 3, 4, 5, 6],
            "b": [10, 20, 30, 40, 50, 60],
            "b_copy": [10, 20, 30, 40, 50, 60],
            "b_copy_2": [10, 20, 30, 40, 50, 60],
            "c": [5, 6, 7, 8, 9, 10],
            "const": [3, 3, 3, 3, 3, 3],
            "y": [0, 1, 0, 1, 0, 1],
            "y2": [1, 2, 3, 4, 5, 6],
        }
    )


def _fit(results_dir, tmp_path, frame, dataset, model, target):
    data_path = _write_frame(tmp_path / "train.csv", frame)
    yaml_path = _write_yaml(
        tmp_path / "igel.yaml",
        {"dataset": dataset, "model": model, "target": target},
    )
    Igel(cmd="fit", data_path=str(data_path), yaml_path=str(yaml_path))
    description_path = results_dir / Constants.description_file
    description = json.loads(description_path.read_text(encoding="utf-8"))
    return description


def test_build_schema_orders_drops_and_canonicalizes_duplicates():
    frame = _supervised_frame()
    schema = build_feature_schema(
        frame,
        {
            "include": ["c", "b", "a", "b_copy", "b_copy_2", "const"],
            "exclude": "c",
            "drop_constant": True,
            "drop_duplicate": True,
        },
        ["y", "y2"],
    )

    assert schema["input_features"] == ["b", "a"]
    assert schema["dropped_features"] == {
        "excluded": ["c"],
        "constant": ["const"],
        "duplicate": ["b_copy", "b_copy_2"],
    }
    assert schema["duplicate_feature_aliases"] == {
        "b": ["b_copy", "b_copy_2"]
    }


def test_include_and_exclude_validation_errors():
    frame = _supervised_frame()

    with pytest.raises(FeatureSchemaError, match="not_a_column"):
        build_feature_schema(frame, {"include": ["a", "not_a_column"]}, ["y"])

    with pytest.raises(FeatureSchemaError, match="Duplicate entries in include"):
        build_feature_schema(frame, {"include": ["a", "b", "a"]}, ["y"])

    with pytest.raises(FeatureSchemaError, match="Duplicate entries in exclude"):
        build_feature_schema(frame, {"exclude": ["c", "c"]}, ["y"])

    with pytest.raises(FeatureSchemaError, match="empty feature name"):
        build_feature_schema(frame, {"include": ["a", ""]}, ["y"])

    with pytest.raises(FeatureSchemaError, match="Target column"):
        build_feature_schema(frame, {"include": "y"}, ["y"])

    with pytest.raises(FeatureSchemaError, match="Target column"):
        build_feature_schema(frame, {"exclude": ["y2"]}, ["y", "y2"])

    with pytest.raises(FeatureSchemaError, match="removed every feature"):
        build_feature_schema(
            frame,
            {"exclude": ["a", "b", "b_copy", "b_copy_2", "c", "const"]},
            ["y", "y2"],
        )

    with pytest.raises(FeatureSchemaError, match="must be a boolean"):
        build_feature_schema(frame, {"drop_constant": "yes"}, ["y"])


def test_fit_persists_schema_for_single_target(results_dir, tmp_path):
    description = _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {
            "features": {
                "include": ["b", "a", "b_copy", "const", "c"],
                "exclude": ["c"],
                "drop_constant": True,
                "drop_duplicate": True,
            }
        },
        {"type": "regression", "algorithm": "LinearRegression"},
        ["y"],
    )

    schema_path = Path(description["feature_schema_path"])
    assert schema_path.name == "feature_schema.joblib"
    assert schema_path.is_file()
    assert schema_path.parent == results_dir
    stored = joblib.load(schema_path)
    assert description["input_features"] == ["b", "a"]
    assert stored["input_features"] == ["b", "a"]
    assert description["dropped_features"] == {
        "excluded": ["c"],
        "constant": ["const"],
        "duplicate": ["b_copy"],
    }
    assert description["duplicate_feature_aliases"] == {"b": ["b_copy"]}
    assert description["train_data_shape"][1] == 2


def test_predict_and_evaluate_apply_persisted_schema(results_dir, tmp_path):
    _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {
            "features": {
                "include": "b",
                "drop_duplicate": True,
            }
        },
        {"type": "regression", "algorithm": "LinearRegression"},
        ["y"],
    )
    # include is only b, so aliases are not recorded. Refit with duplicates kept
    # out of the selected set and an alias available at predict time.
    _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {
            "features": {
                "include": ["b", "a", "b_copy"],
                "drop_duplicate": True,
            }
        },
        {"type": "regression", "algorithm": "LinearRegression"},
        ["y"],
    )

    canonical = _write_frame(
        tmp_path / "canonical.csv",
        pd.DataFrame(
            {
                "a": [1, 2],
                "b": [10, 20],
                "extra": [7, 8],
                "y": [0, 1],
            }
        ),
    )
    alias = _write_frame(
        tmp_path / "alias.csv",
        pd.DataFrame(
            {
                "a": [1, 2],
                "b_copy": [10, 20],
                "unused": [4, 5],
            }
        ),
    )
    Igel(cmd="predict", data_path=str(canonical))
    canonical_pred = pd.read_csv(results_dir / Constants.prediction_file)
    Igel(cmd="predict", data_path=str(alias))
    alias_pred = pd.read_csv(results_dir / Constants.prediction_file)
    pd.testing.assert_frame_equal(canonical_pred, alias_pred)

    eval_path = _write_frame(
        tmp_path / "eval.csv",
        pd.DataFrame(
            {
                "b_copy": [10, 20, 30],
                "a": [1, 2, 3],
                "extra": [1, 1, 1],
                "y": [0, 1, 0],
            }
        ),
    )
    Igel(cmd="evaluate", data_path=str(eval_path))
    evaluation = json.loads(
        (results_dir / Constants.evaluation_file).read_text(encoding="utf-8")
    )
    assert evaluation

    missing = _write_frame(
        tmp_path / "missing.csv",
        pd.DataFrame({"a": [1, 2], "extra": [3, 4]}),
    )
    with pytest.raises(FeatureSchemaError, match="b"):
        Igel(cmd="predict", data_path=str(missing))

    conflict = _write_frame(
        tmp_path / "conflict.csv",
        pd.DataFrame(
            {
                "a": [1, 2],
                "b": [10, 20],
                "b_copy": [10, 99],
            }
        ),
    )
    with pytest.raises(FeatureSchemaError, match="b_copy") as caught:
        Igel(cmd="predict", data_path=str(conflict))
    assert "b" in str(caught.value)


def test_multi_target_and_clustering_schema(results_dir, tmp_path):
    description = _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {
            "features": {
                "include": ["a", "b", "const"],
                "drop_constant": True,
            }
        },
        {"type": "regression", "algorithm": "LinearRegression"},
        ["y", "y2"],
    )
    assert description["input_features"] == ["a", "b"]
    assert description["dropped_features"]["constant"] == ["const"]

    predict_path = _write_frame(
        tmp_path / "multi_predict.csv",
        pd.DataFrame({"b": [10, 20], "a": [1, 2], "noise": [8, 9]}),
    )
    Igel(cmd="predict", data_path=str(predict_path))
    predictions = pd.read_csv(results_dir / Constants.prediction_file)
    assert list(predictions.columns) == ["y", "y2"]
    assert len(predictions) == 2

    cluster = pd.DataFrame(
        {
            "a": [1, 1, 1, 8, 8, 8],
            "a_copy": [1, 1, 1, 8, 8, 8],
            "const": [4, 4, 4, 4, 4, 4],
        }
    )
    clustered = _fit(
        results_dir,
        tmp_path,
        cluster,
        {
            "features": {
                "drop_constant": True,
                "drop_duplicate": True,
            }
        },
        {
            "type": "clustering",
            "algorithm": "KMeans",
            "arguments": {"n_clusters": 2, "n_init": 10, "random_state": 0},
        },
        None,
    )
    assert clustered["input_features"] == ["a"]
    assert clustered["dropped_features"]["constant"] == ["const"]
    assert clustered["duplicate_feature_aliases"] == {"a": ["a_copy"]}

    cluster_predict = _write_frame(
        tmp_path / "clusters.csv",
        pd.DataFrame({"a_copy": [1, 8], "extra": [0, 1]}),
    )
    Igel(cmd="predict", data_path=str(cluster_predict))
    labels = pd.read_csv(results_dir / Constants.prediction_file)
    assert len(labels) == 2

    with pytest.raises(FeatureSchemaError, match="a"):
        Igel(
            cmd="evaluate",
            data_path=str(
                _write_frame(
                    tmp_path / "clusters_missing.csv",
                    pd.DataFrame({"extra": [1, 2, 3]}),
                )
            ),
        )


def test_export_uses_description_input_width(results_dir, tmp_path):
    _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {"features": {"include": ["a", "b", "c"]}},
        {"type": "regression", "algorithm": "LinearRegression"},
        ["y"],
    )
    Igel(cmd="export", model_path=str(results_dir / Constants.model_file))
    onnx_path = results_dir / Constants.onnx_model_file
    assert onnx_path.is_file()
    model = onnx.load(str(onnx_path))
    width = model.graph.input[0].type.tensor_type.shape.dim[1].dim_value
    assert width == 3


def test_predict_endpoint_returns_400_for_schema_errors(
    results_dir, tmp_path, monkeypatch
):
    _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {"features": {"include": ["a", "b"]}},
        {"type": "classification", "algorithm": "LogisticRegression"},
        ["y"],
    )
    monkeypatch.setenv(Constants.model_results_path, str(results_dir))
    client = TestClient(app)

    missing = client.post("/predict", json={"a": [1, 2], "extra": [3, 4]})
    assert missing.status_code == 400
    assert "b" in missing.json()["detail"]

    conflict = client.post(
        "/predict",
        json={"a": [1, 2], "b": [10, 20], "b_copy": [10, 99]},
    )
    # b_copy is not an alias unless drop_duplicate recorded it. This request
    # has the required columns, so it is a successful prediction.
    assert conflict.status_code == 200

    _fit(
        results_dir,
        tmp_path,
        _supervised_frame(),
        {
            "features": {
                "include": ["a", "b", "b_copy"],
                "drop_duplicate": True,
            }
        },
        {"type": "classification", "algorithm": "LogisticRegression"},
        ["y"],
    )
    conflict = client.post(
        "/predict",
        json={"a": [1, 2], "b": [10, 20], "b_copy": [10, 99]},
    )
    assert conflict.status_code == 400
    detail = conflict.json()["detail"]
    assert "b" in detail
    assert "b_copy" in detail

    ok = client.post(
        "/predict",
        json={"a": [1, 2], "b_copy": [10, 20], "extra": [5, 6]},
    )
    assert ok.status_code == 200
    assert "prediction" in ok.json()
