import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch, MagicMock

ROOT=Path(__file__).resolve().parents[2]

class LabCleanupTests(unittest.TestCase):
    def test_refused_namespace_start_cannot_delete_foreign_link_or_resolver(self):
        import sys
        sys.path.insert(0,str(ROOT/'tests/network'))
        try:
            import live_network
            with tempfile.TemporaryDirectory() as directory:
                client=live_network.Client('a',241,Path(directory),{})
                client.resolver=MagicMock()
                with patch.object(live_network,'command') as command, patch.object(live_network.subprocess,'run') as run:
                    client.close()
                    command.assert_not_called()
                    run.assert_not_called()
                    client.resolver.exists.assert_not_called()
        finally:
            sys.path.pop(0)

if __name__=='__main__':unittest.main()
