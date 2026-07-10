#!/usr/bin/env python3
"""Normalize the vendored openapi.yaml so oapi-codegen can consume it.

The upstream spec is produced by thrift-gen-http-swagger with `-enable_extends`.
Extended Thrift methods inherit their parent's path parameters, so a short path
such as `/identity/login/bind/{state}` ends up declaring `{GrantType}` and
`{GrantKey}` path parameters that never appear in the URL template. oapi-codegen
rejects that mismatch.

This script drops any `in: path` parameter whose name is not present as a
`{placeholder}` in the path key. It is a pure structural normalization: it never
adds, renames, or removes routes, so the generated surface still matches the API.

Usage:  normalize_openapi.py <input.yaml> <output.yaml>
"""

import re
import sys

import yaml


def normalize(spec: dict) -> dict:
    paths = spec.get("paths", {})
    for path, item in paths.items():
        if not isinstance(item, dict):
            continue
        present = set(re.findall(r"{([^}]+)}", path))
        for method, op in item.items():
            if not isinstance(op, dict):
                continue
            params = op.get("parameters")
            if not isinstance(params, list):
                continue
            op["parameters"] = [
                p
                for p in params
                if not (
                    isinstance(p, dict)
                    and p.get("in") == "path"
                    and p.get("name") not in present
                )
            ]
    return spec


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit("usage: normalize_openapi.py <input.yaml> <output.yaml>")
    with open(sys.argv[1], "r", encoding="utf-8") as fh:
        spec = yaml.safe_load(fh)
    spec = normalize(spec)
    with open(sys.argv[2], "w", encoding="utf-8") as fh:
        yaml.safe_dump(spec, fh, sort_keys=False, allow_unicode=True, width=4096)


if __name__ == "__main__":
    main()
