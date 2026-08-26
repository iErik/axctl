package niri

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"axctl/pkg/ipc"
)

type Niri struct {
	socketPath string
	mu         sync.Mutex
}

func New() (*Niri, error) {
	path := os.Getenv("NIRI_SOCKET")
	if path == "" {
		return nil, fmt.Errorf("NIRI_SOCKET not set")
	}
	return &Niri{socketPath: path}, nil
}

func (n *Niri) request(req interface{}, resp interface{}) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	conn, err := net.Dial("unix", n.socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}

	// Niri answers with a bare `{"Ok": …}` or `{"Err": …}` — there is no
	// enclosing "Reply" object.
	var reply struct {
		Ok  json.RawMessage `json:"Ok"`
		Err json.RawMessage `json:"Err"`
	}

	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		return err
	}

	if len(reply.Err) > 0 && string(reply.Err) != "null" {
		return fmt.Errorf("niri error: %s", string(reply.Err))
	}

	if resp == nil {
		return nil
	}

	if len(reply.Ok) == 0 {
		return fmt.Errorf("niri returned an empty reply")
	}

	return json.Unmarshal(unwrapVariant(reply.Ok), resp)
}

// unwrapVariant strips the tag off a serde-tagged enum. Niri wraps every query
// result in the name of the request that produced it — `{"Windows": […]}`,
// `{"Outputs": {…}}` — so the body the caller wants is one level in. Anything
// that is not a single-key object (an action's `"Handled"`, say) is returned
// untouched.
func unwrapVariant(raw json.RawMessage) json.RawMessage {
	var variant map[string]json.RawMessage
	if err := json.Unmarshal(raw, &variant); err != nil || len(variant) != 1 {
		return raw
	}
	for _, body := range variant {
		return body
	}
	return raw
}

func (n *Niri) parseWindowID(id string) (int, error) {
	var idInt int
	if _, err := fmt.Sscanf(id, "%d", &idInt); err != nil {
		return 0, err
	}
	return idInt, nil
}

func (n *Niri) ListWindows() ([]ipc.Window, error) {
	// First get workspace->output mapping for MonitorID resolution
	workspaces, _ := n.ListWorkspaces()
	wsOutputMap := make(map[string]string)
	for _, ws := range workspaces {
		wsOutputMap[ws.ID] = ws.MonitorID
	}

	var niriWindows []struct {
		ID           int     `json:"id"`
		Title        *string `json:"title"`
		AppID        *string `json:"app_id"`
		WorkspaceID  *int    `json:"workspace_id"`
		IsFloating   bool    `json:"is_floating"`
		IsFullscreen bool    `json:"is_fullscreen"`
		IsFocused    bool    `json:"is_focused"`
	}

	err := n.request("Windows", &niriWindows)
	if err != nil {
		return nil, err
	}

	windows := make([]ipc.Window, len(niriWindows))
	for i, w := range niriWindows {
		title := ""
		if w.Title != nil {
			title = *w.Title
		}
		class := ""
		if w.AppID != nil {
			class = *w.AppID
		}
		wsID := ""
		if w.WorkspaceID != nil {
			wsID = fmt.Sprintf("%d", *w.WorkspaceID)
		}
		monitorID := ""
		if wsID != "" {
			monitorID = wsOutputMap[wsID]
		}

		windows[i] = ipc.Window{
			ID:           fmt.Sprintf("%d", w.ID),
			Title:        title,
			AppID:        class,
			WorkspaceID:  wsID,
			IsFocused:    w.IsFocused,
			IsFloating:   w.IsFloating,
			IsFullscreen: w.IsFullscreen,
			IsHidden:     false,
			Metadata: map[string]interface{}{
				"monitor_id": monitorID,
			},
		}
	}
	return windows, nil
}

func (n *Niri) ActiveWindow() (string, error) {
	var window *struct {
		ID int `json:"id"`
	}
	err := n.request("FocusedWindow", &window)
	if err != nil {
		return "", err
	}
	if window == nil {
		return "", nil
	}
	return fmt.Sprintf("%d", window.ID), nil
}

