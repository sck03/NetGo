import importlib.util
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('prepare_upstream', ROOT / 'scripts/prepare-upstream.py')
upstream = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upstream)


class UpstreamInputTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.checkout = self.root / 'source'
        self.checkout.mkdir()
        self.git('init')
        self.git('config', 'user.name', 'NetGo test fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        (self.checkout / 'go.mod').write_text('module fixture\n\ngo 1.27.0\n', encoding='utf-8')
        (self.checkout / 'main.go').write_text('package fixture\n\nconst Value = 1\n', encoding='utf-8')
        (self.checkout / '.gitignore').write_text('ignored.go\n', encoding='utf-8')
        self.git('add', '.')
        self.git('commit', '-m', 'fixture baseline')
        self.commit = self.git('rev-parse', 'HEAD').strip()
        (self.checkout / 'main.go').write_text('package fixture\n\nconst Value = 2\n', encoding='utf-8')
        self.patch = self.root / 'change.patch'
        self.patch.write_text(self.git('diff', '--no-ext-diff'), encoding='utf-8', newline='\n')

    def git(self, *args):
        return subprocess.run(['git', *args], cwd=self.checkout, check=True, capture_output=True, text=True).stdout

    def test_exact_patch_is_reusable_without_changing_the_real_index(self):
        before = self.git('status', '--porcelain')
        upstream.assert_inputs(self.checkout, self.commit, self.patch)
        upstream.assert_inputs(self.checkout, self.commit, self.patch)
        self.assertEqual(self.git('status', '--porcelain'), before)

    def test_unrelated_tracked_edits_are_rejected_and_preserved(self):
        (self.checkout / 'go.mod').write_text('module edited\n', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'differs from locked source'):
            upstream.assert_inputs(self.checkout, self.commit, self.patch)
        self.assertEqual((self.checkout / 'go.mod').read_text(), 'module edited\n')

    def test_new_ignored_source_is_not_allowed_into_the_build(self):
        file = self.checkout / 'ignored.go'
        file.write_text('package fixture\n', encoding='utf-8')
        self.assertNotIn('ignored.go', self.git('status', '--porcelain'))
        with self.assertRaisesRegex(ValueError, 'differs from locked source'):
            upstream.assert_inputs(self.checkout, self.commit, self.patch)
        self.assertTrue(file.exists())

    @unittest.skipUnless(shutil.which('go'), 'Go required for module replacement check')
    def test_only_the_declared_dex_module_replacement_is_accepted(self):
        subprocess.run(['go', 'mod', 'edit', '-replace', upstream.REPLACEMENT], cwd=self.checkout, check=True)
        upstream.assert_inputs(self.checkout, self.commit, self.patch, upstream.REPLACEMENT)
        with (self.checkout / 'go.mod').open('a', encoding='utf-8') as stream:
            stream.write('\nrequire unexpected.example/dependency v1.0.0\n')
        with self.assertRaisesRegex(ValueError, 'differs from locked source'):
            upstream.assert_inputs(self.checkout, self.commit, self.patch, upstream.REPLACEMENT)


if __name__ == '__main__':
    unittest.main()
