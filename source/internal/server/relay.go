package server

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// UDP relay between players and race servers.
//
// Behind NAT the player's packets reach the race server from a translated
// port, while the race server only knows the address the player reported to
// Blaze; DirtySock matches peers by address and never answers. The relay
// fixes this without touching the client or the player's router:
//
//	player  ──UDP──▶  public relay port P (this server)  ──▶ 127.0.0.1:6000 (race server)
//	player  ◀──────── P  ◀── local socket 127.0.0.1:Q  ◀──── race server
//
// The player's NotifyGameSetup puts P in HNET; the race server's
// NotifyPlayerJoining puts 127.0.0.1:Q in PNET. Replies go back to wherever
// the player's packets came from.

type udpRelay struct {
	s       *Server
	gid     uint32
	name    string
	pub     *net.UDPConn // players send here (public port P)
	lo      *net.UDPConn // the race server sees the player here (127.0.0.1:Q)
	ds      *net.UDPAddr // race server socket
	expect  net.IP       // the player's public IP (from Blaze), if known
	client  atomic.Pointer[net.UDPAddr]
	in, out atomic.Uint64
	closed  atomic.Bool
	opened  time.Time
	pubPort int
	loPort  int
}

// relayDumpPackets: the first packets each way are logged in hex.
const relayDumpPackets = 2

type relayManager struct {
	mu   sync.Mutex
	used map[int]bool
}

// openRelay starts a relay for one player of a race-server game.
func (s *Server) openRelay(gid uint32, name string, expectIP uint64, ds *net.UDPAddr) (*udpRelay, error) {
	c := s.cfg
	s.relays.mu.Lock()
	if s.relays.used == nil {
		s.relays.used = map[int]bool{}
	}
	var pub *net.UDPConn
	port := 0
	for p := c.RelayPortBase; p < c.RelayPortBase+c.RelayPortCount; p++ {
		if s.relays.used[p] {
			continue
		}
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: p})
		if err != nil {
			continue
		}
		pub, port = conn, p
		s.relays.used[p] = true
		break
	}
	s.relays.mu.Unlock()
	if pub == nil {
		return nil, fmt.Errorf("no free relay port in %d-%d", c.RelayPortBase, c.RelayPortBase+c.RelayPortCount-1)
	}
	lo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		pub.Close()
		s.releaseRelayPort(port)
		return nil, err
	}
	r := &udpRelay{s: s, gid: gid, name: name, pub: pub, lo: lo, ds: ds, opened: time.Now(), pubPort: port, loPort: lo.LocalAddr().(*net.UDPAddr).Port}
	if expectIP != 0 {
		r.expect = net.ParseIP(ipString(expectIP)).To4()
	}
	go r.fromPlayer()
	go r.fromRaceServer()
	s.log.Printf("[RELAY] game %d %q: players' side UDP %d, race server sees the player at 127.0.0.1:%d, race server %s", gid, name, port, r.loPort, ds)
	return r, nil
}

func (s *Server) releaseRelayPort(p int) {
	s.relays.mu.Lock()
	delete(s.relays.used, p)
	s.relays.mu.Unlock()
}

// publicNet: the race server's address as given to this player (HNET).
func (r *udpRelay) publicNet(ip uint64) netInfo {
	return netInfo{Set: true, ExtIP: ip, ExtPort: uint64(r.pubPort), IntIP: ip, IntPort: uint64(r.pubPort)}
}

// raceServerNet: the player's address as given to the race server (PNET).
func (r *udpRelay) raceServerNet() netInfo {
	lo := ipUint("127.0.0.1")
	return netInfo{Set: true, ExtIP: lo, ExtPort: uint64(r.loPort), IntIP: lo, IntPort: uint64(r.loPort)}
}

func (r *udpRelay) fromPlayer() {
	buf := make([]byte, 2048)
	for {
		n, src, err := r.pub.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if prev := r.client.Load(); prev == nil || !prev.IP.Equal(src.IP) || prev.Port != src.Port {
			r.client.Store(src)
			if prev == nil {
				note := ""
				if r.expect != nil && !src.IP.Equal(r.expect) {
					note = fmt.Sprintf(" (Blaze saw this player at %s)", r.expect)
				}
				r.s.log.Printf("[RELAY] game %d %q: first packet from the player at %s%s (%d bytes) -> race server", r.gid, r.name, src, note, n)
			} else {
				r.s.log.Printf("[RELAY] game %d %q: player address changed %s -> %s", r.gid, r.name, prev, src)
			}
		}
		if k := r.in.Add(1); k <= relayDumpPackets {
			r.s.log.Printf("[RELAY] game %d %q: player->race server #%d from %s, %d bytes: % x", r.gid, r.name, k, src, n, buf[:n])
		}
		_, _ = r.lo.WriteToUDP(buf[:n], r.ds)
	}
}

func (r *udpRelay) fromRaceServer() {
	buf := make([]byte, 2048)
	for {
		n, from, err := r.lo.ReadFromUDP(buf)
		if err != nil {
			return
		}
		dst := r.client.Load()
		if dst == nil {
			continue // the player has not sent anything yet
		}
		k := r.out.Add(1)
		if k == 1 {
			r.s.log.Printf("[RELAY] game %d %q: race server answered (%d bytes) -> %s", r.gid, r.name, n, dst)
		}
		if k <= relayDumpPackets {
			r.s.log.Printf("[RELAY] game %d %q: race server->player #%d from %s, %d bytes: % x", r.gid, r.name, k, from, n, buf[:n])
		}
		_, _ = r.pub.WriteToUDP(buf[:n], dst)
	}
}

func (r *udpRelay) close(why string) {
	if r == nil || !r.closed.CompareAndSwap(false, true) {
		return
	}
	r.pub.Close()
	r.lo.Close()
	r.s.releaseRelayPort(r.pubPort)
	r.s.log.Printf("[RELAY] game %d %q closed (%s): %d packets player->race server, %d race server->player, %s",
		r.gid, r.name, why, r.in.Load(), r.out.Load(), time.Since(r.opened).Round(time.Second))
}

// raceServerAddr: where the relay sends a game's player traffic.
func (s *Server) raceServerAddr(g *game) *net.UDPAddr {
	port := s.cfg.DSMeshPort
	if port == 0 {
		port = int(g.dsNet.IntPort)
	}
	host := s.cfg.RelayDSHost
	if host == "" {
		host = "127.0.0.1"
	}
	return &net.UDPAddr{IP: net.ParseIP(host), Port: port}
}
