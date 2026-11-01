"""Shared by the golden capture scripts: the repo's paths, the frozen
Python modules loaded by path, and writing a golden file.

The scripts run once, by hand, from the repo folder, with the Python
named in tests/golden/README.md, and -B so no __pycache__ is left in
gui/ or game/:

    python3 -B tests/golden/pyfmt.py

Each writes JSON into the owning Go package's testdata/golden/. CI
only reads those files; it never runs these scripts.
"""
import importlib.util
import json
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.dirname(
    os.path.abspath(__file__))))


def load(name, rel):
    """Import a frozen Python file by path, as module name. Both
    programs have a __main__ guard, so nothing runs."""
    spec = importlib.util.spec_from_file_location(
        name, os.path.join(REPO, rel))
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


def write(package, name, data):
    """Write data as JSON to internal/<package>/testdata/golden/<name>,
    one value per line where that keeps lines short."""
    folder = os.path.join(REPO, "internal", package, "testdata",
                          "golden")
    os.makedirs(folder, exist_ok=True)
    path = os.path.join(folder, name)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(dumps(data))
    print("wrote", os.path.relpath(path, REPO))


def dumps(data):
    """JSON with ASCII escapes, so every line is plain and short: a
    list of cases is one case per line."""
    if isinstance(data, dict):
        parts = []
        for k, v in data.items():
            if isinstance(v, list):
                inner = ",\n".join(" " + json.dumps(x) for x in v)
                parts.append(json.dumps(k) + ": [\n" + inner + "\n]")
            else:
                parts.append(json.dumps(k) + ": " + json.dumps(v))
        return "{\n" + ",\n".join(parts) + "\n}\n"
    return json.dumps(data, indent=1) + "\n"


def version():
    return "%d.%d.%d" % sys.version_info[:3]
