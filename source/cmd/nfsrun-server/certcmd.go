package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nfsrun-server/internal/config"
	"nfsrun-server/internal/protocert"
)

// The Redirector certificate is made and checked by the server itself
// (internal/protocert); first-run setup and start_server call these.

// gameExeFor: the patched game exe the race servers are made from.
func gameExeFor(c config.Config, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if c.DSGameDir == "" {
		return ""
	}
	return filepath.Join(c.DSGameDir, c.DSSourceExe)
}

// certTarget: which domain/hosts the certificate must serve and where they
// came from. The patched game exe is the truth (the game connects to the
// hosts written in it); an explicit --game-exe must be readable and patched,
// the DS_GAME_DIR exe may still be retail (then BASE_DOMAIN / --domain).
func certTarget(c config.Config, domainFlag, exeFlag string) (domain string, hosts []string, from string, note string, err error) {
	domainFlag = strings.ToLower(strings.TrimSpace(domainFlag))
	if exe := gameExeFor(c, exeFlag); exe != "" {
		h, d, e := protocert.ExeHosts(exe)
		switch {
		case e == nil:
			if domainFlag != "" && domainFlag != d {
				return "", nil, "", "", fmt.Errorf("--domain %s, but the game exe %s points to %s", domainFlag, exe, d)
			}
			if bd := strings.ToLower(c.BaseDomain); domainFlag == "" && bd != "" && bd != d {
				note = fmt.Sprintf("BASE_DOMAIN=%s in server.env differs from the game exe (%s): the exe wins, the game connects to the hosts written in it", bd, d)
			}
			return d, h, "game exe " + exe, note, nil
		case exeFlag != "":
			return "", nil, "", "", e
		case errors.Is(e, protocert.ErrRetailExe) || os.IsNotExist(e):
			note = fmt.Sprintf("game exe not used (%v)", e)
		default:
			return "", nil, "", "", e
		}
	}
	switch {
	case domainFlag != "":
		return domainFlag, protocert.HostsFor(domainFlag), "--domain", note, nil
	case c.BaseDomain != "":
		d := strings.ToLower(c.BaseDomain)
		return d, protocert.HostsFor(d), "BASE_DOMAIN in server.env", note, nil
	}
	return "", nil, "", note, fmt.Errorf("no base domain: set DS_GAME_DIR to the patched game folder, BASE_DOMAIN in server.env, or pass --domain")
}

func writeCert(certFile, keyFile, domain, from string) error {
	crt, key, err := protocert.Generate(domain, time.Now())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0755); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405")
	for _, f := range []string{certFile, keyFile} {
		if _, err := os.Stat(f); err == nil {
			_ = os.Rename(f, f+".old-"+stamp)
		}
	}
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(certFile, crt, 0644); err != nil {
		return err
	}
	info := fmt.Sprintf("Hostname=*.%s\r\nIssuer=CN=%s, OU=Online Technology Group, O=%s, L=Redwood City, ST=California, C=US\r\nOuterAlgorithm=rsaEncryption (Old ProtoSSL form)\r\nDomainFrom=%s\r\nGeneratedBy=nfsrun-server --make-cert\r\nGeneratedAt=%s\r\n",
		domain, protocert.IssuerCN, protocert.IssuerO, from, time.Now().Format(time.RFC3339))
	_ = os.WriteFile(strings.TrimSuffix(certFile, filepath.Ext(certFile))+".info.txt", []byte(info), 0644)
	return nil
}

// runCertCommand handles --exe-domain / --make-cert / --check-cert;
// handled=false when none was given.
func runCertCommand(cfgPath, exeDomain string, makeCert, checkCert, fix bool, domainFlag, exeFlag string) (handled bool, code int) {
	if exeDomain != "" {
		_, d, err := protocert.ExeHosts(exeDomain)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return true, 4
		}
		fmt.Println(d)
		return true, 0
	}
	if !makeCert && !checkCert {
		return false, 0
	}
	c, err := config.Load(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "config:", err)
		return true, 2
	}
	domain, hosts, from, note, err := certTarget(c, domainFlag, exeFlag)
	if note != "" {
		fmt.Println("note:", note)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "certificate:", err)
		return true, 4
	}
	if makeCert {
		if err := writeCert(c.CertFile, c.KeyFile, domain, from); err != nil {
			fmt.Fprintln(os.Stderr, "certificate:", err)
			return true, 3
		}
		fmt.Printf("Redirector certificate *.%s written to %s (domain from %s)\n", domain, c.CertFile, from)
		return true, 0
	}
	crt, e1 := os.ReadFile(c.CertFile)
	key, e2 := os.ReadFile(c.KeyFile)
	var problems []string
	switch {
	case e1 != nil:
		problems = []string{e1.Error()}
	case e2 != nil:
		problems = []string{e2.Error()}
	default:
		problems = protocert.Problems(crt, key, hosts, time.Now())
	}
	if len(problems) == 0 {
		fmt.Printf("Redirector certificate OK: %s covers %s (from %s)\n", c.CertFile, strings.Join(hosts, ", "), from)
		return true, 0
	}
	fmt.Printf("Redirector certificate %s will be refused by the game:\n", c.CertFile)
	for _, p := range problems {
		fmt.Println("  -", p)
	}
	if !fix {
		return true, 1
	}
	if err := writeCert(c.CertFile, c.KeyFile, domain, from); err != nil {
		fmt.Fprintln(os.Stderr, "certificate:", err)
		return true, 3
	}
	fmt.Printf("A new certificate *.%s was generated in %s (a replaced one is kept as .old-<date>).\n", domain, c.CertFile)
	return true, 0
}

// logCertCheck: at startup, say loudly when the gateway's certificate cannot
// work for the game (the symptom is only a closed connection after the
// Certificate message in gateway.log).
func logCertCheck(c config.Config, l interface{ Printf(string, ...any) }) {
	domain, hosts, from, note, err := certTarget(c, "", "")
	if note != "" {
		l.Printf("[CERT] %s", note)
	}
	if err != nil {
		l.Printf("[CERT] not checked: %v", err)
		return
	}
	crt, e1 := os.ReadFile(c.CertFile)
	key, e2 := os.ReadFile(c.KeyFile)
	if e1 != nil || e2 != nil {
		l.Printf("[CERT] cannot read %s / %s: %v %v", c.CertFile, c.KeyFile, e1, e2)
		return
	}
	if p := protocert.Problems(crt, key, hosts, time.Now()); len(p) > 0 {
		l.Printf("[CERT] PROBLEM: %s will be refused by the game (domain %s from %s): %s", c.CertFile, domain, from, strings.Join(p, "; "))
		l.Printf("[CERT] fix: stop the server and run  nfsrun-server.exe --check-cert --fix  (start_server.bat does it before starting the gateway)")
		return
	}
	l.Printf("[CERT] Redirector certificate OK for *.%s (%s)", domain, from)
}
