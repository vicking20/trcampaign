import concurrent.futures
from pathlib import Path
import sys
import threading
import unittest
from unittest.mock import patch

import blaze

with patch.object(sys, 'argv', ['trserver.py']):
    import trserver


class DedicatedGameTests(unittest.TestCase):
    def setUp(self):
        trserver._local.session = trserver.Session(name='nexus001')
        fixture = Path(__file__).resolve().parent / 'fixtures/create-game.hex'
        self.request = blaze.decode(bytes.fromhex(fixture.read_text()))

    def test_server_persona_callback_receives_bare_session_info(self):
        login = blaze.decode(blaze.encode(trserver.auth_express_login({'MAIL': 'c4win32serv01@ea.com'})))
        persona = blaze.decode(blaze.encode(trserver.auth_login_persona({'PNAM': 'nexus001'})))
        self.assertEqual(persona['BUID'], login['UID'])
        self.assertEqual(persona['PDTL']['PID'], persona['BUID'])
        self.assertEqual(persona['PDTL']['DSNM'], 'nexus001')
        self.assertNotIn('SESS', persona)

    def test_creation_and_finalization_preserve_server_identity_and_endpoint(self):
        reply = blaze.decode(blaze.encode(trserver.gm_create_game(self.request)))
        notifications = trserver.gm_created_game()
        setup = blaze.decode(blaze.encode(next(fields for comp, cmd, fields in notifications if (comp, cmd) == (4, 0x14))))
        game = setup['GAME']
        self.assertEqual(game['GID'], reply['GID'])
        self.assertEqual(game['THST']['HPID'], trserver.session().persona_id)
        self.assertNotEqual(game['THST']['HPID'], trserver.PERSONA_ID)
        self.assertEqual(game['HNET'][1][0]['INIP']['PORT'], 25200)
        self.assertEqual(game['HNET'][1][0]['EXIP']['PORT'], 25200)
        self.assertEqual(setup['PROS'][1], [])
        self.assertEqual(setup['REAS'].value[1]['DCTX'], 0)
        self.assertEqual(game['GSTA'], 1)
        trserver.gm_finalize_game({'GID': reply['GID']})
        self.assertTrue(trserver.session().created_game['ready'])
        state = blaze.decode(blaze.encode(trserver.gm_finalized_game()[0][2]))
        self.assertEqual(state, {'GID': reply['GID'], 'GSTA': 130})
        with self.assertRaises(ValueError):
            trserver.gm_finalize_game({'GID': reply['GID'] + 1})

    def test_connections_keep_separate_identity_and_address(self):
        barrier = threading.Barrier(2)

        def login(name, port):
            trserver._local.session = trserver.Session(name=name)
            trserver.session().address = (1, port, 2, port)
            barrier.wait()
            ident = blaze.decode(blaze.encode(trserver._user_ident()))
            data = blaze.decode(blaze.encode(trserver._ext_data()))
            return ident['NAME'], ident['ID'], data['ADDR'].value[1]['INIP']['PORT']

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            server = pool.submit(login, 'nexus001', 25200)
            player = pool.submit(login, 'TRPlayer', 3659)
            self.assertEqual(server.result(), ('nexus001', trserver._pid('nexus001'), 25200))
            self.assertEqual(player.result(), ('TRPlayer', trserver.PERSONA_ID, 3659))


if __name__ == '__main__':
    unittest.main()
