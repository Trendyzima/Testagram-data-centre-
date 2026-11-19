#!/usr/bin/env python3
"""Static safety contract for Data Centre service database backup/restore scripts."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class BackupScopeContract(unittest.TestCase):
    def test_backup_records_partial_scope_and_component_checksums(self):
        source = (ROOT / "vps/backup.sh").read_text()
        for required in (
            'sha256sum ./*.dump > components.sha256',
            'dump_database media "$MEDIA_DATABASE_URL"',
            'dump_database federation "$FEDERATION_DATABASE_URL"',
            'backup_scope=data_centre_and_service_metadata',
            'media_database_included=',
            'federation_database_included=',
            'neon_database_required=false',
            'media_bytes_included=false',
            'external_identity_documents_included=false',
        ):
            self.assertIn(required, source)

    def test_restore_verifies_scope_and_each_database_dump(self):
        source = (ROOT / "vps/restore-drill.sh").read_text()
        for required in (
            'sha256sum -c components.sha256',
            "backup_scope=data_centre_and_service_metadata",
            "restore_one media testagram_media_restore",
            "restore_one federation testagram_federation_restore",
            "media_database_included=true",
            "federation_database_included=true",
            "neon_database_required=false",
            "media_bytes_included=false",
            "external_identity_documents_included=false",
            "DATA CENTRE INFRASTRUCTURE RESTORE DRILL PASSED",
        ):
            self.assertIn(required, source)
        self.assertNotIn("neon-social.dump", source)
        self.assertNotIn("NEON_DATABASE_URL", source)
        self.assertNotIn('echo "RESTORE DRILL PASSED"', source)


if __name__ == "__main__":
    unittest.main()