func (n *Niri) FocusWindow(id string) error {
	idInt, err := n.parseWindowID(id)
	if err != nil {
		return err
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{
			"FocusWindow": map[string]interface{}{"id": idInt},
		},
	}, nil)
}

func (n *Niri) FocusDir(direction string) error {
	action := ""
	switch direction {
	case "l":
		action = "FocusColumnLeft"
	case "r":
		action = "FocusColumnRight"
	case "u":
		action = "FocusWindowUp"
	case "d":
		action = "FocusWindowDown"
	default:
		return fmt.Errorf("invalid direction")
	}
	return n.request(map[string]interface{}{
		"Action": action,
	}, nil)
}

func (n *Niri) CloseWindow(id string) error {
	args := map[string]interface{}{}
	if id != "" {
		idInt, err := n.parseWindowID(id)
		if err != nil {
			return err
		}
		args["id"] = idInt
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"CloseWindow": args},
	}, nil)
}

func (n *Niri) MoveWindow(id string, direction string) error {
	action := ""
	switch direction {
	case "l":
		action = "MoveColumnLeft"
	case "r":
		action = "MoveColumnRight"
	case "u":
		action = "MoveWindowUp"
	case "d":
		action = "MoveWindowDown"
	default:
		return fmt.Errorf("invalid direction")
	}
	return n.request(map[string]interface{}{
		"Action": action,
	}, nil)
}

func (n *Niri) ResizeWindow(id string, width, height int) error {
	err := n.request(map[string]interface{}{
		"Action": map[string]interface{}{
			"SetWindowWidth": map[string]interface{}{
				"width": map[string]interface{}{"Fixed": width},
			},
		},
	}, nil)
	if err != nil {
		return err
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{
			"SetWindowHeight": map[string]interface{}{
				"height": map[string]interface{}{"Fixed": height},
			},
		},
	}, nil)
}

func (n *Niri) ToggleFloating(id string) error {
	args := map[string]interface{}{}
	if id != "" {
		idInt, err := n.parseWindowID(id)
		if err != nil {
			return err
		}
		args["id"] = idInt
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"ToggleWindowFloating": args},
	}, nil)
}

func (n *Niri) SetFullscreen(id string, state bool) error {
	// Check current state before toggling (like Hyprland does)
	windows, err := n.ListWindows()
	if err != nil {
		return err
	}

	targetID := id
	if targetID == "" {
		targetID, _ = n.ActiveWindow()
	}

	isFs := false
	for _, w := range windows {
		if w.ID == targetID {
			isFs = w.IsFullscreen
			break
		}
	}

	// Already in requested state, nothing to do
	if isFs == state {
		return nil
	}

	args := map[string]interface{}{}
	if id != "" {
		idInt, err := n.parseWindowID(id)
		if err != nil {
			return err
		}
		args["id"] = idInt
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"FullscreenWindow": args},
	}, nil)
}

func (n *Niri) SetMaximized(id string, state bool) error {
	action := "MaximizeWindow"
	if !state {
		action = "UnmaximizeWindow"
	}
	return n.request(map[string]interface{}{
		"Action": action,
	}, nil)
}

func (n *Niri) PinWindow(id string, state bool) error {
	return ipc.ErrNotSupported
}

func (n *Niri) ToggleGroup(id string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) GroupNav(direction string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) SetLayoutProperty(id string, key, value string) error {
	switch key {
	case "column-width":
		return n.request(map[string]interface{}{
			"Action": map[string]interface{}{
				"SetWindowWidth": map[string]interface{}{
					"width": map[string]interface{}{"Fixed": value},
				},
			},
		}, nil)
	default:
		return ipc.ErrNotSupported
	}
}

