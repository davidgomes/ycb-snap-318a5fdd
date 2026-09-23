import json
from pathlib import Path

import joblib
import pandas as pd
import pytest
import yaml
from fastapi.testclient import TestClient
from igel import Igel
from igel.constants import Constants
from igel.features import FeatureSchemaError

import igel.servers.fastapi_server as server


def _use_results(monkeypatch, results_dir):
    results_dir.mkdir(parents=True, exist_ok=True)
    monkeypatch.setattr(Igel, "results_path", results_dir)
    monkeypatch.setattr(Igel, "default_model_path", results_dir / "model.joblib")
    monkeypatch.setattr(
        Igel, "default_onnx_model_path", results_dir / "model.onnx"
    )
    monkeypatch.setattr(
        Igel, "description_file", results_dir / "description.json"
    )
    monkeypatch.setattr(
        Igel, "evaluation_file", results_dir / "evaluation.json"
    )
    monkeypatch.setattr(
        Igel, "prediction_file", results_dir / "predictions.csv"
    )


def _write_yaml(path, dataset, model, target=None):
    payload = {"dataset": dataset, "model": model}
    if target is not None:
        payload["target"] = target
    path.write_text(yaml.safe_dump(payload))


def _fit(data_path, yaml_path):
    Igel(cmd="fit", data_path=str(data_path), yaml_path=str(yaml_path))


def _description(results_dir):
    with open(results_dir / "description.json") as handle:
        return json.load(handle)


def test_fit_persists_selected_schema_for_single_target(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)
    data = tmp_path / "train.csv"
    pd.DataFrame(
        {
            "b": [1, 2, 3, 4],
            "a": [10, 20, 30, 40],
            "a_copy": [10, 20, 30, 40],
            "c": [7, 7, 7, 7],
            "d": [1, 0, 1, 0],
            "extra": [5, 6, 7, 8],
            "y": [0, 1, 0, 1],
        }
    ).to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        dataset={
            "features": {
                "include": ["b", "a", "a_copy", "c", "d"],
                "exclude": "d",
                "drop_constant": True,
                "drop_duplicate": True,
            }
        },
        model={
            "type": "regression",
            "algorithm": "LinearRegression",
        },
        target=["y"],
    )

    _fit(data, config)

    schema_file = results / Constants.feature_schema_file
    assert schema_file.exists()
    description = _description(results)
    assert Path(description["feature_schema_path"]).name == (
        Constants.feature_schema_file
    )
    assert description["input_features"] == ["b", "a"]
    assert description["dropped_features"] == {
        "excluded": ["d"],
        "constant": ["c"],
        "duplicate": ["a_copy"],
    }
    assert description["duplicate_feature_aliases"] == {"a": ["a_copy"]}
    stored = joblib.load(schema_file)
    assert stored["input_features"] == description["input_features"]
    assert stored["dropped_features"] == description["dropped_features"]
    assert (
        stored["duplicate_feature_aliases"]
        == description["duplicate_feature_aliases"]
    )


def test_predict_and_evaluate_apply_persisted_schema(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)
    train = tmp_path / "train.csv"
    pd.DataFrame(
        {
            "a": [1, 2, 3, 4],
            "a_copy": [1, 2, 3, 4],
            "c": [9, 9, 9, 9],
            "y": [1, 2, 3, 4],
        }
    ).to_csv(train, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        dataset={
            "features": {
                "drop_constant": True,
                "drop_duplicate": True,
            }
        },
        model={"type": "regression", "algorithm": "LinearRegression"},
        target=["y"],
    )
    _fit(train, config)
    assert _description(results)["input_features"] == ["a"]

    canonical = tmp_path / "canonical.csv"
    pd.DataFrame({"a": [5, 6], "y": [5, 6]}).to_csv(canonical, index=False)
    Igel(cmd="predict", data_path=str(canonical))
    expected = pd.read_csv(results / "predictions.csv")

    reordered = tmp_path / "reordered.csv"
    pd.DataFrame(
        {
            "extra": [100, 200],
            "a_copy": [5, 6],
            "c": [1, 2],
            "unused": [0, 1],
        }
    ).to_csv(reordered, index=False)
    Igel(cmd="predict", data_path=str(reordered))
    via_alias = pd.read_csv(results / "predictions.csv")
    pd.testing.assert_frame_equal(via_alias, expected)

    both = tmp_path / "both.csv"
    pd.DataFrame({"a": [5, 6], "a_copy": [5, 6], "extra": [3, 4]}).to_csv(
        both, index=False
    )
    Igel(cmd="predict", data_path=str(both))
    pd.testing.assert_frame_equal(
        pd.read_csv(results / "predictions.csv"), expected
    )

    evaluate_data = tmp_path / "eval.csv"
    pd.DataFrame(
        {"c": [3, 8], "extra": [1, 1], "a": [2, 3], "y": [2, 3]}
    ).to_csv(evaluate_data, index=False)
    Igel(cmd="evaluate", data_path=str(evaluate_data))
    assert (results / "evaluation.json").exists()


