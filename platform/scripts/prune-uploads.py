#!/usr/bin/env python3
"""Run with app stopped; remove unreferenced uploads older than backup retention."""
import os
import pathlib
import subprocess
import time
root = pathlib.Path(os.environ['UPLOAD_DIR'])
result = subprocess.run(['psql', os.environ['DATABASE_URL'], '-XAt', '-v', 'ON_ERROR_STOP=1', '-c', "SELECT path FROM uploads UNION SELECT thumbnail FROM uploads WHERE thumbnail IS NOT NULL"], check=True, capture_output=True, text=True)
keep = set(result.stdout.splitlines())
for file in root.iterdir():
    if file.is_file() and file.name not in keep and file.stat().st_mtime < time.time() - 15*86400:
        file.unlink()
