package nodered

import "net/http"

// serveTestPalette handles the registry lookup for fake runtimes that deploy nodes.
func serveTestPalette(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.Path != "/nodes" {
		return false
	}
	_, _ = w.Write([]byte(`[{"types":["broker","change","comment","custom","debug","exec","function","http in","http response","inject","link in","link out","mqtt in","mqtt out","mqtt-broker","plugin","some-config","status","switch","tls-config","trigger","ui-base"]}]`))
	return true
}