func (n *Niri) ListWorkspaces() ([]ipc.Workspace, error) {
	var niriWorkspaces []struct {
		ID             int     `json:"id"`
		Idx            int     `json:"idx"`
		Name           *string `json:"name"`
		Output         *string `json:"output"`
		IsActive       bool    `json:"is_active"`
		IsFocused      bool    `json:"is_focused"`
		ActiveWindowID *int    `json:"active_window_id"`
	}

	err := n.request("Workspaces", &niriWorkspaces)
	if err != nil {
		return nil, err
	}

	res := make([]ipc.Workspace, len(niriWorkspaces))
	for i, w := range niriWorkspaces {
		name := ""
		if w.Name != nil {
			name = *w.Name
		}
		output := ""
		if w.Output != nil {
			output = *w.Output
		}
		activeWindowID := ""
		if w.ActiveWindowID != nil {
			activeWindowID = fmt.Sprintf("%d", *w.ActiveWindowID)
		}
		res[i] = ipc.Workspace{
			ID:        fmt.Sprintf("%d", w.ID),
			Name:      name,
			MonitorID: output,
			IsActive:  w.IsActive,
			IsEmpty:   false,
			Metadata: map[string]interface{}{
				"focused":          w.IsFocused,
				"index":            w.Idx,
				"active_window_id": activeWindowID,
			},
		}
	}
	return res, nil
}

func (n *Niri) ActiveWorkspace() (*ipc.Workspace, error) {
	workspaces, err := n.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, ws := range workspaces {
		if ws.Metadata["focused"].(bool) {
			return &ws, nil
		}
	}
	return nil, fmt.Errorf("no focused workspace found")
}

func (n *Niri) SwitchWorkspace(id string) error {
	var idInt int
	if _, err := fmt.Sscanf(id, "%d", &idInt); err == nil {
		return n.request(map[string]interface{}{
			"Action": map[string]interface{}{
				"FocusWorkspace": map[string]interface{}{
					"reference": map[string]interface{}{"Id": idInt},
				},
			},
		}, nil)
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{
			"FocusWorkspace": map[string]interface{}{
				"reference": map[string]interface{}{"Name": id},
			},
		},
	}, nil)
}

func (n *Niri) MoveToWorkspace(windowID, workspaceID string) error {
	args := map[string]interface{}{}

	var wsIDInt int
	if _, err := fmt.Sscanf(workspaceID, "%d", &wsIDInt); err == nil {
		args["reference"] = map[string]interface{}{"Id": wsIDInt}
	} else {
		args["reference"] = map[string]interface{}{"Name": workspaceID}
	}

	if windowID != "" {
		idInt, err := n.parseWindowID(windowID)
		if err != nil {
			return err
		}
		args["window_id"] = idInt
	}

	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"MoveWindowToWorkspace": args},
	}, nil)
}

type niriOutput struct {
	Name  string `json:"name"`
	Make  string `json:"make"`
	Model string `json:"model"`
	Modes []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
		// Millihertz, not Hz.
		RefreshRate int `json:"refresh_rate"`
	} `json:"modes"`
	CurrentMode *int `json:"current_mode"`
	Logical     *struct {
		X         int     `json:"x"`
		Y         int     `json:"y"`
		Width     int     `json:"width"`
		Height    int     `json:"height"`
		Scale     float64 `json:"scale"`
		Transform string  `json:"transform"`
	} `json:"logical"`
}

// FocusedOutput is a separate query: the Outputs reply says nothing about
// which one has focus, and a caller with no focused monitor cannot place
// anything on screen.
func (n *Niri) focusedOutputName() string {
	var out *struct {
		Name string `json:"name"`
	}
	if err := n.request("FocusedOutput", &out); err != nil || out == nil {
		return ""
	}
	return out.Name
}

