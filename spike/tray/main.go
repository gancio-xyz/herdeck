// Command tray is a throwaway Phase 0 spike: a menu bar icon that polls herdr,
// shows agent counts in the title, lists workspaces that have an agent and
// focuses one on click. No deck involved.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/systray"
)

type workspace struct {
	ID          string `json:"workspace_id"`
	Number      int    `json:"number"`
	Label       string `json:"label"`
	AgentStatus string `json:"agent_status"`
	Focused     bool   `json:"focused"`
}

type agent struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	AgentStatus string `json:"agent_status"`
}

func socketPath() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

func call(method string, params any, out any) error {
	c, err := net.DialTimeout("unix", socketPath(), time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	req, _ := json.Marshal(map[string]any{"id": "1", "method": method, "params": params})
	if _, err := c.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct{ Code, Message string }
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

var rank = map[string]int{"blocked": 0, "working": 1, "done": 2, "idle": 3, "unknown": 4}
var dot = map[string]string{"blocked": "🟠", "working": "🔵", "done": "🟢", "idle": "⚪️", "unknown": "⚫️"}

func snapshot() ([]workspace, error) {
	var wl struct{ Workspaces []workspace }
	if err := call("workspace.list", map[string]any{}, &wl); err != nil {
		return nil, err
	}
	var al struct{ Agents []agent }
	if err := call("agent.list", map[string]any{}, &al); err != nil {
		return nil, err
	}
	hasAgent := map[string]bool{}
	for _, a := range al.Agents {
		hasAgent[a.WorkspaceID] = true
	}
	var out []workspace
	for _, w := range wl.Workspaces {
		if hasAgent[w.ID] {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].AgentStatus] < rank[out[j].AgentStatus] })
	return out, nil
}

func signature(ws []workspace) string {
	var b strings.Builder
	for _, w := range ws {
		fmt.Fprintf(&b, "%s|%s|%s|%v;", w.ID, w.Label, w.AgentStatus, w.Focused)
	}
	return b.String()
}

func render(ws []workspace) {
	counts := map[string]int{}
	for _, w := range ws {
		counts[w.AgentStatus]++
	}
	var parts []string
	for _, s := range []string{"blocked", "working", "done"} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", dot[s], counts[s]))
		}
	}
	title := strings.Join(parts, " ")
	if title == "" {
		title = "herdeck"
	}
	systray.SetTitle(title)

	systray.ResetMenu()
	for _, w := range ws {
		label := fmt.Sprintf("%s  %s", dot[w.AgentStatus], w.Label)
		if w.Focused {
			label += "  (focused)"
		}
		item := systray.AddMenuItem(label, w.AgentStatus)
		id := w.ID
		go func() {
			for range item.ClickedCh {
				if err := call("workspace.focus", map[string]any{"workspace_id": id}, nil); err != nil {
					log.Printf("focus %s: %v", id, err)
					continue
				}
				exec.Command("open", "-a", "Terminal").Run()
			}
		}()
	}
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit herdeck spike", "")
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()
}

func onReady() {
	systray.SetTitle("herdeck")
	go func() {
		last := "\x00"
		for {
			ws, err := snapshot()
			if err != nil {
				if last != "err" {
					systray.SetTitle("herdeck ⚠︎")
					systray.ResetMenu()
					systray.AddMenuItem("herdr not reachable: "+err.Error(), "").Disable()
					last = "err"
				}
			} else if sig := signature(ws); sig != last {
				render(ws)
				last = sig
			}
			time.Sleep(time.Second)
		}
	}()
}

func main() {
	systray.Run(onReady, func() {})
}
