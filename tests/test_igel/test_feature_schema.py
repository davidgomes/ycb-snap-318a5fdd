import json
from pathlib import Path

import joblib
import pandas as pd
import pytest
import yaml
from fastapi.testclient import TestClient

from igel.feature_schema import FeatureSchemaError
from igel.igel import Igel
from igel.servers.fastapi_server import app


def _use_results(tmp_path, monkeypatch):
    results = tmp_path / "model_results"
    results.mkdir()
    monkeypatch.setattr(Igel, "results_path", results)
    monkeypatch.setattr(Igel, "default_model_path", results / "model.joblib")
    monkeypatch.setattr(Igel, "default_onnx_model_path", results / "model.onnx")
    monkeypatch.setattr(Igel, "description_file", results / "description.json")
    monkeypatch.setattr(Igel, "evaluation_file", results / "evaluation.json")
    monkeypatch.setattr(Igel, "prediction_file", results / "predictions.csv")
    return results


def _write_yaml(path, features, model_type="regression", algorithm="LinearRegression", target=None):
    payload = {
        "dataset": {
            "type": "csv",
            "features": features,
        },
        "model": {
            "type": model_type,
            "algorithm": algorithm,
            **(
                {"arguments": {"n_clusters": 2}}
                if model_type == "clustering"
                else {}
            ),
        },
    }
    if model_type != "clustering":
        payload["target"] = target or ["y"]
    else:
        payload["target"] = None
    path.write_text(yaml.safe_dump(payload))


def _frame():
    return pd.DataFrame(
        {
            "a": [1, 2, 3, 4],
            "b": [10, 20, 30, 40],
            "b_copy": [10, 20, 30, 40],
            "const": [7, 7, 7, 7],
            "extra": [5, 6, 7, 8],
            "y": [0, 1, 0, 1],
            "y2": [1, 0, 1, 0],
        }
    )


def test_fit_persists_schema_and_predict_uses_aliases(tmp_path, monkeypatch):
    results = _use_results(tmp_path, monkeypatch)
    monkeypatch.chdir(tmp_path)
    data = tmp_path / "train.csv"
    _frame().to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        {
            "include": ["b", "a", "b_copy", "const", "extra"],
            "exclude": "extra",
            "drop_constant": True,
            "drop_duplicate": True,
        },
    )

    Igel(cmd="fit", data_path=str(data), yaml_path=str(config))

    description = json.loads((results / "description.json").read_text())
    assert description["input_features"] == ["b", "a"]
    assert description["dropped_features"] == {
        "excluded": ["extra"],
        "constant": ["const"],
        "duplicate": ["b_copy"],
    }
    assert description["duplicate_feature_aliases"] == {"b": ["b_copy"]}
    schema_path = Path(description["feature_schema_path"])
    assert schema_path.name == "feature_schema.joblib"
    assert schema_path.exists()
    assert joblib.load(schema_path)["input_features"] == ["b", "a"]

    predict_path = tmp_path / "predict.csv"
    pd.DataFrame(
        {"a": [8, 9], "b_copy": [80, 90], "unused": [1, 1]}
    ).to_csv(predict_path, index=False)
    Igel(
        cmd="predict",
        data_path=str(predict_path),
        model_path=str(results / "model.joblib"),
        description_file=str(results / "description.json"),
        prediction_file=str(tmp_path / "preds.csv"),
    )
    preds = pd.read_csv(tmp_path / "preds.csv")
    assert list(preds.columns) == ["y"]
    assert len(preds) == 2


