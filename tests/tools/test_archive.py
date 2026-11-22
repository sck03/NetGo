import hashlib
from pathlib import Path
import sys
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from verification import verify_archive


class ArchiveTests(unittest.TestCase):
    def archive(self, change=None):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        path = Path(temp.name) / 'delivery.zip'
        content = {'bin/program': b'verified image', 'README.md': b'development bundle'}
        sums = lambda names: ''.join(hashlib.sha256(content[name]).hexdigest() + '  ' + name + '\n' for name in names).encode()
        content['bin/SHA256SUMS'] = hashlib.sha256(content['bin/program']).hexdigest().encode() + b'  program\n'
        content['SHA256SUMS'] = sums(['bin/program', 'README.md', 'bin/SHA256SUMS'])
        if change:
            change(content)
        with zipfile.ZipFile(path, 'w') as archive:
            for name, data in content.items():
                archive.writestr(name, data)
        return path

    def test_nested_manifests_and_complete_root_manifest_pass(self):
        result = verify_archive(self.archive())
        self.assertEqual(result['files'], 4)
        self.assertEqual(result['checksums'], 'PASS')

    def test_corrupt_nested_manifest_is_detected(self):
        def corrupt(files):
            files['bin/SHA256SUMS'] = b'0' * 64 + b'  program\n'
            files['SHA256SUMS'] = ''.join(hashlib.sha256(data).hexdigest() + '  ' + name + '\n'
                                         for name, data in files.items() if name != 'SHA256SUMS').encode()
        path = self.archive(corrupt)
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            verify_archive(path)

    def test_unlisted_file_is_rejected(self):
        path = self.archive(lambda files: files.update({'undeclared.txt': b'not recorded'}))
        with self.assertRaisesRegex(ValueError, 'absent from its checksum'):
            verify_archive(path)

    def test_path_escape_is_rejected(self):
        path = self.archive(lambda files: files.update({'../outside.txt': b'outside'}))
        with self.assertRaisesRegex(ValueError, 'unsafe archive path'):
            verify_archive(path)

    def test_self_consistent_archive_with_wrong_build_is_rejected(self):
        path = self.archive()
        with self.assertRaisesRegex(ValueError, 'differs from verified input'):
            verify_archive(path, {'bin/program': hashlib.sha256(b'different tested build').hexdigest()})


if __name__ == '__main__':
    unittest.main()
