#!/usr/bin/env python3
"""Validate every manifest example against manifest/schema.json.

Guards the failure this repo already hit once: a schema change that left
its own examples invalid. Run via `make validate-examples`.
"""
import json
import pathlib
import sys

try:
    import jsonschema
except ImportError:
    sys.exit("jsonschema is not installed: pip install jsonschema")

root = pathlib.Path(__file__).resolve().parent.parent
schema = json.loads((root / "manifest" / "schema.json").read_text())
examples = sorted((root / "manifest" / "examples").glob("*.json"))
if not examples:
    sys.exit("no examples found under manifest/examples/")

failed = False
for path in examples:
    try:
        jsonschema.validate(json.loads(path.read_text()), schema)
        print(f"ok    {path.relative_to(root)}")
    except jsonschema.ValidationError as err:
        failed = True
        print(f"FAIL  {path.relative_to(root)}\n      {err.message}\n      at {list(err.absolute_path)}")

sys.exit(1 if failed else 0)
