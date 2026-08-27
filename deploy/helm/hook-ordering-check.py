#!/usr/bin/env python3
"""Refuse a chart whose install-time ordering cannot work.

Helm creates hook resources before the release's normal resources. A hook that
references a Secret or a ServiceAccount belonging to the normal release is
therefore referencing something that does not exist yet.

That failure is unusually expensive to read. The pod is not rejected outright;
it sits in CreateContainerConfigError while the kubelet retries it, and the
install fails eight minutes later saying only "timed out waiting for the
condition". This chart hit it twice in a row on a real cluster, once for a
ServiceAccount and once for a Secret, and neither was visible in the rendered
YAML, which is perfectly valid in both cases.

So the check reads the rendered manifest and asserts, for every hook resource:

  every Secret it names is itself a hook, at a strictly lower weight
  every ServiceAccount it names is itself a hook, or it names none

Usage:
    helm template r chart --set ... | python3 hook-ordering-check.py
"""

import re
import sys

HOOK = re.compile(r'"helm\.sh/hook":\s*(\S+)')
WEIGHT = re.compile(r'"helm\.sh/hook-weight":\s*"(-?\d+)"')
KIND = re.compile(r"^kind: (\S+)", re.M)
NAME = re.compile(r"^  name: (\S+)", re.M)
SECRET_REF = re.compile(r"secretKeyRef:\s*\n\s*name: (\S+)")
SA_REF = re.compile(r"^\s*serviceAccountName: (\S+)", re.M)

# Kinds that run a pod and can therefore be blocked by a missing reference.
RUNS_A_POD = {"Job", "Pod", "CronJob", "Deployment", "StatefulSet", "DaemonSet"}


def parse(text):
    out = {}
    for doc in text.split("\n---\n"):
        k = KIND.search(doc)
        n = NAME.search(doc)
        if not k or not n:
            continue
        w = WEIGHT.search(doc)
        out[n.group(1).strip('"')] = {
            "kind": k.group(1),
            "hook": bool(HOOK.search(doc)),
            "weight": int(w.group(1)) if w else 0,
            "doc": doc,
        }
    return out


def main():
    manifests = parse(sys.stdin.read())
    if not manifests:
        print("hook-ordering-check: nothing on stdin", file=sys.stderr)
        return 2

    problems = []
    for name, r in manifests.items():
        if not r["hook"] or r["kind"] not in RUNS_A_POD:
            continue

        for ref in sorted(set(SECRET_REF.findall(r["doc"]))):
            target = manifests.get(ref)
            if target is None:
                # Supplied by the operator and already present. Nothing to
                # order, so nothing to complain about.
                continue
            if not target["hook"]:
                problems.append(
                    f"{r['kind']} {name} is a hook and reads Secret {ref}, which is a normal "
                    f"release resource. It will not exist when the hook runs."
                )
            elif target["weight"] >= r["weight"]:
                problems.append(
                    f"{r['kind']} {name} (weight {r['weight']}) reads Secret {ref} "
                    f"(weight {target['weight']}). The Secret must have the LOWER weight so it "
                    f"is created first."
                )

        for ref in sorted(set(SA_REF.findall(r["doc"]))):
            target = manifests.get(ref)
            if target is not None and not target["hook"]:
                problems.append(
                    f"{r['kind']} {name} is a hook and runs as ServiceAccount {ref}, which is a "
                    f"normal release resource. The pod cannot be created."
                )

    if problems:
        print("hook-ordering-check: refused.", file=sys.stderr)
        for p in problems:
            print(f"  {p}", file=sys.stderr)
        print(
            "\n  Helm creates hooks before normal resources. A hook may only depend on\n"
            "  resources the operator supplied, or on other hooks with a lower weight.",
            file=sys.stderr,
        )
        return 1

    hooks = sum(1 for r in manifests.values() if r["hook"])
    print(f"hook-ordering-check: {len(manifests)} resources, {hooks} hooks, ordering is sound")
    return 0


if __name__ == "__main__":
    sys.exit(main())
