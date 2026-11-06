package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"nfsrun-server/internal/server"
)

// runAdminCommand: --command "<line>" outside the server window. Players
// online in a running server are not kicked from here; the server window's
// command line does that.
func runAdminCommand(srv *server.Server, line string, yes bool) int {
	res := srv.Command(line, false)
	if res.Confirm != "" {
		ok := yes
		if !ok {
			fmt.Printf("%s (y/n) ", res.Confirm)
			answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			a := strings.ToLower(strings.TrimSpace(answer))
			ok = a == "y" || a == "yes"
		}
		if !ok {
			fmt.Println("Cancelled.")
			return 1
		}
		res = srv.Command(line, true)
	}
	for _, l := range res.Lines {
		fmt.Println(l)
	}
	return 0
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
