"""Smoke tests for the CLI entry point.

We mock ``init_session`` and ``resolve_ids`` (the two Steam-layer entry
points) so we can drive ``cmd_swd`` without Steam installed.
"""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from swd import cli


def test_swd_help_exits_zero() -> None:
    code = cli.cmd_swd(["--help"])
    # argparse exits via SystemExit; cmd_swd returns that code.
    assert code == 0  # argparse's --help prints and exits with SystemExit(0)


def test_swd_version_exits_zero() -> None:
    # cmd_swd catches argparse SystemExit and returns the code.
    assert cli.cmd_swd(["--version"]) == 0


def test_swd_unknown_flag_exits_two(capsys) -> None:
    # argparse exits via SystemExit(2) on unknown flags; cmd_swd returns it.
    code = cli.cmd_swd(["--no-such-flag"])
    assert code == 2


def test_parse_args_minimal() -> None:
    ns = cli.parse_args(["294100", "3683834622"])
    assert ns.appid == 294100
    assert ns.workshopid == [3683834622]
    assert ns.proxy is None
    assert ns.retries == 5
    assert ns.color is None  # auto
    assert ns.log_file is None


def test_parse_args_all_flags() -> None:
    ns = cli.parse_args(
        [
            "294100",
            "3683834622",
            "-o",
            "out",
            "-v",
            "--proxy",
            "socks5://127.0.0.1:1080",
            "--retries",
            "10",
            "--no-color",
            "--log-file",
            "run.log",
        ]
    )
    assert ns.output == "out"
    assert ns.verbose is True
    assert ns.proxy == "socks5://127.0.0.1:1080"
    assert ns.retries == 10
    assert ns.color is False
    assert str(ns.log_file) == "run.log"  # Path


def test_cmd_swd_init_failure_exits_one(tmp_path) -> None:
    """If init_session returns (None, None), cmd_swd must exit 1."""
    fake_log = MagicMock()
    with (
        patch.object(cli, "Log", return_value=fake_log),
        patch.object(cli, "init_session", return_value=(None, None)),
    ):
        code = cli.cmd_swd(["294100", "1"])
    assert code == 1


def test_cmd_swd_no_items_exits_one(tmp_path) -> None:
    fake_log = MagicMock()
    fake_client = MagicMock()
    with (
        patch.object(cli, "Log", return_value=fake_log),
        patch.object(cli, "init_session", return_value=(fake_client, MagicMock())),
        patch.object(cli, "resolve_ids", return_value=[]),
    ):
        code = cli.cmd_swd(["294100", "1"])
    assert code == 1
    fake_client.logout.assert_called_once()


def test_cmd_swd_full_run_exits_zero(tmp_path) -> None:
    """All-OK run returns 0 and writes files."""
    fake_log = MagicMock()
    fake_client = MagicMock()
    fake_cdn = MagicMock()
    with (
        patch.object(cli, "Log", return_value=fake_log),
        patch.object(cli, "init_session", return_value=(fake_client, fake_cdn)),
        patch.object(cli, "resolve_ids", return_value=[1001]),
        patch.object(cli, "download_item") as dl,
    ):
        from swd.ui import ItemStats

        dl.return_value = ItemStats(
            out_dir=tmp_path / "1001", ok=1, fail=0, bytes_done=10, duration=0.1
        )
        code = cli.cmd_swd(["294100", "1001", "-o", str(tmp_path)])
    assert code == 0
    assert dl.called
