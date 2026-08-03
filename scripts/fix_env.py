#!/usr/bin/env python3
import secrets
from pathlib import Path

p = Path(".env")
existing = {}
if p.exists():
    for line in p.read_text(encoding="utf-8", errors="ignore").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        existing[k.strip()] = v.strip()

password = existing.get("NEWSMAKER_PASSWORD") or secrets.token_hex(8)
secret = existing.get("NEWSMAKER_SECRET") or secrets.token_hex(24)
user = existing.get("NEWSMAKER_USER") or "admin"

content = (
    "NEWSMAKER_ADDR=:8080\n"
    f"NEWSMAKER_USER={user}\n"
    f"NEWSMAKER_PASSWORD={password}\n"
    f"NEWSMAKER_SECRET={secret}\n"
    "DATA_DIR=/data\n"
    "WEB_DIR=/app/web\n"
    "FFMPEG_PATH=ffmpeg\n"
)
p.write_text(content, encoding="utf-8")
print("ok keys=7 password_len=%d secret_len=%d" % (len(password), len(secret)))