func (n *Niri) ListMonitors() ([]ipc.Monitor, error) {
	// Keyed by output name, not a list.
	var niriOutputs map[string]niriOutput
	if err := n.request("Outputs", &niriOutputs); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(niriOutputs))
	for name := range niriOutputs {
		names = append(names, name)
	}
	sort.Strings(names)

	focusedName := n.focusedOutputName()

	// Which workspace each output is showing — the shell reads it off the
	// monitor rather than cross-referencing the workspace list.
	activeWorkspace := make(map[string]string)
	if workspaces, err := n.ListWorkspaces(); err == nil {
		for _, ws := range workspaces {
			if ws.IsActive {
				activeWorkspace[ws.MonitorID] = ws.ID
			}
		}
	}

	res := make([]ipc.Monitor, len(names))
	for i, name := range names {
		o := niriOutputs[name]
		m := ipc.Monitor{
			ID:          name,
			Name:        name,
			Description: strings.TrimSpace(fmt.Sprintf("%s %s", o.Make, o.Model)),
			IsFocused:   name == focusedName,
			Metadata: map[string]interface{}{
				"active_workspace": activeWorkspace[name],
			},
		}
		if o.Logical != nil {
			m.Width = o.Logical.Width
			m.Height = o.Logical.Height
			m.Metadata["x"] = o.Logical.X
			m.Metadata["y"] = o.Logical.Y
			m.Scale = o.Logical.Scale
			m.Metadata["transform"] = niriTransformToInt(o.Logical.Transform)
		}
		if o.CurrentMode != nil && *o.CurrentMode < len(o.Modes) {
			mode := o.Modes[*o.CurrentMode]
			m.RefreshRate = float64(mode.RefreshRate) / 1000.0
			if m.Width == 0 {
				m.Width = mode.Width
			}
			if m.Height == 0 {
				m.Height = mode.Height
			}
		}
		res[i] = m
	}
	return res, nil
}

func niriTransformToInt(t string) int {
	switch t {
	case "Normal":
		return 0
	case "90":
		return 1
	case "180":
		return 2
	case "270":
		return 3
	case "Flipped":
		return 4
	case "Flipped90":
		return 5
	case "Flipped180":
		return 6
	case "Flipped270":
		return 7
	default:
		return 0
	}
}

func (n *Niri) FocusMonitor(id string) error {
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{
			"FocusOutput": map[string]interface{}{"output": id},
		},
	}, nil)
}

func (n *Niri) MoveToMonitor(windowID, monitorID string) error {
	args := map[string]interface{}{"output": monitorID}
	if windowID != "" {
		idInt, err := n.parseWindowID(windowID)
		if err != nil {
			return err
		}
		args["window_id"] = idInt
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"MoveWindowToOutput": args},
	}, nil)
}

func (n *Niri) MoveWindowPixel(id string, x, y int) error {
	args := map[string]interface{}{
		"x": map[string]interface{}{"SetFixed": float64(x)},
		"y": map[string]interface{}{"SetFixed": float64(y)},
	}
	if id != "" {
		idInt, err := n.parseWindowID(id)
		if err != nil {
			return err
		}
		args["id"] = idInt
	}
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"MoveFloatingWindow": args},
	}, nil)
}

func (n *Niri) MoveToWorkspaceSilent(windowID, workspaceID string) error {
	return n.MoveToWorkspace(windowID, workspaceID)
}

func (n *Niri) ToggleSpecialWorkspace(name string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) GetConfig(key string) (interface{}, error) {
	return nil, ipc.ErrNotSupported
}

func (n *Niri) BatchConfig(configs map[string]interface{}) error {
	for k, v := range configs {
		if err := n.SetConfig(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (n *Niri) BatchKeybinds(jsonPayload string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) RawBatch(command string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) GetAnimations() (interface{}, error) {
	return nil, ipc.ErrNotSupported
}

func (n *Niri) GetCursorPosition() (int, int, error) {
	return 0, 0, ipc.ErrNotSupported
}

func (n *Niri) BindKey(mods, key, command string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) UnbindKey(mods, key string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) SetLayout(name string) error {
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{
			"SwitchLayout": map[string]interface{}{
				"layout": map[string]interface{}{"Named": name},
			},
		},
	}, nil)
}

func (n *Niri) SetConfig(key string, value interface{}) error {
	switch key {
	case "border.active_color", "border.inactive_color":
		_ = ipc.FirstColor(fmt.Sprintf("%v", value))
		return ipc.ErrNotSupported
	default:
		return ipc.ErrNotSupported
	}
}

func (n *Niri) ReloadConfig() error {
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"LoadConfigFile": map[string]interface{}{}},
	}, nil)
}

