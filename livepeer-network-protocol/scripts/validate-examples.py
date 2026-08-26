#!/usr/bin/env python3
"""Validate every schema's examples against its schema.

Guards the failure this repo already hit once: a schema change that left
its own examples invalid. Run via `make validate-examples`.

Covers:
  manifest/schema.json            <- manifest/examples/*.json (must pass)
  protocols/runner-attach/schema.json
                                  <- protocols/runner-attach/examples/*.json (must pass)
                                  <- protocols/runner-attach/examples/invalid/*.json (must FAIL)
  protocols/certification-steps/schema.json
                                  <- protocols/certification-steps/examples/{,invalid/}*.json

An invalid example that validates is as much a bug as a valid one that
does not: the schema is the contract, and the invalid set pins the
rejections the spec promises.
"""
import json
import pathlib
import sys

try:
    import jsonschema
except ImportError:
    sys.exit("jsonschema is not installed: pip install jsonschema")

root = pathlib.Path(__file__).resolve().parent.parent

SUITES = [
    (root / "manifest" / "schema.json", root / "manifest" / "examples"),
    (root / "protocols" / "runner-attach" / "schema.json",
     root / "protocols" / "runner-attach" / "examples"),
    (root / "protocols" / "certification-steps" / "schema.json",
     root / "protocols" / "certification-steps" / "examples"),
]

failed = False


def check(schema, path, expect_valid):
    global failed
    doc = json.loads(path.read_text())
    validator = jsonschema.Draft202012Validator(schema)
    errors = sorted(validator.iter_errors(doc), key=lambda e: list(e.absolute_path))
    rel = path.relative_to(root)
    if expect_valid and errors:
        failed = True
        err = errors[0]
        print(f"FAIL  {rel}\n      {err.message}\n      at {list(err.absolute_path)}")
    elif not expect_valid and not errors:
        failed = True
        print(f"FAIL  {rel}\n      expected the schema to reject this document; it validated")
    else:
        tag = "ok   " if expect_valid else "ok(!)"
        print(f"{tag} {rel}")


for schema_path, examples_dir in SUITES:
    schema = json.loads(schema_path.read_text())
    valid = sorted(examples_dir.glob("*.json"))
    invalid = sorted((examples_dir / "invalid").glob("*.json"))
    if not valid:
        sys.exit(f"no examples found under {examples_dir.relative_to(root)}/")
    for path in valid:
        check(schema, path, expect_valid=True)
    for path in invalid:
        check(schema, path, expect_valid=False)

sys.exit(1 if failed else 0)
