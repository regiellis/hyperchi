"""Offline checks on the release workflow: no network, no Docker, no Dokploy.

    python3 -m unittest discover -s scripts -p 'test_deployment.py'
"""
from __future__ import annotations

import os
import re
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import dokploy  # noqa: E402
import release  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent


class ManifestTests(unittest.TestCase):
    def test_deploy_manifest_only_pulls(self) -> None:
        text = (ROOT / "docker-compose.deploy.yml").read_text()
        for line in text.splitlines():
            bare = line.split("#", 1)[0].strip()
            self.assertFalse(bare.startswith("build"), f"deploy manifest declares a build key: {line!r}")
        self.assertIn(f"/{release.NAMESPACE}}}/{release.IMAGE}:${{TAG", text)
        self.assertIn("pull_policy: always", text)
        self.assertNotIn("ports:", text, "production publishes no host ports; Dokploy routes to web:8080")

    def test_container_is_confined(self) -> None:
        text = (ROOT / "docker-compose.deploy.yml").read_text()
        for setting in ("read_only: true", "no-new-privileges:true", "cap_drop:", "mem_limit:", "pids_limit:"):
            self.assertIn(setting, text)
        lines = (ROOT / "Dockerfile.dockerignore").read_text().splitlines()
        self.assertIn("**", lines)
        self.assertNotIn("!scripts/", lines)


class SettingsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.saved = dict(os.environ)
        for key in list(os.environ):
            if key.startswith(("HC_", "DOKPLOY_")):
                del os.environ[key]

    def tearDown(self) -> None:
        os.environ.clear()
        os.environ.update(self.saved)

    def test_release_requires_vps(self) -> None:
        os.environ["HC_RELEASE_TAG"] = "abc123"
        with self.assertRaises(SystemExit):
            release.settings()

    def test_release_rejects_bad_tag(self) -> None:
        os.environ["HC_RELEASE_TAG"] = "not ok"
        os.environ["HC_RELEASE_VPS"] = "root@example.invalid"
        with self.assertRaises(SystemExit):
            release.settings()

    def test_release_settings_shape(self) -> None:
        os.environ["HC_RELEASE_TAG"] = "abc123"
        os.environ["HC_RELEASE_VPS"] = "root@example.invalid"
        tag, vps, port = release.settings()
        self.assertEqual((tag, vps, port), ("abc123", "root@example.invalid", "19500"))
        self.assertEqual(release.image(port, tag), "localhost:19500/hyperchi/hyperchi-web:abc123")

    def test_dokploy_requires_credentials(self) -> None:
        with self.assertRaises(SystemExit):
            dokploy._config()

    def test_dokploy_rejects_plain_http_off_loopback(self) -> None:
        os.environ["DOKPLOY_HOST"] = "http://dokploy.example.invalid/api"
        os.environ["DOKPLOY_API"] = "key"
        with self.assertRaises(SystemExit):
            dokploy._config(need_compose=False)

    def test_service_name_default(self) -> None:
        self.assertEqual(dokploy.SERVICE_NAME, "www-hyperchi-go")
        self.assertEqual(dokploy.DOMAIN, "")




class ConfiguredDomainTests(unittest.TestCase):
    def setUp(self):
        from unittest.mock import patch
        domain = patch.object(dokploy, "DOMAIN", "hyperchi.example")
        domain.start()
        self.addCleanup(domain.stop)


