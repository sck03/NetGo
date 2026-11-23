import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from fingerprint import fingerprint
import verification as verify


class VerificationTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        for name in ('go.mod', 'go.sum', 'package.json', 'package-lock.json', 'tsconfig.json', 'vite.config.ts', 'install-server.sh', 'install-client.ps1'):
            (self.root / name).write_text('fixed input', encoding='utf-8')
        (self.root / 'internal').mkdir()
        (self.root / 'internal/source.go').write_text('package example\n', encoding='utf-8')
        self.report_path = self.root / 'evidence/verification.json'
        self.report_path.parent.mkdir()
        for name in verify.artifact_paths('windows'):
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b'tested build')
        self.report = {'schema': 2, 'platform': 'windows', 'status': 'PASS',
                       'code_sha256': fingerprint(self.root)['code_sha256'], 'checks': []}
        for name in sorted(verify.CHECKS['windows']):
            log = self.report_path.parent / (name + '.txt')
            value = 'PASS\n'
            if name == 'source-inputs':
                value = json.dumps(fingerprint(self.root))
            if name == 'artifact-inputs':
                value = json.dumps(verify.artifact_inputs(self.root, 'windows'))
            log.write_text(value, encoding='utf-8')
            self.report['checks'].append({'name': name, 'status': 'PASS', 'log': log.name, 'sha256': verify.sha256(log)})
        self.save()

    def save(self):
        self.report_path.write_text(json.dumps(self.report), encoding='utf-8')

    def validate(self):
        return verify.validate_report(self.root, self.report_path, 'windows')

    def test_matching_source_artifacts_and_complete_logs(self):
        result = self.validate()
        self.assertEqual(result['code_sha256'], self.report['code_sha256'])
        self.assertEqual(result['verification_sha256'], verify.sha256(self.report_path))
        self.assertEqual(result['real_network_acceptance'], 'NOT-RUN')

    def test_source_change_requires_new_verification(self):
        (self.root / 'internal/source.go').write_text('package changed\n', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'source inputs changed'):
            self.validate()

    def test_rebuilt_binary_and_rewritten_sums_do_not_reuse_old_checks(self):
        binary = self.root / 'dist/linux-amd64/netgo-server'
        binary.write_bytes(b'unverified rebuild')
        (binary.parent / 'SHA256SUMS').write_text(verify.sha256(binary) + '  netgo-server\n', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'artifacts changed'):
            self.validate()

    def test_failed_skipped_and_missing_checks_are_rejected(self):
        original = copy.deepcopy(self.report)
        for change in ('report-status', 'skip', 'missing', 'duplicate', 'wrong-platform', 'old-schema'):
            with self.subTest(change=change):
                self.report = copy.deepcopy(original)
                if change == 'report-status':
                    self.report['status'] = 'INCOMPLETE'
                elif change == 'skip':
                    self.report['checks'][0]['status'] = 'NOT-RUN'
                elif change == 'missing':
                    self.report['checks'].pop()
                elif change == 'duplicate':
                    self.report['checks'][-1] = self.report['checks'][0]
                elif change == 'wrong-platform':
                    self.report['platform'] = 'linux'
                else:
                    self.report['schema'] = 1
                self.save()
                with self.assertRaises(ValueError):
                    self.validate()

    def test_changed_log_is_rejected(self):
        (self.report_path.parent / 'go-race.txt').write_text('different result\n', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'log checksum mismatch'):
            self.validate()

    def test_log_path_escape_is_rejected(self):
        self.report['checks'][0]['log'] = '../outside.txt'
        self.save()
        with self.assertRaisesRegex(ValueError, 'invalid verification log'):
            self.validate()

    def test_duplicate_json_key_is_rejected(self):
        self.report_path.write_text('{"status":"FAIL","status":"PASS"}', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'duplicate JSON key'):
            self.validate()


if __name__ == '__main__':
    unittest.main()
