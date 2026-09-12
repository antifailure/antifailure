import json
import unittest
from runtimeproof import roster, verify


class RuntimeProofTests(unittest.TestCase):
    def events(self, action='pass'):
        package = 'github.com/antifailure/antifailure/engine/internal/runtime/k8s'
        return [json.dumps({'Package': package, 'Test': name, 'Action': action})
                for name in ['TestConformance/A', 'TestConformance/B', 'TestConformance', 'TestImmediateStartupCannotBypassContainment', None]]

    def test_complete_run_is_accepted(self):
        self.assertEqual(verify(self.events(), ['A', 'B']), 2)

    def test_skipped_run_is_refused(self):
        events = self.events()
        row = json.loads(events[0])
        row['Action'] = 'skip'
        events[0] = json.dumps(row)
        with self.assertRaises(ValueError):
            verify(events, ['A', 'B'])

    def test_missing_behavior_is_refused(self):
        with self.assertRaises(ValueError):
            verify(self.events()[1:], ['A', 'B'])

    def test_missing_startup_proof_is_refused(self):
        events = [line for line in self.events() if json.loads(line).get('Test') != 'TestImmediateStartupCannotBypassContainment']
        with self.assertRaises(ValueError):
            verify(events, ['A', 'B'])

    def test_duplicate_verdict_is_refused(self):
        with self.assertRaises(ValueError):
            verify(self.events() + self.events()[:1], ['A', 'B'])

    def test_bad_json_is_not_a_clean_run(self):
        with self.assertRaises(ValueError):
            verify(self.events() + ['not json'], ['A', 'B'])

    def test_a_roster_entry_that_cannot_be_read_is_refused(self):
        with self.assertRaises(ValueError):
            roster('var runtimeBehaviors = []Behavior{\n {"A", "description", ""},\n dynamicName,\n}')

    def test_roster_names_are_read_from_the_declared_entries(self):
        self.assertEqual(roster('var runtimeBehaviors = []Behavior{\n {"A", "description", ""},\n}'), ['A'])

    def test_a_failed_package_is_refused(self):
        # Every behavior can report pass while the package itself fails, and
        # then the package verdict is the only thing left that knows. A test
        # outside the two roots is filtered out before its own fail is read,
        # and a panic in setup produces no per test event at all.
        events = self.events()
        package = json.loads(events[-1])
        package['Action'] = 'fail'
        events[-1] = json.dumps(package)
        with self.assertRaises(ValueError):
            verify(events, ['A', 'B'])

    def test_failed_behavior_is_refused(self):
        events = self.events()
        row = json.loads(events[0])
        row['Action'] = 'fail'
        events[0] = json.dumps(row)
        with self.assertRaises(ValueError):
            verify(events, ['A', 'B'])

    def test_another_package_cannot_supply_the_startup_proof(self):
        # The recipe runs one package. A passing test of the same name in some
        # other package is not this runtime's startup proof, and a reader that
        # accepted it would certify a run of something else.
        startup = 'TestImmediateStartupCannotBypassContainment'
        events = [line for line in self.events() if json.loads(line).get('Test') != startup]
        events.append(json.dumps({'Package': 'github.com/antifailure/antifailure/engine/internal/runtime/local',
                                  'Test': startup, 'Action': 'pass'}))
        with self.assertRaises(ValueError):
            verify(events, ['A', 'B'])
