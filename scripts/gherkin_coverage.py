#!/usr/bin/env python3
"""Gate de trazabilidad: cada escenario [E<n>.<m>-S<k>] de docs/requirements.json
debe tener al menos un test Go llamado Test<E n>_<m>_S<k>_...

Uso: python3 scripts/gherkin_coverage.py [epica ...]
Sin argumentos revisa todas las épicas. Sale con 1 si falta algún test.
"""
import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parent.parent
req = json.loads((root / "docs" / "requirements.json").read_text())
wanted = set(sys.argv[1:])

tags = []
for epic in req["epics"]:
    if wanted and epic["id"] not in wanted:
        continue
    for story in epic["user_stories"]:
        for crit in story["acceptance_criteria"]:
            m = re.search(r"\[E(\d+)\.(\d+)-S(\d+)\]\s*$", crit)
            if not m:
                print(f"criterio sin tag en {story['id']}: {crit[:60]}")
                sys.exit(2)
            tags.append(m.groups())

tests = set()
for f in root.rglob("*_test.go"):
    for name in re.findall(r"^func (Test\w+)\(", f.read_text(), re.M):
        tests.add(name)

missing = []
for e, s, k in tags:
    prefix = f"TestE{e}_{s}_S{k}_"
    if not any(t.startswith(prefix) for t in tests):
        missing.append(f"E{e}.{s}-S{k} (esperado {prefix}*)")

print(f"escenarios: {len(tags)}, con test: {len(tags) - len(missing)}, sin test: {len(missing)}")
for m in missing:
    print("  falta", m)
sys.exit(1 if missing else 0)