class ReleaseSafetyTests(ConfiguredDomainTests):
    def setUp(self):
        from unittest.mock import patch
        super().setUp()
        self.lock = patch.object(dokploy, "release_lock", __import__("contextlib").nullcontext)
        self.lock.start()
        self.addCleanup(self.lock.stop)

    def test_busy_release_makes_no_writes(self):
        from unittest.mock import patch
        with patch.object(dokploy, "_service", return_value={"composeStatus": "running"}), \
                patch.object(dokploy, "_call") as api:
            with self.assertRaises(SystemExit):
                release.deploy("new-tag")
            api.assert_not_called()

    def test_busy_standalone_mutations_make_no_writes(self):
        from unittest.mock import patch
        with patch.object(dokploy, "_service", return_value={"composeStatus": "queued"}), \
                patch.object(dokploy, "_call") as api:
            for command in (dokploy.cmd_sync, lambda: dokploy.cmd_env_set("TAG=new"), dokploy.cmd_deploy):
                with self.assertRaises(SystemExit):
                    command()
            api.assert_not_called()

    def test_environment_update_preserves_multiline_values(self):
        from unittest.mock import patch
        existing = "CERT='first  \n  second=value\nthird'\nTAG='old'\nOTHER=untouched"
        with patch.object(dokploy, "_service", return_value={"env": existing}), \
                patch.object(dokploy, "_call") as api:
            dokploy.cmd_env_set("TAG=new")
        updated = api.call_args.args[1]["env"]
        self.assertEqual(updated, existing.replace("TAG='old'", "TAG='new'"))

    def test_malformed_environment_fails_before_write(self):
        from unittest.mock import patch
        for value in ("BAD='unterminated", "unrecognized line", "A=one\nA=two"):
            with self.subTest(value=value), patch.object(dokploy, "_service", return_value={"env": value}), \
                    patch.object(dokploy, "_call") as api:
                with self.assertRaises(SystemExit):
                    dokploy.cmd_env_set("TAG=new")
                api.assert_not_called()

    def test_env_display_never_prints_multiline_contents(self):
        import contextlib
        import io
        from unittest.mock import patch
        output = io.StringIO()
        with patch.object(dokploy, "_service", return_value={"env": "CERT='first\nsecond'"}), \
                contextlib.redirect_stdout(output):
            dokploy.cmd_env()
        self.assertEqual(output.getvalue(), "  CERT=<set>\n")

    def test_changed_tag_refuses_deployment(self):
        from unittest.mock import patch
        with patch.object(dokploy, "_service", return_value={"env": "TAG='unexpected'"}), \
                patch.object(dokploy, "_call") as api:
            with self.assertRaises(SystemExit):
                dokploy.cmd_deploy(expected_tag="wanted")
            api.assert_not_called()

    def test_deployment_done_requires_public_verification(self):
        from unittest.mock import patch
        with patch.object(dokploy, "_service", return_value={"env": "TAG='wanted'"}), \
                patch.object(dokploy, "_call", side_effect=[[], None, [{"deploymentId": "new", "status": "done"}]]), \
                patch.object(dokploy, "verify_site", side_effect=SystemExit("unhealthy")) as verify:
            with self.assertRaises(SystemExit):
                dokploy.cmd_deploy(expected_tag="wanted")
            verify.assert_called_once_with("wanted")


class PublicVerificationTests(ConfiguredDomainTests):
    @staticmethod
    def response(body, tag="wanted", status=200):
        from unittest.mock import MagicMock
        result = MagicMock()
        result.__enter__.return_value = result
        result.status = status
        result.headers = {"X-Release": tag}
        result.read.return_value = body
        return result

    def test_checks_both_routes_without_credentials(self):
        from unittest.mock import patch
        with patch.object(dokploy.urllib.request, "build_opener") as build:
            build.return_value.open.side_effect = [self.response(b"ok\n"), self.response(b'<title>HyperChi</title><main id="main">')]
            dokploy.verify_site("wanted")
            calls = build.return_value.open.call_args_list
            self.assertEqual(len(calls), 2)
            for call in calls:
                self.assertNotIn("X-api-key", dict(call.args[0].header_items()))
                self.assertTrue(call.args[0].full_url.startswith("https://"))

    def test_rejects_stale_release_or_broken_page(self):
        from unittest.mock import patch
        for responses in ([self.response(b"ok\n", tag="old")],
                          [self.response(b"ok\n"), self.response(b"broken")],
                          [self.response(b"ok\n", status=503)]):
            with self.subTest(responses=responses), patch.object(dokploy.urllib.request, "build_opener") as build, \
                    patch.object(dokploy.time, "monotonic", side_effect=[0, 0, 0, 0, 121, 121]), \
                    patch.object(dokploy.time, "sleep"):
                build.return_value.open.side_effect = responses
                with self.assertRaises(SystemExit):
                    dokploy.verify_site("wanted")


class LockTests(unittest.TestCase):
    def test_second_process_cannot_enter_release(self):
        import subprocess
        import tempfile
        from unittest.mock import patch
        scratch = ROOT / "tmp"
        scratch.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=scratch) as directory, \
                patch.object(dokploy, "COMPOSE_FILE", Path(directory) / "compose.yml"):
            code = "import pathlib,dokploy; dokploy.COMPOSE_FILE=pathlib.Path(" + repr(str(Path(directory) / "compose.yml")) + ");\nwith dokploy.release_lock(): print('entered')"
            env = {**os.environ, "PYTHONPATH": str(ROOT / "scripts")}
            with dokploy.release_lock():
                result = subprocess.run([sys.executable, "-c", code], env=env, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("entered", result.stdout)
            result = subprocess.run([sys.executable, "-c", code], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