def test_schema_errors_name_missing_and_conflicting_columns(
    tmp_path, monkeypatch
):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)
    train = tmp_path / "train.csv"
    pd.DataFrame(
        {"a": [1, 2, 3, 4], "a_copy": [1, 2, 3, 4], "y": [1, 2, 3, 4]}
    ).to_csv(train, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        dataset={"features": {"drop_duplicate": True}},
        model={"type": "regression", "algorithm": "LinearRegression"},
        target=["y"],
    )
    _fit(train, config)

    missing = tmp_path / "missing.csv"
    pd.DataFrame({"extra": [1, 2]}).to_csv(missing, index=False)
    with pytest.raises(FeatureSchemaError, match="a") as missing_error:
        Igel(cmd="predict", data_path=str(missing))
    assert "missing required features" in str(missing_error.value)

    conflict = tmp_path / "conflict.csv"
    pd.DataFrame({"a": [1, 2], "a_copy": [1, 9]}).to_csv(conflict, index=False)
    with pytest.raises(FeatureSchemaError, match="a_copy") as conflict_error:
        Igel(cmd="predict", data_path=str(conflict))
    message = str(conflict_error.value)
    assert "conflicting columns" in message
    assert "a" in message


def test_feature_config_validation_errors(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)
    data = tmp_path / "train.csv"
    pd.DataFrame({"a": [1, 2, 3], "b": [3, 2, 1], "y": [0, 1, 0]}).to_csv(
        data, index=False
    )
    model = {"type": "classification", "algorithm": "DecisionTree"}

    def fit_features(features, name):
        config = tmp_path / f"{name}.yaml"
        _write_yaml(
            config,
            dataset={"features": features},
            model=model,
            target=["y"],
        )
        Igel(cmd="fit", data_path=str(data), yaml_path=str(config))

    with pytest.raises(FeatureSchemaError, match="unknown include"):
        fit_features({"include": ["a", "missing"]}, "unknown")
    with pytest.raises(FeatureSchemaError, match="duplicated include"):
        fit_features({"include": ["a", "a"]}, "duplicate")
    with pytest.raises(FeatureSchemaError, match="non-empty"):
        fit_features({"include": ["a", ""]}, "empty")
    with pytest.raises(FeatureSchemaError, match="target columns in exclude"):
        fit_features({"exclude": "y"}, "target")
    with pytest.raises(FeatureSchemaError, match="every feature"):
        fit_features({"exclude": ["a", "b"]}, "none")
    with pytest.raises(FeatureSchemaError, match="include must be"):
        fit_features({"include": {"a": True}}, "bad-type")


