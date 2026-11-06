package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nfsrun-server/internal/config"
	"nfsrun-server/internal/console"
	"nfsrun-server/internal/database"
	"nfsrun-server/internal/server"
)

func main() {
	cfgPath := flag.String("config", "config/server.env", "server config")
	invite := flag.String("invite", "", "make a one-time invite code for a new player and exit (same as --command \"invite <name>\")")
	adminCmd := flag.String("command", "", "run one server-window command (e.g. \"players all\", \"reset-key Name\") against the database and exit; --yes answers its question")
	adminYes := flag.Bool("yes", false, "with --command: confirm without asking")
	exeDomain := flag.String("exe-domain", "", "print the base domain of a patched game exe and exit")
	makeCert := flag.Bool("make-cert", false, "write the Old-ProtoSSL Redirector certificate (CERT_FILE/KEY_FILE) and exit")
	checkCert := flag.Bool("check-cert", false, "check that the Redirector certificate is one the game accepts and exit (1 = not)")
	fixCert := flag.Bool("fix", false, "with --check-cert: generate a new certificate when the check fails")
	certDomain := flag.String("domain", "", "base domain for --make-cert/--check-cert (default: from the game exe, then BASE_DOMAIN)")
	certExe := flag.String("game-exe", "", "patched game exe to read the Redirector hosts from (default: DS_GAME_DIR\\DS_SOURCE_EXE)")
	flag.Parse()
	if handled, code := runCertCommand(*cfgPath, *exeDomain, *makeCert, *checkCert, *fixCert, *certDomain, *certExe); handled {
		os.Exit(code)
	}
	// Started from anywhere: work in the folder the server was extracted to.
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "data")); err == nil {
			_ = os.Chdir(filepath.Dir(exe))
		}
	}
	firstRun(*cfgPath)
	c, e := config.Load(*cfgPath)
	if e != nil {
		fmt.Fprintln(os.Stderr, "config:", e)
		os.Exit(2)
	}
	if _, err := os.Stat(c.CertFile); err != nil { // first start: make the Redirector certificate
		_ = os.MkdirAll(filepath.Dir(c.CertFile), 0o755)
		runCertCommand(*cfgPath, "", false, true, true, "", "")
	}
	_ = os.MkdirAll("logs", 0755)
	logPath := filepath.Join("logs", "server.log")
	lf, e := openRotating(logPath, int64(c.LogMaxMB)<<20, 5)
	if e != nil {
		panic(e)
	}
	defer lf.Close()
	// The full log goes to logs\server.log; the window shows a
	// compact dashboard (CONSOLE_MODE=log brings the old scrolling log back).
	ring := console.NewEventRing(12)
	dashboard := c.ConsoleMode != "log" && console.IsConsole()
	var out io.Writer = io.MultiWriter(lf, ring)
	if !dashboard {
		out = io.MultiWriter(os.Stdout, lf, ring)
	}
	l := log.New(out, "", log.LstdFlags|log.Lmicroseconds)
	fail := func(err error) {
		console.Restore()
		l.Printf("FATAL: %v", err)
		fmt.Fprintf(os.Stderr, "\nFATAL: %v\n(see %s)\n", err, logPath)
		os.Exit(1)
	}
	db, e := database.Open(c.DatabaseEnv)
	if e != nil {
		fail(e)
	}
	if name := strings.TrimSpace(*invite); name != "" {
		*adminCmd = "invite " + quoteArg(name)
	}
	if strings.TrimSpace(*adminCmd) != "" {
		if e := db.Migrate(); e != nil {
			fail(e)
		}
		os.Exit(runAdminCommand(server.New(c, db, log.New(lf, "", log.LstdFlags|log.Lmicroseconds)), *adminCmd, *adminYes))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	l.Printf("==================================================================")
	l.Printf("NFS The Run private server starting")
	l.Printf("config: public=%s/%s blazePort=%d auth=%s session=%dh logTDF=%v", c.PublicHost, c.PublicIP, c.BlazePort, c.AuthMode, c.AuthSessionHours, c.LogTDF)
	logCertCheck(c, l)
	srv := server.New(c, db, l)
	stopFile := filepath.Join("run", "stop.request")
	_ = os.Remove(stopFile) // an old request must not stop this run
	stop := func(reason string) {
		l.Printf("stop requested: %s", reason)
		cancel()
	}
	uiDone := make(chan struct{})
	if dashboard {
		go console.Run(srv.Stats, ring, console.Options{
			Public:  consoleHeader(c),
			LogPath: logPath, StopFile: stopFile,
			Refresh: time.Duration(c.ConsoleRefresh) * time.Second,
			Command: srv.Command, Help: server.CommandHelp,
		}, stop, uiDone)
	} else {
		// log mode: commands typed as lines, results in the log
		go console.CommandLoop(os.Stdin, os.Stdout, srv.Command)
		// log mode: the stop file still works
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-uiDone:
					return
				case <-t.C:
					if _, err := os.Stat(stopFile); err == nil {
						_ = os.Remove(stopFile)
						stop("stop_server.bat")
					}
				}
			}
		}()
	}
	err := srv.Serve(ctx)
	close(uiDone)
	console.Restore()
	if err != nil {
		fail(err)
	}
	fmt.Println("\nServer stopped.")
}

// consoleHeader is the second line of the dashboard: host and public ports.
func consoleHeader(c config.Config) string {
	launcher := "launcher HTTPS off"
	if c.LauncherHTTPSListen != "" {
		launcher = "launcher HTTPS " + strings.TrimPrefix(c.LauncherHTTPSListen, ":")
		if c.LauncherURL != "" {
			launcher += " (via " + c.LauncherURL + ")"
		}
	}
	return fmt.Sprintf("%s  |  Blaze %d  |  %s  |  web Autolog 18443 -> HTTP %s", c.PublicHost, c.BlazePort, launcher, strings.TrimPrefix(c.HTTPListen, ":"))
}