func (n *Niri) SetDpms(monitorID string, on bool) error {
	action := "Off"
	if on {
		action = "On"
	}
	return n.request(map[string]interface{}{
		"Output": map[string]interface{}{
			"output": monitorID,
			"action": action,
		},
	}, nil)
}

func (n *Niri) Execute(command string) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}

	fields := strings.SplitN(cmd, " ", 2)
	dispatcher := fields[0]
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(fields[1])
	}

	switch strings.ToLower(dispatcher) {
	case "exec":
		return n.request(map[string]interface{}{
			"Action": map[string]interface{}{
				"Spawn": map[string]interface{}{"command": []string{"sh", "-c", arg}},
			},
		}, nil)
	case "killactive", "closewindow":
		return n.CloseWindow("")
	case "fullscreen":
		return n.SetFullscreen("", true)
	case "togglefloating":
		return n.ToggleFloating("")
	case "movefocus":
		return n.FocusDir(arg)
	case "movewindow":
		return n.MoveWindow("", arg)
	case "workspace":
		switch arg {
		case "r+1", "e+1", "+1":
			return n.request(map[string]interface{}{
				"Action": map[string]interface{}{"FocusWorkspaceDown": map[string]interface{}{}},
			}, nil)
		case "r-1", "e-1", "-1":
			return n.request(map[string]interface{}{
				"Action": map[string]interface{}{"FocusWorkspaceUp": map[string]interface{}{}},
			}, nil)
		default:
			if strings.HasPrefix(arg, "special") {
				fmt.Printf("axctl niri: ignoring special workspace command %q\n", cmd)
				return nil
			}
			return n.SwitchWorkspace(arg)
		}
	case "movetoworkspace", "movetoworkspacesilent":
		if strings.HasPrefix(arg, "special") {
			fmt.Printf("axctl niri: ignoring special workspace command %q\n", cmd)
			return nil
		}
		return n.MoveToWorkspace("", arg)
	case "togglespecialworkspace":
		fmt.Printf("axctl niri: ignoring special workspace command %q\n", cmd)
		return nil
	case "movewindowpixel":
		fmt.Printf("axctl niri: ignoring pixel move %q (tiled niri windows have no pixel placement)\n", cmd)
		return nil
	case "layoutmsg":
		parts := strings.Fields(arg)
		if len(parts) >= 2 && parts[0] == "colresize" {
			delta := parts[1]
			if !strings.HasSuffix(delta, "%") && (strings.HasPrefix(delta, "+") || strings.HasPrefix(delta, "-")) {
				delta += "%"
			}
			return n.SetLayoutProperty("", "width", delta)
		}
		fmt.Printf("axctl niri: ignoring unsupported layoutmsg %q\n", cmd)
		return nil
	default:
		fmt.Printf("axctl niri: ignoring unsupported command %q\n", cmd)
		return nil
	}
}

func (n *Niri) Exit() error {
	return n.request(map[string]interface{}{
		"Action": map[string]interface{}{"Quit": map[string]interface{}{}},
	}, nil)
}

