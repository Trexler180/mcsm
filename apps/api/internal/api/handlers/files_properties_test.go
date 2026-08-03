package handlers

import "testing"

func TestIsServerProperties(t *testing.T) {
	yes := []string{
		"server.properties",
		"/server.properties",
		" /server.properties ",
		`\server.properties`,
		"/Server.Properties",
	}
	for _, p := range yes {
		if !isServerProperties(p) {
			t.Errorf("isServerProperties(%q) = false, want true", p)
		}
	}

	// A plugin's own copy is not the server's listen config, so writing one must
	// not move the panel's port.
	no := []string{
		"plugins/Geyser/server.properties",
		"/backup/server.properties.bak",
		"server.properties.old",
		"ops.json",
		"",
	}
	for _, p := range no {
		if isServerProperties(p) {
			t.Errorf("isServerProperties(%q) = true, want false", p)
		}
	}
}

func TestPropertiesPort(t *testing.T) {
	body := "#Minecraft server properties\nmotd=Hi\nserver-port=25570\nquery.port=25599\n"
	port, ok := propertiesPort([]byte(body))
	if !ok || port != 25570 {
		t.Errorf("propertiesPort = (%d, %v), want (25570, true)", port, ok)
	}
}

func TestPropertiesPortIgnoresCommentsAndJunk(t *testing.T) {
	cases := []string{
		"#server-port=25570\nmotd=Hi\n",
		"motd=Hi\n",
		"server-port=\n",
		"server-port=notaport\n",
		"server-port=70000\n",
		"server-port=0\n",
	}
	for _, body := range cases {
		if port, ok := propertiesPort([]byte(body)); ok {
			t.Errorf("propertiesPort(%q) = (%d, true), want no port", body, port)
		}
	}
}

func TestPropertiesPortHandlesCRLF(t *testing.T) {
	port, ok := propertiesPort([]byte("motd=Hi\r\nserver-port=25570\r\n"))
	if !ok || port != 25570 {
		t.Errorf("propertiesPort = (%d, %v), want (25570, true)", port, ok)
	}
}
