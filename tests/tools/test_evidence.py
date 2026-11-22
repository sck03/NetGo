import copy
import hashlib
import json
import math
from pathlib import Path
import re
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tests/network"))
import evidence


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = {"schema": 1, "id": "tool-selftest", "kind": "local-automation", "ready": True,
                    "timezone": "Asia/Shanghai", "operator": "unit-test", "description": "Synthetic collector unit test; no real network acceptance."}
        self.env_file = self.root / "environment.json"
        self.save_env()

    def save_env(self):
        self.env_file.write_bytes(evidence.encode(self.env))

    def collect(self, code="print('fixture')", name="run", **options):
        directory = self.root / name
        result = evidence.collect("A01", self.env_file, directory, [sys.executable, "-u", "-c", code], **options)
        return directory, result

    def real_fixture(self):
        # Synthetic metadata exercises manual-review validation only. These
        # temporary files are never delivered as actual network evidence.
        self.env.update(kind="real", hosts={"L": {"config_sha256": "a" * 64}, "C1": {}, "D": {}},
                        network={"recovery_procedure": "synthetic test fixture only"})
        self.save_env()

    def test_binary_output_hashes_exact_saved_bytes(self):
        directory, result = self.collect("import os; os.write(1,b'\\xff\\r\\nout'); os.write(2,b'err\\n')")
        raw = (directory / "verification.log").read_bytes()
        self.assertEqual(raw, b"\xff\r\nout\n--- STDERR ---\nerr\n")
        self.assertEqual(result["verification_sha256"], hashlib.sha256(raw).hexdigest())
        self.assertEqual(evidence.verify(directory)["status"], "REVIEW-REQUIRED")
        self.assertEqual(evidence.verdict(directory, result), "AUTOMATION-ONLY")

    def test_timeout_preserves_partial_output(self):
        directory, result = self.collect("import time; print('before timeout',flush=True); time.sleep(20)", timeout=.4)
        self.assertEqual(result["status"], "TIMEOUT")
        self.assertEqual(result["exit_code"], 124)
        self.assertIn(b"before timeout", (directory / "stdout.bin").read_bytes())
        evidence.verify(directory)

    def test_failed_spawn_and_nonzero_exit_are_preserved(self):
        failed = self.root / "spawn-failed"
        result = evidence.collect("A02", self.env_file, failed, [str(self.root / "missing-program")])
        self.assertEqual(result["status"], "EXEC-ERROR")
        evidence.verify(failed)
        directory, result = self.collect("import sys; print('failed'); sys.exit(7)")
        self.assertEqual((result["status"], result["exit_code"]), ("COMMAND-FAILED", 7))
        evidence.verify(directory)

    def test_output_limit_is_not_a_passing_command(self):
        directory, result = self.collect("import os; os.write(1,b'x'*100000)", max_log_bytes=1024)
        self.assertEqual(result["status"], "OUTPUT-LIMIT")
        self.assertEqual((directory / "stdout.bin").stat().st_size, 1024)
        evidence.verify(directory)

    def test_tamper_and_escaping_paths_fail_integrity(self):
        directory, result = self.collect()
        (directory / "stdout.bin").write_bytes(b"changed")
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            evidence.verify(directory)
        result["files"]["../environment.json"] = evidence.digest(self.env_file)
        (directory / "result.json").write_bytes(evidence.encode(result))
        with self.assertRaises(ValueError):
            evidence.verify(directory)
        with self.assertRaises(ValueError):
            evidence.safe_file(directory, "../environment.json")

    def test_not_prepared_and_automation_cannot_be_reviewed_pass(self):
        directory, _ = self.collect()
        with self.assertRaisesRegex(ValueError, "real environment"):
            evidence.review(directory, "PASS", "reviewer", "Synthetic tests must not count as a network pass.", evidence.CHECKS)
        self.env.update(kind="unprepared", ready=False)
        self.save_env()
        with self.assertRaises(ValueError):
            self.collect(name="not-prepared")
        self.assertFalse((self.root / "not-prepared").exists())

    def test_unrecorded_files_and_inconsistent_execution_metadata_rejected(self):
        directory, result = self.collect()
        extra = directory / "unreviewed.log"
        extra.write_text("extra")
        with self.assertRaisesRegex(ValueError, "unrecorded"):
            evidence.verify(directory)
        extra.unlink()
        result["exit_code"] = 1
        (directory / "result.json").write_bytes(evidence.encode(result))
        with self.assertRaisesRegex(ValueError, "disagree"):
            evidence.verify(directory)

    def test_manual_review_is_bound_to_unchanged_real_record(self):
        self.real_fixture()
        directory, result = self.collect()
        with self.assertRaises(ValueError):
            evidence.review(directory, "PASS", "reviewer", "Synthetic validation of review rules.", [])
        evidence.review(directory, "PASS", "reviewer", "Synthetic validation of review rules; never a real acceptance record.", evidence.CHECKS)
        self.assertEqual(evidence.verdict(directory, result), "PASS")
        result["duration_seconds"] += 1
        (directory / "result.json").write_bytes(evidence.encode(result))
        with self.assertRaisesRegex(ValueError, "does not match"):
            evidence.verdict(directory, evidence.verify(directory))

    def test_older_pass_cannot_hide_new_failed_attempt(self):
        self.real_fixture()
        directory, _ = self.collect(name="A01/runs/first")
        evidence.review(directory, "PASS", "reviewer", "Synthetic validation of report aggregation only.", evidence.CHECKS)
        self.collect(code="import sys; sys.exit(1)", name="A01/runs/second")
        summary = evidence.report(self.root, self.root / "report")
        self.assertEqual(summary["cases"]["A01"]["status"], "EXECUTION-FAILED")
        self.assertEqual(summary["release_gate"], "CLOSED")

    def test_initial_suite_has_all_unrun_cases_and_requirement_links(self):
        suite = self.root / "suite"
        evidence.initialize(suite)
        summary = evidence.report(suite, suite / "report")
        self.assertEqual(summary["counts"], {"NOT-RUN": 33})
        self.assertEqual(summary["release_gate"], "CLOSED")
        for entry in summary["requirements"].values():
            self.assertTrue(entry["cases"])
            self.assertEqual(entry["status"], "NOT-PASSED")
        document = (ROOT / "docs/NETWORK_ACCEPTANCE.zh-CN.md").read_text(encoding="utf-8")
        rows = {match[0]: set(re.findall(r"R\d\d", match[1])) for match in re.findall(r"^\| (A\d\d) [^\n]+\| ([^|\n]+) \|$", document, re.M)}
        self.assertEqual(set(rows), set(evidence.CASES))
        for case, entry in evidence.CASES.items():
            self.assertEqual(set(entry["requirements"]), rows[case])

    def test_case_integrity_error_closes_report(self):
        directory, _ = self.collect(name="A01/runs/bad")
        (directory / "stderr.bin").write_bytes(b"tamper")
        summary = evidence.report(self.root, self.root / "summary")
        self.assertEqual(summary["cases"]["A01"]["status"], "INTEGRITY-ERROR")
        self.assertEqual(len(summary["integrity_errors"]), 1)

    def test_metrics_keep_paths_and_failures_separate(self):
        rows = [{"path": "direct", "fault": "wan", "success": True, "business_seconds": i} for i in range(1, 21)]
        rows.append({"path": "local_relay", "fault": "wan", "success": False, "business_seconds": 120})
        result = evidence.metrics(rows)
        self.assertEqual(result[0]["p50_seconds"], 10)
        self.assertEqual(result[0]["p95_seconds"], 19)
        self.assertTrue(result[0]["sample_target_met"])
        self.assertEqual(result[1]["failure_rate"], 1)
        self.assertIsNone(result[1]["p95_seconds"])
        for invalid in (-1, math.inf, math.nan):
            with self.assertRaises(ValueError):
                evidence.metrics([{"path": "direct", "success": True, "business_seconds": invalid}])


if __name__ == "__main__":
    unittest.main()
