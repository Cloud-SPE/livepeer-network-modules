#!/usr/bin/env python3
"""Validate arbitrary documents against one schema in this module.

`validate-examples.py` guards the module's own curated examples. This
guards documents produced ELSEWHERE — an implementation's golden files —
against the same schema, so an implementer can prove conformance in
their own test suite instead of discovering it at a broker.

    python3 scripts/validate-doc.py --schema protocols/runner-attach/schema.json doc.json [...]
"""
import argparse
import json
import pathlib
import sys

try:
    import jsonschema
except ImportError:
    sys.exit("jsonschema is not installed: pip install jsonschema")

root = pathlib.Path(__file__).resolve().parent.parent

parser = argparse.ArgumentParser()
parser.add_argument("--schema", required=True, help="schema path, relative to the module root or absolute")
parser.add_argument("docs", nargs="+", help="documents to validate")
args = parser.parse_args()

schema_path = pathlib.Path(args.schema)
if not schema_path.is_absolute():
    schema_path = root / schema_path
schema = json.loads(schema_path.read_text())
validator = jsonschema.Draft202012Validator(schema)

failed = False
for raw in args.docs:
    path = pathlib.Path(raw)
    errors = sorted(validator.iter_errors(json.loads(path.read_text())),
                    key=lambda e: list(e.absolute_path))
    if errors:
        failed = True
        err = errors[0]
        print(f"FAIL  {path}\n      {err.message}\n      at {list(err.absolute_path)}")
    else:
        print(f"ok    {path}")

sys.exit(1 if failed else 0)