func (n *Niri) Subscribe() (<-chan ipc.Event, error) {
	conn, err := net.Dial("unix", n.socketPath)
	if err != nil {
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode("EventStream"); err != nil {
		conn.Close()
		return nil, err
	}

	ch := make(chan ipc.Event, 64)
	go func() {
		defer conn.Close()
		defer close(ch)
		dec := json.NewDecoder(conn)
		for {
			// Niri writes the reply to the EventStream request first, then
			// one bare `{"<EventName>": {…}}` object per event — there is no
			// enclosing "Event" object to unwrap.
			var eventWrapper map[string]json.RawMessage
			if err := dec.Decode(&eventWrapper); err != nil {
				break
			}
			if _, isReply := eventWrapper["Ok"]; isReply {
				continue
			}

			event := ipc.Event{
				Timestamp: time.Now().Unix(),
				Payload:   make(map[string]interface{}),
			}

			for name, data := range eventWrapper {
				switch name {
				case "WorkspacesChanged":
					event.Type = ipc.EventWorkspaceChanged
				case "WorkspaceActivated":
					event.Type = ipc.EventWorkspaceChanged
					var d struct {
						ID int `json:"id"`
					}
					json.Unmarshal(data, &d)
					event.Payload["id"] = fmt.Sprintf("%d", d.ID)
					event.Payload["name"] = fmt.Sprintf("%d", d.ID)
				case "WindowOpened":
					event.Type = ipc.EventWindowCreated
					var d struct {
						Window struct {
							ID    int     `json:"id"`
							Title *string `json:"title"`
							AppID *string `json:"app_id"`
						} `json:"window"`
					}
					json.Unmarshal(data, &d)
					title := ""
					if d.Window.Title != nil {
						title = *d.Window.Title
					}
					class := ""
					if d.Window.AppID != nil {
						class = *d.Window.AppID
					}
					event.Window = &ipc.Window{
						ID:    fmt.Sprintf("%d", d.Window.ID),
						Title: title,
						AppID: class,
					}
				case "WindowClosed":
					event.Type = ipc.EventWindowClosed
					var d struct {
						ID int `json:"id"`
					}
					json.Unmarshal(data, &d)
					event.Payload["id"] = fmt.Sprintf("%d", d.ID)
				case "WindowFocused":
					event.Type = ipc.EventWindowFocused
					var d struct {
						ID *int `json:"id"`
					}
					json.Unmarshal(data, &d)
					if d.ID != nil {
						event.Payload["id"] = fmt.Sprintf("%d", *d.ID)
					}
				case "WindowOpenedOrChanged":
					// This event fires when a window's properties change (title, app_id, etc.),
					// NOT when focus changes. Map to WindowTitleChanged.
					event.Type = ipc.EventWindowTitleChanged
					var d struct {
						Window struct {
							ID    int     `json:"id"`
							Title *string `json:"title"`
							AppID *string `json:"app_id"`
						} `json:"window"`
					}
					json.Unmarshal(data, &d)
					title := ""
					if d.Window.Title != nil {
						title = *d.Window.Title
					}
					class := ""
					if d.Window.AppID != nil {
						class = *d.Window.AppID
					}
					event.Window = &ipc.Window{
						ID:    fmt.Sprintf("%d", d.Window.ID),
						Title: title,
						AppID: class,
					}
					event.Payload["id"] = fmt.Sprintf("%d", d.Window.ID)
					event.Payload["title"] = title
				case "WindowsChanged":
					// Global window list changed — trigger cache refresh
					event.Type = ipc.EventWorkspaceChanged
				case "KeyboardLayoutsChanged":
					event.Type = ipc.EventConfigReloaded
				case "ConfigLoaded":
					event.Type = ipc.EventConfigReloaded
				}
			}

			if event.Type != "" {
				select {
				case ch <- event:
				default:
				}
			}
		}
	}()

	return ch, nil
}

func (n *Niri) SwitchKeyboardLayout(action string) error {
	var layoutArg interface{} = "Next"
	if action == "prev" {
		layoutArg = "Prev"
	} else if action != "next" {
		var idx int
		if _, err := fmt.Sscanf(action, "%d", &idx); err == nil {
			layoutArg = idx
		}
	}
	// For Niri, it's either "Next", "Prev", or integer index
	req := map[string]interface{}{
		"Action": map[string]interface{}{"SwitchLayout": layoutArg},
	}
	var resp interface{}
	return n.request(req, &resp)
}

func (n *Niri) SetKeyboardLayouts(layouts string, variants string) error {
	return ipc.ErrNotSupported
}

func (n *Niri) GetCapabilities() (ipc.Capabilities, error) {
	return ipc.Capabilities{
		ID:           "niri",
		Layouts:      []string{},
		LayoutSwitch: false,
		Blur:         false,
		Shadows:      true,
		Shadow: ipc.ShadowCapabilities{
			Enabled: true, Size: true, Color: true, Offset: true,
			RenderPower: false, Scale: false, Sharp: false, IgnoreWindow: false,
		},
		Animations:          true,
		RoundedCorners:      true,
		WorkspacesSupported: true,
		WindowsSupported:    true,
		SpecialWorkspaces:   false,
		InnerOuterGaps:      false,
	}, nil
}