def test_multi_target_and_clustering_share_schema_rules(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)

    multi_data = tmp_path / "multi.csv"
    pd.DataFrame(
        {
            "a": [1, 2, 3, 4],
            "a_copy": [1, 2, 3, 4],
            "noise": [8, 8, 8, 8],
            "y1": [1, 2, 3, 4],
            "y2": [2, 3, 4, 5],
        }
    ).to_csv(multi_data, index=False)
    multi_config = tmp_path / "multi.yaml"
    _write_yaml(
        multi_config,
        dataset={
            "features": {
                "include": "a",
                "drop_constant": True,
                "drop_duplicate": True,
            }
        },
        model={"type": "regression", "algorithm": "LinearRegression"},
        target=["y1", "y2"],
    )
    _fit(multi_data, multi_config)
    description = _description(results)
    assert description["input_features"] == ["a"]
    assert description["target"] == ["y1", "y2"]

    with pytest.raises(FeatureSchemaError, match="target columns in include"):
        bad = tmp_path / "bad-multi.yaml"
        _write_yaml(
            bad,
            dataset={"features": {"include": ["a", "y2"]}},
            model={"type": "regression", "algorithm": "LinearRegression"},
            target=["y1", "y2"],
        )
        _fit(multi_data, bad)

    cluster_data = tmp_path / "cluster.csv"
    pd.DataFrame(
        {
            "a": [0, 0, 0, 5, 5, 5],
            "a_copy": [0, 0, 0, 5, 5, 5],
            "c": [1, 1, 1, 1, 1, 1],
            "b": [1, 2, 3, 8, 9, 10],
        }
    ).to_csv(cluster_data, index=False)
    cluster_config = tmp_path / "cluster.yaml"
    _write_yaml(
        cluster_config,
        dataset={
            "features": {
                "include": ["b", "a", "a_copy", "c"],
                "drop_constant": True,
                "drop_duplicate": True,
            }
        },
        model={
            "type": "clustering",
            "algorithm": "KMeans",
            "arguments": {"n_clusters": 2, "n_init": 10, "random_state": 0},
        },
    )
    _fit(cluster_data, cluster_config)
    clustered = _description(results)
    assert clustered["input_features"] == ["b", "a"]
    assert clustered["dropped_features"]["constant"] == ["c"]
    assert clustered["duplicate_feature_aliases"] == {"a": ["a_copy"]}

    predict_data = tmp_path / "cluster-predict.csv"
    pd.DataFrame(
        {"a_copy": [0, 5], "b": [2, 9], "extra": [4, 4], "c": [3, 9]}
    ).to_csv(predict_data, index=False)
    Igel(cmd="predict", data_path=str(predict_data))
    predictions = pd.read_csv(results / "predictions.csv")
    assert len(predictions) == 2


def test_export_uses_description_input_width(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)
    data = tmp_path / "train.csv"
    pd.DataFrame(
        {"a": [1, 2, 3, 4], "b": [4, 3, 2, 1], "y": [1, 2, 3, 4]}
    ).to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        dataset={"features": {"include": ["b", "a"]}},
        model={"type": "regression", "algorithm": "LinearRegression"},
        target=["y"],
    )
    _fit(data, config)
    Igel(cmd="export", model_path=str(results / "model.joblib"))

    import onnx

    model = onnx.load(str(results / "model.onnx"))
    width = model.graph.input[0].type.tensor_type.shape.dim[1].dim_value
    assert width == len(_description(results)["input_features"]) == 2


def test_predict_endpoint_returns_400_for_schema_errors(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    _use_results(monkeypatch, results)
    data = tmp_path / "train.csv"
    pd.DataFrame({"a": [1, 2, 3, 4], "y": [1, 2, 3, 4]}).to_csv(
        data, index=False
    )
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        dataset={"features": {"include": ["a"]}},
        model={"type": "regression", "algorithm": "LinearRegression"},
        target=["y"],
    )
    _fit(data, config)

    monkeypatch.setenv(Constants.model_results_path, str(results))
    monkeypatch.setattr(
        server, "temp_post_req_data_path", tmp_path / "post_req_data.csv"
    )
    client = TestClient(server.app)
    response = client.post("/predict", json={"extra": [1, 2], "other": [3, 4]})
    assert response.status_code == 400
    detail = response.json()["detail"]
    assert "a" in detail
    assert "missing required features" in detail

    ok = client.post("/predict", json={"extra": [9, 8], "a": [1, 2]})
    assert ok.status_code == 200
    assert len(ok.json()["prediction"]) == 2
