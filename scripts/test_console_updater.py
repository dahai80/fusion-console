import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile

import console_updater as update


class UpdaterTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        (self.root / "settings.json").write_text(json.dumps(dict(
            url="http://127.0.0.1:11480", bin=str(self.root / "bin"), job="gui/501/test", gh="gh")))
        (self.root / "drain.key").write_text("a" * 32)
        self.worker = update.Updater(self.root)
        self.sha = "1" * 40
        self.archive = io.BytesIO()
        with zipfile.ZipFile(self.archive, "w") as z:
            z.writestr("fusion-console-darwin-arm64", b"binary")
            z.writestr("sidecar/harnessd.py", b"sidecar")
            z.writestr("../unrequested", b"extra")
        self.data = self.archive.getvalue()
        self.artifact = dict(name="fusion-console-darwin-arm64", expired=False,
                             workflow_run=dict(head_sha=self.sha, id=7), id=23,
                             digest="sha256:" + hashlib.sha256(self.data).hexdigest())

    def stage(self):
        with patch.object(self.worker, "github", return_value={"artifacts": [self.artifact]}), \
             patch.object(update.platform, "machine", return_value="arm64"), \
             patch.object(update.subprocess, "run", side_effect=lambda args, **kw: kw["stdout"].write(self.data)):
            return self.worker.stage(self.sha, 7)

    def test_matching_digest_and_exact_files(self):
        stage = self.stage()
        self.assertEqual((stage / "fusion-console").read_bytes(), b"binary")
        self.assertEqual((stage / "sidecar/harnessd.py").read_bytes(), b"sidecar")
        self.assertFalse((self.root / "unrequested").exists())

    def test_invalid_digest_leaves_installed_files_alone(self):
        self.artifact["digest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(RuntimeError, "SHA-256"):
            self.stage()
        self.assertFalse((self.root / "artifact-stage").exists())

    def test_artifact_from_another_run_is_rejected(self):
        self.artifact["workflow_run"]["id"] = 90
        with self.assertRaises(RuntimeError):
            self.stage()

    def test_newer_local_head_never_downgrades(self):
        self.worker.pref["auto"] = True
        with patch.object(self.worker, "health", return_value={"revision": "2" * 40}), \
             patch.object(self.worker, "newest", return_value=(self.sha, "main", 7)), \
             patch.object(self.worker, "github", return_value={"status": "behind"}), \
             patch.object(self.worker, "stage") as stage:
            self.worker.tick()
        stage.assert_not_called()
        self.assertFalse(self.worker.snapshot()["available"])


if __name__ == "__main__":
    unittest.main()
