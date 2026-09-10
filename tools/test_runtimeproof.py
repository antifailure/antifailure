import json
import unittest
from runtimeproof import roster, verify


class RuntimeProofTests(unittest.TestCase):
    def events(self, action='pass'):
        package = 'github.com/antifailure/antifailure/engine/internal/runtime/k8s'
        return [json.dumps({'Package': package, 'Test': name, 'Action': action})
                for name in ['TestConformance/A', 'TestConformance/B', 'TestConformance', None]]

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