def test_conflicting_aliases_and_missing_features(tmp_path, monkeypatch):
    results = _use_results(tmp_path, monkeypatch)
    monkeypatch.chdir(tmp_path)
    data = tmp_path / "train.csv"
    _frame().to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(
        config,
        {"include": ["a", "b", "b_copy"], "drop_duplicate": True},
    )
    Igel(cmd="fit", data_path=str(data), yaml_path=str(config))

    conflict = tmp_path / "conflict.csv"
    pd.DataFrame({"a": [1], "b": [1], "b_copy": [2]}).to_csv(conflict, index=False)
    with pytest.raises(FeatureSchemaError, match="b_copy"):
        Igel(
            cmd="predict",
            data_path=str(conflict),
            model_path=str(results / "model.joblib"),
            description_file=str(results / "description.json"),
        )

    missing = tmp_path / "missing.csv"
    pd.DataFrame({"a": [1], "y": [0]}).to_csv(missing, index=False)
    with pytest.raises(FeatureSchemaError, match="b"):
        Igel(
            cmd="evaluate",
            data_path=str(missing),
            model_path=str(results / "model.joblib"),
            description_file=str(results / "description.json"),
        )


@pytest.mark.parametrize(
    "features, message",
    [
        ({"include": ["a", "a"]}, "duplicated include"),
        ({"include": ["nope"]}, "unknown include"),
        ({"exclude": ["y"]}, "target columns"),
        ({"include": ""}, "non-empty"),
        ({"include": ["a"], "exclude": ["a"]}, "removes every feature"),
        ({"drop_constant": True, "include": ["const"]}, "removes every feature"),
    ],
)
def test_feature_config_validation(tmp_path, monkeypatch, features, message):
    _use_results(tmp_path, monkeypatch)
    monkeypatch.chdir(tmp_path)
    data = tmp_path / "train.csv"
    _frame().to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(config, features)
    with pytest.raises(FeatureSchemaError, match=message):
        Igel(cmd="fit", data_path=str(data), yaml_path=str(config))


def test_multi_target_and_clustering(tmp_path, monkeypatch):
    results = _use_results(tmp_path, monkeypatch)
    monkeypatch.chdir(tmp_path)
    data = tmp_path / "train.csv"
    _frame().to_csv(data, index=False)

    multi = tmp_path / "multi.yaml"
    _write_yaml(multi, {"include": ["a", "b"], "exclude": ["const"]}, target=["y", "y2"])
    Igel(cmd="fit", data_path=str(data), yaml_path=str(multi))
    description = json.loads((results / "description.json").read_text())
    assert description["input_features"] == ["a", "b"]
    assert description["target"] == ["y", "y2"]

    held = tmp_path / "eval.csv"
    _frame().drop(columns=["extra"]).to_csv(held, index=False)
    Igel(
        cmd="evaluate",
        data_path=str(held),
        description_file=str(results / "description.json"),
    )

    cluster = tmp_path / "cluster.yaml"
    _write_yaml(
        cluster,
        {"exclude": ["const"], "drop_duplicate": True},
        model_type="clustering",
        algorithm="KMeans",
    )
    # fresh results
    Igel(cmd="fit", data_path=str(data), yaml_path=str(cluster))
    description = json.loads((results / "description.json").read_text())
    assert "y" in description["input_features"]
    assert "b_copy" in description["dropped_features"]["duplicate"]
    assert description["duplicate_feature_aliases"]["b"] == ["b_copy"]


def test_predict_endpoint_returns_400(tmp_path, monkeypatch):
    results = _use_results(tmp_path, monkeypatch)
    monkeypatch.chdir(tmp_path)
    data = tmp_path / "train.csv"
    _frame().to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(config, {"include": ["a", "b"]})
    Igel(cmd="fit", data_path=str(data), yaml_path=str(config))
    monkeypatch.setenv("IGEL_MODEL_RESULTS_PATH", str(results))

    client = TestClient(app)
    response = client.post("/predict", json={"a": [1, 2]})
    assert response.status_code == 400
    body = response.json()
    assert "detail" in body
    assert "b" in body["detail"]


def test_export_uses_description_width(tmp_path, monkeypatch):
    results = _use_results(tmp_path, monkeypatch)
    monkeypatch.chdir(tmp_path)
    data = tmp_path / "train.csv"
    _frame().to_csv(data, index=False)
    config = tmp_path / "igel.yaml"
    _write_yaml(config, {"include": ["a", "b"]})
    Igel(cmd="fit", data_path=str(data), yaml_path=str(config))
    Igel(
        cmd="export",
        model_path=str(results / "model.joblib"),
    )
    assert (results / "model.onnx").exists()
