"""Installer preflight regression tests; no Docker daemon or root writes needed."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class InstallerPreflight(unittest.TestCase):
    def run_preflight(self, status="404", **overrides):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / "deploy/docker").mkdir(parents=True)
            (base / "deploy/docker/Dockerfile").touch()
            script = (ROOT / "deploy/install.sh").read_text()
            script = script.split("# ---- 2. sysctl")[0]
            script = script.replace('[[ $EUID -eq 0 ]] || die "run as root (sudo)"', ":")
            installer = base / "deploy/install.sh"
            installer.write_text(script)
            bin_dir = base / "bin"
            bin_dir.mkdir()
            mocks = {
                "docker": '''#!/usr/bin/env bash
echo "$*" >> "$CALL_LOG"
if [[ "$1" == version ]]; then echo 29.0.0; fi
if [[ "$1" == build || "$1" == pull ]]; then exit "${IMAGE_EXIT:-0}"; fi
''',
                "curl": '''#!/usr/bin/env bash
while [[ $# -gt 0 ]]; do
  if [[ "$1" == -o ]]; then shift; printf '{"tag_name":"v0.1.0"}' > "$1"; fi
  shift
done
printf '%s' "$RELEASE_STATUS"
''',
                "ss": "#!/usr/bin/env bash\nexit 0\n",
            }
            for name, content in mocks.items():
                p = bin_dir / name
                p.write_text(content)
                p.chmod(0o755)
            env = {k: v for k, v in os.environ.items() if not k.startswith(("LOXY_", "HAPROXY_"))}
            env.update(PATH=f"{bin_dir}:{env['PATH']}", CALL_LOG=str(base / "calls"),
                       RELEASE_STATUS=status, LOXY_DIR=str(base / "install"))
            env.update(overrides)
            result = subprocess.run(["bash", str(installer)], env=env, text=True, capture_output=True)
            calls = (base / "calls").read_text()
            return result, calls

    def test_no_release_builds_source(self):
        result, calls = self.run_preflight()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("building from source", result.stderr)
        self.assertIn("source-haproxy3.4.6", calls)
        self.assertIn("build -f", calls)
        self.assertNotIn("pull ghcr.io", calls)

    def test_release_pulls_published_image(self):
        result, calls = self.run_preflight(status="200")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("pull ghcr.io/2shiraz/loxy:v0.1.0-haproxy3.4.6", calls)
        self.assertNotIn("build -f", calls)

    def test_rate_limit_does_not_trigger_source_build(self):
        result, calls = self.run_preflight(status="403")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("HTTP 403", result.stderr)
        self.assertNotIn("build -f", calls)

    def test_pinned_version_bypasses_lookup(self):
        result, calls = self.run_preflight(status="403", LOXY_VERSION="v0.2.0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("v0.2.0-haproxy3.4.6", calls)

    def test_explicit_source_bypasses_lookup(self):
        result, calls = self.run_preflight(status="403", LOXY_BUILD_FROM_SOURCE="1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("build -f", calls)

    def test_missing_image_fails_before_host_changes(self):
        result, calls = self.run_preflight(status="200", IMAGE_EXIT="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release image unavailable", result.stderr)
        self.assertNotIn("pull haproxy:", calls)


if __name__ == "__main__":
    unittest.main()
