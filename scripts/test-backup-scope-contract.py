#!/usr/bin/env python3
"""Static safety contract for the explicitly database-only backup/restore scripts."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class BackupScopeContract(unittest.TestCase):
    def test_backup_records_partial_scope_and_component_checksums(self):
        source = (ROOT / "vps/backup.sh").read_text()
        for required in (
            'sha256sum data-centre.dump neon-social.dump > components.sha256',
            'backup_scope=database_only',
            'cross_database_atomic=false',
            'media_bytes_included=false',
            'appwrite_documents_included=false',
        ):
            self.assertIn(required, source)

    def test_restore_verifies_scope_and_each_database_dump(self):
        source = (ROOT / "vps/restore-drill.sh").read_text()
        for required in (
            'sha256sum -c components.sha256',
            "backup_scope=database_only",
            "cross_database_atomic=false",
            "media_bytes_included=false",
            "appwrite_documents_included=false",
            "DATABASE-ONLY RESTORE DRILL PASSED",
        ):
            self.assertIn(required, source)
        self.assertNotIn('echo "RESTORE DRILL PASSED"', source)


if __name__ == "__main__":
    unittest.main()
