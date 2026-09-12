"""Refuse an incomplete, skipped, or failed Kubernetes conformance run."""

import json
from pathlib import Path
import re
import sys


def roster(source):
    block = re.search(r'^var runtimeBehaviors = \[\]Behavior\{\n(.*?)^\}', source, re.M | re.S)
    if not block:
        raise ValueError('the runtime behavior roster could not be read')
    names = []
    for raw in block[1].splitlines():
        line = raw.strip()
        if not line or line.startswith('//'):
            continue
        entry = re.match(r'^\{"([^"]+)",', line)
        if not entry:
            raise ValueError('an entry in the runtime roster could not be read')
        names.append(entry[1])
    if not names or len(set(names)) != len(names):
        raise ValueError('the runtime roster is empty or has duplicate names')
    return names


def verify(lines, names):
    expected = {'TestConformance/' + name for name in names}
    roots = {'TestConformance', 'TestImmediateStartupCannotBypassContainment'}
    verdicts = {}
    package_passed = False
    for line in lines:
        event = json.loads(line)
        if event.get('Package') != 'github.com/antifailure/antifailure/engine/internal/runtime/k8s':
            continue
        action, name = event.get('Action'), event.get('Test')
        if not name:
            if action in ('pass', 'fail', 'skip'):
                package_passed = action == 'pass'
            continue
        if name.split('/')[0] not in roots or action not in ('pass', 'fail', 'skip'):
            continue
        if action != 'pass':
            raise ValueError(f'{name} reported {action}, so the runtime is not proved')
        if name.count('/') > 1:
            continue
        if name in verdicts:
            raise ValueError(f'{name} has more than one verdict')
        verdicts[name] = action
    observed = {name for name in verdicts if name.startswith('TestConformance/')}
    if not package_passed or not roots.issubset(verdicts) or observed != expected:
        missing, extra = sorted(expected - observed), sorted(observed - expected)
        raise ValueError(f'incomplete runtime proof: missing={missing}, missing_roots={sorted(roots-set(verdicts))}, unexpected={extra}, package_passed={package_passed}')
    return len(expected)


if __name__ == '__main__':
    try:
        root = Path(__file__).resolve().parents[1]
        names = roster((root / 'engine/conformance/runtime.go').read_text())
        count = verify(Path(sys.argv[1]).read_text().splitlines(), names)
        print(f'runtimeproof: {count} of {count} behaviors and immediate startup containment passed, zero skipped')
    except (ValueError, OSError, IndexError) as error:
        print(f'runtimeproof: {error}', file=sys.stderr)
        sys.exit(1)
