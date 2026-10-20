"""Wire regressions: python3 -m unittest discover -s server -p 'test_*.py'."""
import unittest

import blaze


class NetworkAddressListTests(unittest.TestCase):
    def test_captured_dedicated_server_address_and_following_fields(self):
        # HNET through NTOP from the real 2026-10-19 createGame request.
        wire = bytes.fromhex(
            "a2e97404030102978a7003a700000000c2fcb4000000"
            "a6ea7003a70000008aa7c08a18c2fcb400b089030000"
            "a67baf0000bb29730000bb4bf00001"
        )
        decoded, consumed = blaze._dec_struct(wire, 0, root=True)
        self.assertEqual(consumed, len(wire))
        self.assertEqual(decoded['HNET'], (blaze.T_STRUCT, [{
            'EXIP': {'IP': 0, 'PORT': 0},
            'INIP': {'IP': 3232238026, 'PORT': 25200},
        }]))
        self.assertEqual(decoded['NTOP'], 1)
        self.assertEqual(decoded['IGNO'], 0)
        self.assertEqual(decoded['NRES'], 0)

    def test_regular_and_empty_struct_lists(self):
        wire = blaze.encode([
            ('FULL', blaze.T_LIST, (blaze.T_STRUCT, [[('ID', blaze.T_INT, 42)]])),
            ('NONE', blaze.T_LIST, (blaze.T_STRUCT, [])),
        ])
        self.assertEqual(blaze.decode(wire), {
            'FULL': (blaze.T_STRUCT, [{'ID': 42}]),
            'NONE': (blaze.T_STRUCT, []),
        })


if __name__ == '__main__':
    unittest.main()
