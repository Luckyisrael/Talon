#!/usr/bin/env python3
"""Prefix every stdin line with HH:MM:SS.mmm — the SSE capture wrapper that
makes e2e/scorecard.py score time instead of only counts.

  curl -N ... | python3 e2e/timestamp-stream.py > /tmp/run-stream.log
"""
import sys
import time

for line in sys.stdin:
    sys.stdout.write(time.strftime("%H:%M:%S.") + f"{int(time.time() * 1000) % 1000:03d} " + line)
    sys.stdout.flush()
