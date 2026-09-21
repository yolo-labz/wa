#!/usr/bin/env python3
"""Lifecycle regression with fake build tools; real media is checked separately."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

SCRIPT = Path(__file__).resolve().with_name("record-demo.sh")


class RecordingOwnership(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="wa-recorder-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.tools = self.root / "tools"
        self.tools.mkdir()
        # Block at the first build, then fail deliberately, exercising EXIT cleanup.
        for name, body in {
            "git": "echo test-build",
            "vhs": '[ "${1:-}" != validate ] || exit 0; exit 99',
            "ffmpeg": "exit 99",
            "ffprobe": "exit 99",
            "go": 'touch "$TEST_READY"; while [ ! -f "$TEST_RELEASE" ]; do sleep 0.02; done; exit 42',
        }.items():
            tool = self.tools / name
            tool.write_text("#!/usr/bin/env bash\n" + body + "\n")
            tool.chmod(0o755)
        self.processes = []
        self.addCleanup(self.stop_processes)

    def stop_processes(self):
        for process, release in self.processes:
            release.touch()
            process.communicate(timeout=10)

    def repo(self, name):
        repo = self.root / name
        (repo / "scripts").mkdir(parents=True)
        (repo / "docs/assets").mkdir(parents=True)
        shutil.copy2(SCRIPT, repo / "scripts/record-demo.sh")
        return repo

    def start(self, repo, name):
        ready, release = self.root / (name + "-ready"), self.root / (name + "-release")
        env = dict(os.environ, PATH=str(self.tools) + ":" + os.environ["PATH"],
                   TEST_READY=str(ready), TEST_RELEASE=str(release))
        process = subprocess.Popen(["bash", str(repo / "scripts/record-demo.sh")],
                                   env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   text=True)
        self.processes.append((process, release))
        return process, ready, release

    def wait_ready(self, ready):
        deadline = time.monotonic() + 10
        while not ready.exists() and time.monotonic() < deadline:
            time.sleep(0.02)
        self.assertTrue(ready.exists(), "recorder never reached build")

    def finish(self, process, release, expected_code=42):
        release.touch()
        output, _ = process.communicate(timeout=10)
        self.assertEqual(process.returncode, expected_code, output)
        sandbox = Path(output.split(" in ", 1)[1].splitlines()[0])
        self.assertFalse(sandbox.exists())
        return sandbox

    def test_existing_unknown_lock_is_untouched(self):
        repo = self.repo("occupied")
        lock = repo / "docs/assets/.wa-demo.lock"
        lock.mkdir()
        (lock / "unknown-owner").write_text("do not delete")
        process, ready, _ = self.start(repo, "blocked")
        output, _ = process.communicate(timeout=5)
        self.assertNotEqual(process.returncode, 0, output)
        self.assertIn("Recording lock exists", output)
        self.assertFalse(ready.exists())
        self.assertEqual((lock / "unknown-owner").read_text(), "do not delete")

    def test_same_checkout_excludes_second_recorder(self):
        repo = self.repo("shared")
        first, ready, release = self.start(repo, "first")
        self.wait_ready(ready)
        second, _, _ = self.start(repo, "second")
        output, _ = second.communicate(timeout=5)
        self.assertNotEqual(second.returncode, 0, output)
        self.assertTrue((repo / "docs/assets/.wa-demo.lock").is_dir())
        self.assertIsNone(first.poll())
        self.finish(first, release)
        self.assertFalse((repo / "docs/assets/.wa-demo.lock").exists())

    def test_term_cleans_owned_resources(self):
        repo = self.repo("signalled")
        process, ready, release = self.start(repo, "term")
        self.wait_ready(ready)
        process.terminate()
        # Bash handles the signal when its foreground build returns.
        self.finish(process, release, expected_code=143)
        self.assertFalse((repo / "docs/assets/.wa-demo.lock").exists())

    def test_separate_checkouts_have_distinct_owned_sandboxes(self):
        runs = [self.start(self.repo(name), name) for name in ("one", "two")]
        for _, ready, _ in runs:
            self.wait_ready(ready)
        roots = [self.finish(process, release) for process, _, release in runs]
        self.assertNotEqual(*roots)


if __name__ == "__main__":
    unittest.main(verbosity=2)
