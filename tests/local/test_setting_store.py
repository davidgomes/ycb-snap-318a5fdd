import sys
from pathlib import Path
from unittest.mock import patch

import pytest

from mnamer.setting_store import SettingStore
from mnamer.types import DaemonAction, MediaType, ProviderType
from tests import DEFAULT_SETTINGS

pytestmark = pytest.mark.local


@pytest.mark.parametrize(
    "item", DEFAULT_SETTINGS.items(), ids=tuple(DEFAULT_SETTINGS.keys())
)
def test_as_dict(item):
    settings = SettingStore()
    k, v = item
    assert settings.as_dict()[k] == v


@pytest.mark.parametrize(
    "api", (ProviderType.TMDB, ProviderType.OMDB), ids=("TMDB", "OMDB")
)
def test_api_for__movie(api: ProviderType):
    settings = SettingStore(movie_api=api)
    assert settings.api_for(MediaType.MOVIE) is api


@pytest.mark.parametrize("api", ProviderType)
def test_api_key_for(api: ProviderType):
    settings = SettingStore()
    setattr(settings, f"api_key_{api.value}", "xxx")
    assert settings.api_key_for(api) == "xxx"


def test_bulk_apply__keeps_numeric_zeros():
    settings = SettingStore()
    settings.bulk_apply({"batch_size": 0, "stability_checks": 0, "batch": False})
    assert settings.batch_size == 0
    assert settings.stability_checks == 0
    assert settings.batch is False


def test_load__daemon_arguments():
    argv = [
        "mnamer",
        "--config-ignore",
        "--daemon",
        "start",
        "--batch",
        "--daemon-state",
        "state.json",
        "--daemon-config",
        "config.json",
        "--movie-directory",
        "/movies",
        "--stability-interval-ms",
        "250",
        "--stability-checks",
        "3",
        "--batch-size",
        "0",
        "--notify-webhook",
        "http://localhost/hook",
        "--lines",
        "5",
        "--watch",
        "/a",
        "/b",
    ]
    settings = SettingStore()
    with patch.object(sys, "argv", argv):
        settings.load()
    assert settings.daemon is DaemonAction.START
    assert settings.batch is True
    assert settings.daemon_state == "state.json"
    assert settings.daemon_config == "config.json"
    assert settings.movie_directory == Path("/movies")
    assert settings.stability_interval_ms == 250
    assert settings.stability_checks == 3
    assert settings.batch_size == 0
    assert settings.notify_webhook == "http://localhost/hook"
    assert settings.lines == 5
    assert settings.watch == ["/a", "/b"]


def test_load__daemon_run_once_directives():
    argv = [
        "mnamer",
        "--config-ignore",
        "--daemon-run-once",
        "--dry-run",
        "--validate-daemon-config",
    ]
    settings = SettingStore()
    with patch.object(sys, "argv", argv):
        settings.load()
    assert settings.daemon_run_once is True
    assert settings.dry_run is True
    assert settings.validate_daemon_config is True
