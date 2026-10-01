package niri

import (
	"strings"
	"testing"

	"axctl/pkg/ipc"
)

func bind(mods []string, key, dispatcher, arg string) ipc.Keybind {
	return ipc.Keybind{
		Modifiers:  mods,
		Key:        key,
		Dispatcher: dispatcher,
		Argument:   arg,
		Enabled:    true,
	}
}

// Niri refuses to load a config it cannot parse, and Ambxst's binds are stored
// in Hyprland's vocabulary, so anything that does not translate has to be
// dropped or rewritten rather than written out verbatim.
func TestGenerateKeybindsRejectsUnrepresentableKeys(t *testing.T) {
	g := &Generator{}
	out := g.GenerateKeybinds(ipc.ConfigKeybinds{
		Custom: []ipc.Keybind{
			bind(nil, "switch:Lid Switch", "exec", "loginctl lock-session"),
			bind(nil, "switch:on:Lid Switch", "exec", "axctl monitor set-dpms 0 0"),
			bind(nil, "switch:off:Lid Switch", "exec", "axctl monitor set-dpms 0 1"),
			bind([]string{"SUPER"}, "D", "exec", "ambxst run launcher"),
		},
	})

	if strings.Contains(out, "switch:") {
		t.Errorf("switch pseudo-keys leaked into binds:\n%s", out)
	}
	if !strings.Contains(out, `Mod+D { spawn-sh "ambxst run launcher"; }`) {
		t.Errorf("ordinary bind missing:\n%s", out)
	}

	wantClose := `        spawn "sh" "-c" "loginctl lock-session; axctl monitor set-dpms 0 0"`
	if !strings.Contains(out, wantClose) {
		t.Errorf("lid-close command not folded together:\n%s", out)
	}
	if !strings.Contains(out, "lid-open {") {
		t.Errorf("lid-open event missing:\n%s", out)
	}
}

func TestGenerateKeybindsMapsPointerKeys(t *testing.T) {
	g := &Generator{}
	out := g.GenerateKeybinds(ipc.ConfigKeybinds{
		Custom: []ipc.Keybind{
			bind([]string{"SUPER"}, "mouse_down", "workspace", "-1"),
			bind([]string{"SUPER"}, "mouse_up", "workspace", "+1"),
		},
	})

	for _, want := range []string{"Mod+WheelScrollDown", "Mod+WheelScrollUp"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mouse_") {
		t.Errorf("Hyprland pointer spelling survived:\n%s", out)
	}
}

// Hyprland runs every bind registered on a combo; niri errors out on the
// second one, taking the whole config with it.
func TestGenerateKeybindsDropsDuplicateCombos(t *testing.T) {
	g := &Generator{}
	out := g.GenerateKeybinds(ipc.ConfigKeybinds{
		Custom: []ipc.Keybind{
			bind([]string{"SUPER"}, "Super_L", "exec", "ambxst run launcher"),
			bind([]string{"SUPER"}, "Super_L", "exec", "ambxst run task-switcher-confirm"),
		},
	})

	if got := strings.Count(out, "Mod+Super_L"); got != 1 {
		t.Errorf("expected Mod+Super_L exactly once, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "ambxst run launcher") {
		t.Errorf("first binding should win:\n%s", out)
	}
}

// Niri cannot express a release trigger. Ambxst's tap-Super-to-launch bind
// sits on the bare modifier, so emitting it as an ordinary bind fires it the
// moment Super goes down — opening the launcher on every Super+<key> chord.
func TestGenerateKeybindsDropsReleaseBinds(t *testing.T) {
	g := &Generator{}
	release := bind([]string{"SUPER"}, "Super_L", "exec", "ambxst run launcher")
	release.Flags = "r"

	locked := bind(nil, "XF86AudioRaiseVolume", "exec", "ambxst volume up")
	locked.Flags = "le"

	// A release bind on an ordinary key is fine as a press bind: both ends
	// belong to the same deliberate chord. Rebinding the launcher to Super+D
	// in the settings keeps the release flag, and dropping it would silently
	// lose the bind.
	onPlainKey := bind([]string{"SUPER"}, "D", "exec", "ambxst run launcher")
	onPlainKey.Flags = "r"

	out := g.GenerateKeybinds(ipc.ConfigKeybinds{
		Custom: []ipc.Keybind{release, onPlainKey, locked},
	})

	if strings.Contains(out, "Super_L") {
		t.Errorf("release bind on a modifier was emitted:\n%s", out)
	}
	if !strings.Contains(out, `Mod+D { spawn-sh "ambxst run launcher"; }`) {
		t.Errorf("release bind on an ordinary key was dropped:\n%s", out)
	}
	want := `    XF86AudioRaiseVolume allow-when-locked=true { spawn-sh "ambxst volume up"; }`
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in:\n%s", want, out)
	}
}
