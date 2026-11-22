from contextlib import redirect_stdout
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('linux_verification', ROOT / 'scripts/verify-linux.py')
linux = importlib.util.module_from_spec(spec)
spec.loader.exec_module(linux)


class LinuxVerificationTests(unittest.TestCase):
    def test_failed_command_keeps_evidence_and_stops_later_builds(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'verification'
            commands = linux.steps(output, sys.executable)
            harmless = [sys.executable, '-c', 'print("check executed")']
            commands = [(name, [harmless]) for name, _ in commands]
            commands[1] = ('source-inputs', [[sys.executable, '-c', 'import sys; print("fixture failure"); sys.exit(7)']])
            with redirect_stdout(io.StringIO()), self.assertRaises(subprocess.CalledProcessError):
                linux.run(output, commands)
            report = json.loads((output / 'verification.json').read_text())
            self.assertEqual(report['status'], 'FAIL')
            self.assertEqual([c['status'] for c in report['checks']], ['PASS', 'FAIL'])
            self.assertFalse((output / 'product-build.txt').exists())
            self.assertIn('fixture failure', (output / 'source-inputs.txt').read_text())
            for check in report['checks']:
                self.assertEqual(check['sha256'], linux.sha256(output / check['log']))

    def test_postcheck_validation_failure_cannot_leave_pass_report(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'verification'
            commands = [(name, [[sys.executable, '-c', 'print("fixture check")']])
                        for name, _ in linux.steps(output, sys.executable)]
            with redirect_stdout(io.StringIO()), patch.object(linux, 'validate_report', side_effect=ValueError('changed artifact')):
                with self.assertRaisesRegex(ValueError, 'changed artifact'):
                    linux.run(output, commands)
            report = json.loads((output / 'verification.json').read_text())
            self.assertEqual(report['status'], 'FAIL')
            self.assertEqual(report['real_network_acceptance'], 'NOT-RUN')


if __name__ == '__main__':
    unittest.main()
