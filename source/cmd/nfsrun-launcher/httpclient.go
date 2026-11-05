package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nfsrun-server/internal/webcert"
)

// serverPin (SERVER_CERT_PIN): when set, the server's HTTPS certificate is
// accepted only if its public key has this SHA-256; no CA is involved.
var (
	serverPin       string
	serverTransport *http.Transport
	transportOnce   sync.Once
)

func transport() *http.Transport {
	transportOnce.Do(func() {
		t := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		}
		if serverPin != "" {
			pin := serverPin
			t.TLSClientConfig.InsecureSkipVerify = true // replaced by the pin check
			t.TLSClientConfig.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("no server certificate")
				}
				c, err := x509.ParseCertificate(raw[0])
				if err != nil {
					return err
				}
				if webcert.PinOf(c) != pin {
					return errors.New("server certificate does not match SERVER_CERT_PIN")
				}
				return nil
			}
		}
		serverTransport = t
	})
	return serverTransport
}

// serverClient returns a client for the private server; timeout 0 = none
// (live channel).
func serverClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: transport(), Timeout: timeout}
}

// setServerPin applies SERVER_CERT_PIN before the first request.
func setServerPin(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	p, err := webcert.NormalizePin(v)
	if err != nil {
		return err
	}
	serverPin = p
	return nil
}

// fetchGameCode asks the server for a one-time code the game logs in with.
func fetchGameCode(base, sessionKey string) (string, error) {
	req, _ := http.NewRequest("POST", base+"/nfsrun/auth/gamecode", nil)
	req.Header.Set("User-Agent", "nfsrun-launcher")
	req.Header.Set("X-NFSRun-Session", sessionKey)
	resp, err := serverClient(6 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", httpProblem(resp)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.Code, "nfsrun-g1.") {
		return "", fmt.Errorf("server returned an invalid game code")
	}
	return out.Code, nil
}
