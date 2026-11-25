import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('linux_preflight', ROOT / 'scripts/linux_preflight.py')
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)


class DebianPreflightTests(unittest.TestCase):
    def test_debian13_and_pinned_toolchains(self):
        release = preflight.parse_os_release('ID=debian\nVERSION_ID="13"\nPRETTY_NAME="Debian GNU/Linux 13 (trixie)"\n')
        run = lambda args: {'go': 'go version go1.27.4 linux/amd64', 'node': 'v26.13.1', 'pkg-config': '3.5.9'}[args[0]]
        result = preflight.inspect_environment('build', release, 'Linux', 'x86_64', lambda _: '/fixture/bin', run)
        self.assertEqual(result['status'], 'PASS')
        self.assertFalse(result['system_changed'])
        self.assertEqual(result['real_network_acceptance'], 'NOT-RUN')

    def test_old_distribution_toolchains_and_missing_commands_fail(self):
        release = {'ID': 'debian', 'VERSION_ID': '11'}
        result = preflight.inspect_environment('build', release, 'Linux', 'armv7l', lambda _: None, lambda _: 'old-version')
        self.assertEqual(result['status'], 'FAIL')
        self.assertTrue(all(item['status'] == 'FAIL' for item in result['checks']))

    def test_debian12_install_and_unsupported_derivative(self):
        release = {'ID': 'debian', 'VERSION_ID': '12'}
        result = preflight.inspect_environment('install', release, 'Linux', 'x86_64', lambda _: '/usr/bin/fixture')
        self.assertEqual(result['status'], 'PASS')
        release['ID'] = 'ubuntu'
        self.assertEqual(preflight.inspect_environment('install', release, 'Linux', 'x86_64', lambda _: '/bin/tool')['status'], 'FAIL')

    def test_install_does_not_require_compilers_or_execute_os_release(self):
        release = preflight.parse_os_release('ID=debian\nVERSION_ID=13\nPRETTY_NAME="$(touch /must-not-execute)"\n')
        def forbidden(_):
            self.fail('install preflight invoked a compiler')
        result = preflight.inspect_environment('install', release, 'Linux', 'x86_64', lambda _: '/fixture/bin', forbidden)
        self.assertEqual(result['status'], 'PASS')
        self.assertIn('$(touch', result['checks'][0]['detail'])
        with self.assertRaises(ValueError):
            preflight.parse_os_release('ID=debian\nID=ubuntu\n')


if __name__ == '__main__':
    unittest.main()
