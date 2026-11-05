package blaze

import "testing"

func TestNames(t *testing.T) {
	cases := map[string]string{
		CommandName(0x0001, 0x0028):      "Authentication.login",
		CommandName(0x0001, 0x006E):      "Authentication.loginPersona",
		CommandName(0x0009, 0x0002):      "Util.ping",
		CommandName(0x0009, 0x0008):      "Util.postAuth",
		CommandName(0x7802, 0x0014):      "UserSessions.updateNetworkInfo",
		CommandName(0x0005, 0x0001):      "Redirector.getServerInstance",
		NotificationName(0x7802, 0x0002): "UserSessions.UserAdded",
		NotificationName(0x7802, 0x0005): "UserSessions.UserUpdated",
		CommandName(0x1234, 0x0001):      "0x1234.0x0001",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}
