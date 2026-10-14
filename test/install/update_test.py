"""Exercise the updater without a Docker daemon, network, or root writes."""
import io
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40


class Updater(unittest.TestCase):
    def run_update(self, migrated=True, **overrides):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td)
            for d in ("bin", "data/loxy", "data/haproxy-conf", "data/haproxy-state"):
                (p / d).mkdir(parents=True)
            with sqlite3.connect(p / "data/loxy/loxy.db") as db:
                db.execute("CREATE TABLE schema_migrations(name TEXT)")
                if migrated:
                    db.execute("INSERT INTO schema_migrations VALUES ('0002_projects.sql')")
            original = "LOXY_VERSION=source\nHAPROXY_VERSION=3.4.5\nLOXY_SECRET_KEY=keep-me\nLOXY_PANEL_PORT=9443\n"
            (p / ".env").write_text(original)
            (p / "compose.yml").write_text("original compose\n")
            (p / "data/loxy/secret.key").write_text("fallback-key")
            with tarfile.open(p / "source.tar.gz", "w:gz") as tf:
                content = b"FROM scratch\n"
                info = tarfile.TarInfo("source/deploy/docker/Dockerfile")
                info.size = len(content)
                tf.addfile(info, io.BytesIO(content))
            script = (ROOT / "deploy/update.sh").read_text().replace(
                '[[ $EUID -eq 0 ]] || die "run as root (sudo)"', ":")
            (p / "update.sh").write_text(script)
            mocks = {
                "curl": '''#!/usr/bin/env python3
import json, os, pathlib, shutil, sys
dest = sys.argv[sys.argv.index('-o') + 1]
if '/commits/' in sys.argv[2]:
    pathlib.Path(dest).write_text(json.dumps({'sha': 'a' * 40}))
else:
    shutil.copyfile(os.environ['SOURCE_ARCHIVE'], dest)
''',
                "docker": '''#!/usr/bin/env python3
import os, pathlib, sys
a = sys.argv[1:]
with open(os.environ['CALL_LOG'], 'a') as f: f.write(' '.join(a) + '\\n')
if a[0] == 'build': sys.exit(int(os.environ.get('BUILD_EXIT', '0')))
if a[:3] == ['compose', 'ps', '-q']: print('old-container')
if a[0] == 'inspect': print('sha256:old-image')
if a[:2] == ['compose', 'exec'] and a[-1] == 'backup':
    if os.environ.get('BACKUP_EXIT'): sys.exit(1)
    print('/var/lib/loxy/backups/test.db')
if a[0] == 'cp': pathlib.Path(a[-1]).write_text('online backup')
if a[:2] == ['compose', 'up']: sys.exit(int(os.environ.get('UP_EXIT', '0')))
''',
            }
            for name, content in mocks.items():
                tool = p / "bin" / name
                tool.write_text(content)
                tool.chmod(0o755)
            env = dict(os.environ, PATH=f"{p / 'bin'}:{os.environ['PATH']}",
                       LOXY_DIR=str(p), SOURCE_ARCHIVE=str(p / "source.tar.gz"), CALL_LOG=str(p / "calls"))
            env.update(overrides)
            result = subprocess.run(["bash", str(p / "update.sh")], env=env, capture_output=True, text=True)
            calls = (p / "calls").read_text()
            backups = list((p / "backups").glob("update-*")) if (p / "backups").exists() else []
            saved = {f.name: f.read_text() for f in backups[0].iterdir() if f.suffix != ".gz"} if backups else {}
            return result, calls, (p / ".env").read_text(), original, saved

    def test_success_preserves_settings_and_haproxy(self):
        r, calls, env, original, saved = self.run_update()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("HAPROXY_VERSION=3.4.5", calls)
        self.assertIn("--no-deps --pull never --wait --wait-timeout 120 loxy", calls)
        self.assertNotIn("compose down", calls)
        self.assertNotIn("pull haproxy", calls)
        self.assertIn("LOXY_SECRET_KEY=keep-me", env)
        self.assertIn("LOXY_PANEL_PORT=9443", env)
        self.assertIn("LOXY_VERSION=update-aaaaaaaaaaaa", env)
        self.assertEqual(saved[".env"], original)
        self.assertEqual(saved["secret.key"], "fallback-key")
        self.assertEqual(saved["loxy.db"], "online backup")

    def test_legacy_schema_refused(self):
        r, calls, env, original, _ = self.run_update(migrated=False)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("migrate it separately", r.stderr)
        self.assertNotIn("build ", calls)
        self.assertEqual(env, original)

    def test_build_failure_leaves_installation_alone(self):
        r, calls, env, original, saved = self.run_update(BUILD_EXIT="1")
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(env, original)
        self.assertNotIn("compose up", calls)
        self.assertFalse(saved)

    def test_backup_failure_does_not_replace_panel(self):
        r, calls, env, original, _ = self.run_update(BACKUP_EXIT="1")
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(env, original)
        self.assertNotIn("compose up", calls)

    def test_unhealthy_update_reports_backup_and_previous_image(self):
        r, _, _, _, saved = self.run_update(UP_EXIT="1")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("Update failed", r.stderr)
        self.assertIn("database may have migrated", r.stderr)
        self.assertIn("loxy-rollback:", saved["image.txt"])


if __name__ == "__main__":
    unittest.main()
