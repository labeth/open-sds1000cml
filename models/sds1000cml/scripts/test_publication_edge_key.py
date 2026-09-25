import unittest
from check_publication_views import expand_edge_key

class EdgeKeyTest(unittest.TestCase):
    def test_restores_label_and_preserves_duplicate_edges(self):
        graph='N_A --->|R1| N_B\nN_A --->|R1| N_B\n'
        self.assertEqual(expand_edge_key(graph,'|R1 |full relationship'), 'N_A -->|full relationship| N_B\nN_A -->|full relationship| N_B\n')
    def test_rejects_unused_and_duplicate_keys(self):
        for table in ['|R2 |unused','|R1 |one\n|R1 |two']:
            with self.assertRaises(AssertionError):expand_edge_key('N_A -->|R1| N_B',table)
