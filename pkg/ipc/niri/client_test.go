package niri

import (
	"encoding/json"
	"testing"
)

// Every niri query answers with a tagged enum naming the request that produced
// it. Decoding the tag instead of the body is what left the daemon's cache
// empty, and an empty monitor list means the shell can never place a surface.
func TestUnwrapVariant(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"list payload", `{"Windows":[{"id":1}]}`, `[{"id":1}]`},
		{"map payload", `{"Outputs":{"DP-2":{"name":"DP-2"}}}`, `{"DP-2":{"name":"DP-2"}}`},
		{"object payload", `{"FocusedOutput":{"name":"DP-2"}}`, `{"name":"DP-2"}`},
		{"null payload", `{"FocusedWindow":null}`, `null`},
		{"action reply is not a variant", `"Handled"`, `"Handled"`},
		{"bare list is not a variant", `[1,2]`, `[1,2]`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(unwrapVariant(json.RawMessage(c.in)))
			if got != c.want {
				t.Errorf("unwrapVariant(%s) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

// Outputs is keyed by connector name; decoding it as an array yields nothing.
func TestOutputsDecodeAsMap(t *testing.T) {
	const payload = `{"DP-2":{"name":"DP-2","make":"LG","model":"HDR WFHD",
		"modes":[{"width":2560,"height":1080,"refresh_rate":59978}],
		"current_mode":0,
		"logical":{"x":1920,"y":0,"width":2560,"height":1080,"scale":1.0,"transform":"Normal"}}}`

	var outputs map[string]niriOutput
	if err := json.Unmarshal([]byte(payload), &outputs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	o, ok := outputs["DP-2"]
	if !ok {
		t.Fatalf("DP-2 missing from %v", outputs)
	}
	// Niri reports millihertz; the shell renders this value as-is.
	if got := float64(o.Modes[0].RefreshRate) / 1000.0; got != 59.978 {
		t.Errorf("refresh rate = %v, want 59.978", got)
	}
	if o.Logical.X != 1920 {
		t.Errorf("logical x = %d, want 1920", o.Logical.X)
	}
}
