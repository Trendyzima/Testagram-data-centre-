#!/usr/bin/env python3
"""Enforce the Data Centre boundary around Backend-owned native mail."""
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
SELF = "scripts/guard-native-mail-boundary.py"
POLICY = "docs/architecture/native-mail-ownership.md"
PROVIDER = re.compile(r"mail[\s._-]*jet|api[.]mailjet[.]com", re.IGNORECASE)
RETIRED = re.compile(
    r"\b(?:CLOUDFLARE_EMAIL_ACCOUNT_ID|CLOUDFLARE_EMAIL_API_TOKEN|AUTH_FROM_EMAIL|"
    r"SMTP_HOST|SMTP_USERNAME|SMTP_PASSWORD|MAILJET_API_KEY|MAILJET_SECRET_KEY)\b"
)

def main():
    tracked = subprocess.run(
        ["git", "ls-files", "-z"], cwd=ROOT, check=True, capture_output=True
    ).stdout.split(b"\0")
    violations = []
    for item in tracked:
        if not item:
            continue
        relative = item.decode()
        if relative in {SELF, POLICY}:
            continue
        path = ROOT / relative
        try:
            content = path.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue
        if PROVIDER.search(content) or RETIRED.search(content):
            violations.append(relative)
    if violations:
        print("Data Centre contains a forbidden outbound mail provider/relay reference:", file=sys.stderr)
        for path in violations:
            print(f" - {path}", file=sys.stderr)
        return 1
    print("Data Centre native-mail ownership boundary passed.")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
